//go:build bdd

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	natss "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
	lua "github.com/yuin/gopher-lua"

	"github.com/slidebolt/gateway/internal/history"
)

// ---------------------------------------------------------------------------
// Harness — core in-process gateway, no *testing.T dependency.
// ---------------------------------------------------------------------------

// Harness holds an embedded NATS server, all initialised gateway globals, and
// a running httptest.Server. Call Close() to tear everything down.
type Harness struct {
	natsServer    *natss.Server
	NC            *nats.Conn
	JS            nats.JetStreamContext
	Server        *httptest.Server
	cancelHistory context.CancelFunc
	hist          *history.History
	regSvc        *regsvc.Registry
}

// NewHarness spins up a complete in-process gateway using a temporary directory
// for NATS and SQLite storage. Callers own the lifecycle — call Close() when done.
func NewHarness(tmpDir string) (*Harness, error) {
	opts := natss.Options{Port: -1, JetStream: true, StoreDir: tmpDir + "/nats"}
	s, err := natss.NewServer(&opts)
	if err != nil {
		return nil, fmt.Errorf("NATS server: %w", err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		return nil, fmt.Errorf("NATS server failed to start")
	}

	conn, err := nats.Connect(s.ClientURL())
	if err != nil {
		s.Shutdown()
		return nil, fmt.Errorf("NATS connect: %w", err)
	}

	jsCtx, err := conn.JetStream()
	if err != nil {
		conn.Close()
		s.Shutdown()
		return nil, fmt.Errorf("JetStream: %w", err)
	}
	for _, sc := range []nats.StreamConfig{
		{Name: "EVENTS", Subjects: []string{types.SubjectEntityEvents}, Storage: nats.MemoryStorage, MaxMsgs: 5000},
		{Name: "COMMANDS", Subjects: []string{types.SubjectCommandStatus}, Storage: nats.MemoryStorage, MaxMsgs: 5000},
	} {
		if _, err := jsCtx.AddStream(&sc); err != nil {
			conn.Close()
			s.Shutdown()
			return nil, fmt.Errorf("add stream %s: %w", sc.Name, err)
		}
	}

	regSvc := regsvc.RegistryService(
		"test-gateway",
		regsvc.WithNATS(conn),
		regsvc.WithAggregate(),
		regsvc.WithPersist(regsvc.PersistNever),
	)
	if err := regSvc.LoadAll(); err != nil {
		conn.Close()
		s.Shutdown()
		return nil, fmt.Errorf("registry LoadAll: %w", err)
	}
	if err := regSvc.Start(); err != nil {
		conn.Close()
		s.Shutdown()
		return nil, fmt.Errorf("registry Start: %w", err)
	}

	hist, err := history.Open(tmpDir + "/history.db")
	if err != nil {
		regSvc.Stop()
		conn.Close()
		s.Shutdown()
		return nil, fmt.Errorf("history open: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hist.Start(ctx, conn, jsCtx)

	ResetGlobals()
	nc = conn
	js = jsCtx
	registryService = regSvc
	historyService = hist
	commandService = CommandService()
	subscribeRegistry()

	dynamicEventService = newDynamicEventService()
	if err := dynamicEventService.Start(conn); err != nil {
		return nil, fmt.Errorf("dynamicEventService Start: %w", err)
	}

	router, _ := buildRouter()
	srv := httptest.NewServer(router)

	return &Harness{
		natsServer:    s,
		NC:            conn,
		JS:            jsCtx,
		Server:        srv,
		cancelHistory: cancel,
		hist:          hist,
		regSvc:        regSvc,
	}, nil
}

// Close shuts down all components in dependency order.
func (h *Harness) Close() {
	h.cancelHistory()
	h.Server.Close()
	commandService.Close()
	if dynamicEventService != nil {
		dynamicEventService.Stop()
	}
	h.regSvc.Stop()
	h.hist.Close()
	h.NC.Close()
	h.natsServer.Shutdown()
	ResetGlobals()
}

// Get performs a GET against the test server.
func (h *Harness) Get(path string) (*http.Response, error) {
	return http.Get(h.Server.URL + path)
}

// Post performs a POST with a JSON body against the test server.
func (h *Harness) Post(path string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	return http.Post(h.Server.URL+path, "application/json", strings.NewReader(string(data)))
}

// PostWithHeaders performs a POST with a JSON body and extra headers.
func (h *Harness) PostWithHeaders(path string, body any, headers map[string]string) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, h.Server.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}


func (h *Harness) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodDelete, h.Server.URL+path, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// Patch performs a PATCH with a JSON body against the test server.
func (h *Harness) Patch(path string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, h.Server.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// Put performs a PUT with a JSON body against the test server.
func (h *Harness) Put(path string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPut, h.Server.URL+path, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// ReadJSON reads and unmarshals a response body.
func ReadJSON(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w\nbody: %s", err, body)
	}
	return nil
}

// ---------------------------------------------------------------------------
// *testing.T wrappers — thin adapters for table-driven / unit tests.
// ---------------------------------------------------------------------------

// apiHarness wraps Harness with *testing.T convenience methods.
type apiHarness struct {
	*Harness
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	h, err := NewHarness(t.TempDir())
	if err != nil {
		t.Fatalf("newAPIHarness: %v", err)
	}
	t.Cleanup(h.Close)
	return &apiHarness{h}
}

func (h *apiHarness) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := h.Get(path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (h *apiHarness) post(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	resp, err := h.Post(path, body)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	if err := ReadJSON(resp, dst); err != nil {
		t.Fatalf("readJSON: %v", err)
	}
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("expected HTTP %d, got %d\nbody: %s", want, resp.StatusCode, body)
	}
}

// rpcCodedErr wraps an error with an explicit JSON-RPC error code.
type rpcCodedErr struct {
	code    int
	message string
}

func (e *rpcCodedErr) Error() string { return e.message }

func rpcNotFoundErr(msg string) *rpcCodedErr { return &rpcCodedErr{code: -32004, message: msg} }

// ---------------------------------------------------------------------------

// SimulatedPlugin
// ---------------------------------------------------------------------------

// SimulatedPlugin acts as a real plugin process, subscribing to NATS RPC and
// search subjects. It supports script storage and Lua execution via gopher-lua.
type SimulatedPlugin struct {
	ID       string
	Devices  []types.Device
	Entities []types.Entity

	nc       *nats.Conn
	subs     []*nats.Subscription
	scripts  map[string]string         // entityID → Lua source
	state    map[string]map[string]any // entityID → persisted state
	commands map[string]types.CommandStatus
	mu       sync.RWMutex
}

// entityUpdateWire is the wire format the aggregate registry expects on
// SubjectEntityUpdated / SubjectEntityCreated.
type entityUpdateWire struct {
	Entity   types.Entity `json:"entity"`
	PluginID string       `json:"plugin_id"`
}

// Start subscribes the plugin to all relevant NATS subjects and registers it
// with the gateway. Returns an error if any subscription fails.
func (p *SimulatedPlugin) Start() error {
	p.scripts = make(map[string]string)
	p.state = make(map[string]map[string]any)
	p.commands = make(map[string]types.CommandStatus)

	rpcSubject := fmt.Sprintf("plugins.%s.rpc", p.ID)

	sub, err := p.nc.Subscribe(rpcSubject, func(m *nats.Msg) {
		var req types.Request
		if err := json.Unmarshal(m.Data, &req); err != nil {
			return
		}
		result, rpcErr := p.dispatch(req.Method, req.Params)
		var resp types.Response
		resp.JSONRPC = types.JSONRPCVersion
		if req.ID != nil {
			resp.ID = *req.ID
		}
		if rpcErr != nil {
			code := -32000
			if coded, ok := rpcErr.(*rpcCodedErr); ok {
				code = coded.code
			}
			resp.Error = &types.RPCError{Code: code, Message: rpcErr.Error()}
		} else {
			resp.Result, _ = json.Marshal(result)
		}
		data, _ := json.Marshal(resp)
		_ = m.Respond(data)
	})
	if err != nil {
		return fmt.Errorf("plugin %s: subscribe RPC: %w", p.ID, err)
	}
	p.subs = append(p.subs, sub)

	sub, err = p.nc.Subscribe(types.SubjectSearchPlugins, func(m *nats.Msg) {
		res := types.SearchPluginsResponse{
			PluginID: p.ID,
			Matches:  []types.Manifest{p.manifest()},
		}
		data, _ := json.Marshal(res)
		_ = m.Respond(data)
	})
	if err != nil {
		return fmt.Errorf("plugin %s: subscribe search: %w", p.ID, err)
	}
	p.subs = append(p.subs, sub)

	p.publish()
	time.Sleep(20 * time.Millisecond)
	return nil
}

// MustStart calls Start and fatals the test on error.
func (p *SimulatedPlugin) MustStart(t *testing.T) {
	t.Helper()
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
}

// Stop unsubscribes all NATS subscriptions.
func (p *SimulatedPlugin) Stop() {
	for _, s := range p.subs {
		_ = s.Unsubscribe()
	}
	p.subs = nil
}

func (p *SimulatedPlugin) publish() {
	reg := types.Registration{
		Manifest:   p.manifest(),
		RPCSubject: fmt.Sprintf("plugins.%s.rpc", p.ID),
	}
	data, _ := json.Marshal(reg)
	_ = p.nc.Publish(types.SubjectRegistration, data)
}

func (p *SimulatedPlugin) manifest() types.Manifest {
	return types.Manifest{ID: p.ID, Name: p.ID, Version: "1.0.0"}
}

func (p *SimulatedPlugin) dispatch(method string, params json.RawMessage) (any, error) {
	switch method {
	case "health/check", types.RPCMethodHealthCheck:
		return map[string]string{"status": "ok"}, nil

	case "devices/list":
		return p.Devices, nil

	case "devices/get":
		var args struct{ ID string `json:"id"` }
		_ = json.Unmarshal(params, &args)
		for _, d := range p.Devices {
			if d.ID == args.ID {
				return d, nil
			}
		}
		return nil, fmt.Errorf("device not found")

	case "entities/list":
		var args struct{ DeviceID string `json:"device_id"` }
		_ = json.Unmarshal(params, &args)
		if args.DeviceID == "" {
			return p.Entities, nil
		}
		out := make([]types.Entity, 0)
		for _, e := range p.Entities {
			if e.DeviceID == args.DeviceID {
				out = append(out, e)
			}
		}
		return out, nil

	case "entities/get":
		var args struct {
			DeviceID string `json:"device_id"`
			ID       string `json:"id"`
		}
		_ = json.Unmarshal(params, &args)
		for _, e := range p.Entities {
			if e.ID == args.ID && (args.DeviceID == "" || e.DeviceID == args.DeviceID) {
				return e, nil
			}
		}
		return nil, fmt.Errorf("entity not found")

	case "entities/update":
		var args types.Entity
		_ = json.Unmarshal(params, &args)
		p.mu.Lock()
		var updated types.Entity
		found := false
		for i := range p.Entities {
			e := &p.Entities[i]
			if e.ID == args.ID && (args.DeviceID == "" || e.DeviceID == args.DeviceID) {
				if args.Labels != nil {
					e.Labels = args.Labels
				}
				if args.LocalName != "" {
					e.LocalName = args.LocalName
				}
				updated = *e
				found = true
				break
			}
		}
		p.mu.Unlock()
		if !found {
			return nil, fmt.Errorf("entity not found")
		}
		return updated, nil

	case "entities/commands/create":
		var args struct {
			CommandID string          `json:"command_id"`
			EntityID  string          `json:"entity_id"`
			DeviceID  string          `json:"device_id"`
			Payload   json.RawMessage `json:"payload"`
		}
		_ = json.Unmarshal(params, &args)
		status := types.CommandStatus{
			CommandID:     args.CommandID,
			PluginID:      p.ID,
			DeviceID:      args.DeviceID,
			EntityID:      args.EntityID,
			State:         types.CommandPending,
			CreatedAt:     time.Now().UTC(),
			LastUpdatedAt: time.Now().UTC(),
		}
		p.mu.Lock()
		p.commands[args.CommandID] = status
		p.mu.Unlock()
		return status, nil

	case "commands/status/get":
		var args struct{ CommandID string `json:"command_id"` }
		_ = json.Unmarshal(params, &args)
		p.mu.RLock()
		st, ok := p.commands[args.CommandID]
		p.mu.RUnlock()
		if ok {
			return st, nil
		}
		return types.CommandStatus{
			CommandID:     args.CommandID,
			PluginID:      p.ID,
			State:         types.CommandSucceeded,
			CreatedAt:     time.Now().UTC(),
			LastUpdatedAt: time.Now().UTC(),
		}, nil

	case "entities/events/ingest":
		var args struct {
			DeviceID  string          `json:"device_id"`
			EntityID  string          `json:"entity_id"`
			Payload   json.RawMessage `json:"payload"`
			CommandID string          `json:"command_id"`
		}
		_ = json.Unmarshal(params, &args)

		p.mu.Lock()
		var updated *types.Entity
		for i := range p.Entities {
			e := &p.Entities[i]
			if e.ID == args.EntityID && e.DeviceID == args.DeviceID {
				e.Data.Reported = args.Payload
				e.Data.Effective = args.Payload
				e.Data.SyncStatus = types.SyncStatusSynced
				e.Data.UpdatedAt = time.Now().UTC()
				if args.CommandID != "" {
					e.Data.LastCommandID = args.CommandID
					if cmd, ok := p.commands[args.CommandID]; ok {
						cmd.State = types.CommandSucceeded
						cmd.LastUpdatedAt = time.Now().UTC()
						p.commands[args.CommandID] = cmd
					}
				}
				cp := *e
				updated = &cp
				break
			}
		}
		p.mu.Unlock()

		if updated == nil {
			return nil, fmt.Errorf("entity not found: %s/%s", args.DeviceID, args.EntityID)
		}

		// Tell the aggregate about the state change.
		if data, err := json.Marshal(entityUpdateWire{Entity: *updated, PluginID: p.ID}); err == nil {
			_ = p.nc.Publish(types.SubjectEntityUpdated, data)
		}

		// Broadcast the event envelope so any subscriber (cross-plugin, gateway) sees it.
		envelope := types.EntityEventEnvelope{
			EventID:       nextID("evt"),
			PluginID:      p.ID,
			DeviceID:      args.DeviceID,
			EntityID:      args.EntityID,
			EntityType:    updated.Domain,
			CorrelationID: args.CommandID,
			Payload:       args.Payload,
			CreatedAt:     time.Now().UTC(),
		}
		if data, err := json.Marshal(envelope); err == nil {
			_ = p.nc.Publish(types.SubjectEntityEvents, data)
		}

		return *updated, nil

	// --- Script CRUD ---

	case "scripts/put":
		var args struct {
			DeviceID string `json:"device_id"`
			EntityID string `json:"entity_id"`
			Source   string `json:"source"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.Lock()
		p.scripts[args.EntityID] = args.Source
		p.mu.Unlock()
		return map[string]string{"entity_id": args.EntityID}, nil

	case "scripts/get":
		var args struct {
			DeviceID string `json:"device_id"`
			EntityID string `json:"entity_id"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.RLock()
		src, ok := p.scripts[args.EntityID]
		p.mu.RUnlock()
		if !ok {
			return nil, rpcNotFoundErr("script not found")
		}
		return map[string]string{"entity_id": args.EntityID, "source": src}, nil

	case "scripts/delete":
		var args struct {
			DeviceID   string `json:"device_id"`
			EntityID   string `json:"entity_id"`
			PurgeState bool   `json:"purge_state"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.Lock()
		delete(p.scripts, args.EntityID)
		if args.PurgeState {
			delete(p.state, args.EntityID)
		}
		p.mu.Unlock()
		return map[string]bool{"ok": true}, nil

	case "scripts/state/get":
		var args struct {
			DeviceID string `json:"device_id"`
			EntityID string `json:"entity_id"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.RLock()
		st, ok := p.state[args.EntityID]
		p.mu.RUnlock()
		if !ok {
			return map[string]any{}, nil
		}
		return st, nil

	case "scripts/state/put":
		var args struct {
			DeviceID string         `json:"device_id"`
			EntityID string         `json:"entity_id"`
			State    map[string]any `json:"state"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.Lock()
		p.state[args.EntityID] = args.State
		p.mu.Unlock()
		return map[string]bool{"ok": true}, nil

	case "scripts/state/delete":
		var args struct {
			DeviceID string `json:"device_id"`
			EntityID string `json:"entity_id"`
		}
		_ = json.Unmarshal(params, &args)
		p.mu.Lock()
		delete(p.state, args.EntityID)
		p.mu.Unlock()
		return map[string]bool{"ok": true}, nil

	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

// StoreScript stores a Lua script for entityID directly in the plugin's in-memory
// map. This is used by tests and godog steps that bypass the HTTP/RPC path.
func (p *SimulatedPlugin) StoreScript(entityID, source string) {
	p.mu.Lock()
	if p.scripts == nil {
		p.scripts = make(map[string]string)
	}
	p.scripts[entityID] = source
	p.mu.Unlock()
}

// RunScript executes the stored Lua script for entityID. entityState is exposed
// as the global variable `entity_state`. Returns ("", nil) if no script is stored.
func (p *SimulatedPlugin) RunScript(entityID, entityState string) (string, error) {
	p.mu.RLock()
	src, ok := p.scripts[entityID]
	p.mu.RUnlock()
	if !ok {
		return "", nil
	}

	L := lua.NewState()
	defer L.Close()

	L.SetGlobal("entity_state", lua.LString(entityState))

	if err := L.DoString(src); err != nil {
		return "", fmt.Errorf("lua: %w", err)
	}

	// Collect the top-of-stack string result, if any.
	if top := L.GetTop(); top > 0 {
		return L.ToStringMeta(L.Get(-1)).String(), nil
	}
	return "", nil
}

// SeedRegistry pushes the plugin's devices and entities into the aggregate
// using a per-plugin registry scoped to p.ID.
func (p *SimulatedPlugin) SeedRegistry() error {
	pluginReg := regsvc.RegistryService(p.ID, regsvc.WithNATS(p.nc), regsvc.WithPersist(regsvc.PersistNever))
	if err := pluginReg.Start(); err != nil {
		return fmt.Errorf("seedRegistry start: %w", err)
	}
	defer pluginReg.Stop()
	for _, d := range p.Devices {
		if err := pluginReg.SaveDevice(d); err != nil {
			return fmt.Errorf("save device %s: %w", d.ID, err)
		}
	}
	for _, e := range p.Entities {
		if err := pluginReg.SaveEntity(e); err != nil {
			return fmt.Errorf("save entity %s: %w", e.ID, err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	return nil
}

// QueryRegistry sends a direct NATS query to the aggregate, simulating a
// plugin asking the system for entities. This bypasses the HTTP gateway,
// proving the NATS-level search works end-to-end.
func (p *SimulatedPlugin) QueryRegistry(q types.SearchQuery) ([]types.Entity, error) {
	reg := regsvc.RegistryService(p.ID+"-query", regsvc.WithPersist(regsvc.PersistNever))
	if err := reg.AttachNATS(p.nc); err != nil {
		return nil, err
	}
	defer reg.Stop()
	return reg.FindEntities(q), nil
}

// seedRegistry is the *testing.T wrapper used by existing unit tests.
func (p *SimulatedPlugin) seedRegistry(t *testing.T) {
	t.Helper()
	if err := p.SeedRegistry(); err != nil {
		t.Fatalf("seedRegistry: %v", err)
	}
}

// GetEntity returns the plugin's current in-memory entity by deviceID and entityID.
func (p *SimulatedPlugin) GetEntity(deviceID, entityID string) (types.Entity, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, e := range p.Entities {
		if e.ID == entityID && e.DeviceID == deviceID {
			return e, true
		}
	}
	return types.Entity{}, false
}

// SubscribeEvents creates a buffered channel and subscribes it to
// SubjectEntityEvents on the shared NATS bus. All plugins and the gateway
// publish envelopes there. The subscription is tied to p's lifecycle (Stop
// unsubscribes it).
func (p *SimulatedPlugin) SubscribeEvents() (<-chan types.EntityEventEnvelope, error) {
	ch := make(chan types.EntityEventEnvelope, 256)
	sub, err := p.nc.Subscribe(types.SubjectEntityEvents, func(m *nats.Msg) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(m.Data, &env) == nil {
			select {
			case ch <- env:
			default:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.subs = append(p.subs, sub)
	p.mu.Unlock()
	return ch, nil
}
