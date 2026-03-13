package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"

	"github.com/slidebolt/gateway/internal/history"
	gatewaymcp "github.com/slidebolt/gateway/internal/mcp"
)

func run() {
	apiHost := getenv(types.EnvAPIHost)
	if apiHost == "" {
		apiHost = "127.0.0.1"
	}
	apiPort, err := requireEnv(types.EnvAPIPort)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	natsURL, err := requireEnv(types.EnvNATSURL)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	rpcSubject := getenv(types.EnvPluginRPCSbj)
	dataDir, err := requireEnv(types.EnvPluginDataDir)
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	gatewayDataDir = dataDir
	commandService = CommandService()
	defer commandService.Close()
	historyPath := filepath.Join(dataDir, "history.db")
	historyService, err = history.Open(historyPath)
	if err != nil {
		slog.Error("failed to open history store", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := historyService.Close(); err != nil {
			slog.Warn("history close error", "error", err)
		}
	}()
	gatewayRT = gatewayRuntimeInfo{NATSURL: natsURL, Version: getenv("APP_VERSION")}

	gatewayID := strings.TrimPrefix(rpcSubject, types.SubjectRPCPrefix)
	if gatewayID == "" {
		gatewayID = "gateway"
	}
	writeRuntimeDescriptor(apiHost, apiPort, natsURL, gatewayID)

	slog.Info("gateway starting", "api_host", apiHost, "api_port", apiPort, "nats_url", natsURL)

	for i := 0; i < 10; i++ {
		nc, err = nats.Connect(natsURL)
		if err == nil {
			break
		}
		slog.Warn("NATS connect failed", "attempt", i+1, "error", err)
		time.Sleep(time.Second)
	}
	if err != nil {
		slog.Error("failed to connect to NATS after 10 attempts", "error", err)
		os.Exit(1)
	}
	slog.Info("NATS connected", "url", natsURL)
	defer nc.Close()

	registryService = regsvc.RegistryService(
		"gateway",
		regsvc.WithNATS(nc),
		regsvc.WithAggregate(),
		regsvc.WithPersist(regsvc.PersistNever),
	)
	if err := registryService.LoadAll(); err != nil {
		slog.Error("failed to load registry", "error", err)
		os.Exit(1)
	}
	if err := registryService.Start(); err != nil {
		slog.Error("failed to start registry", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := registryService.Stop(); err != nil {
			slog.Warn("registryService stop error", "error", err)
		}
	}()

	startNATSDiscoveryBridge()

	js, err = nc.JetStream()
	if err != nil {
		slog.Error("failed to initialize JetStream", "error", err)
		os.Exit(1)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{types.SubjectEntityEvents},
		Storage:  nats.FileStorage,
		MaxMsgs:  5000,
	})
	if err != nil {
		slog.Warn("failed to add EVENTS stream", "error", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "COMMANDS",
		Subjects: []string{types.SubjectCommandStatus},
		Storage:  nats.FileStorage,
		MaxMsgs:  5000,
	})
	if err != nil {
		slog.Warn("failed to add COMMANDS stream", "error", err)
	}

	historyCtx, stopHistory := context.WithCancel(context.Background())
	historyService.Start(historyCtx, nc, js)
	startGatewayDiagnostics()

	dynamicEventService = newDynamicEventService()
	if err := dynamicEventService.Start(nc); err != nil {
		slog.Error("failed to start dynamic event service", "error", err)
		os.Exit(1)
	}
	defer dynamicEventService.Stop()

	subscribeRegistry()
	selfRegister(rpcSubject)
	startDiscoveryProbe(historyCtx)

	r, humaAPI := buildRouter()
	srv := &http.Server{
		Addr:    apiHost + ":" + apiPort,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen error", "error", err)
			os.Exit(1)
		}
	}()

	// MCP bridge over Stdio — tools are generated directly from the OpenAPI spec,
	// so every REST route is automatically available to AI agents.
	mcpBridge := gatewaymcp.New(humaAPI, "http://"+apiHost+":"+apiPort)
	go mcpBridge.Serve()

	waitForShutdownSignal()
	slog.Info("shutting down gateway")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stopHistory()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("gateway shutdown error", "error", err)
	}

	_ = nc.Drain()
	slog.Info("gateway exiting")
}

func subscribeRegistry() {
	_, _ = nc.Subscribe(types.SubjectRegistration, func(m *nats.Msg) {
		var reg types.Registration
		if err := json.Unmarshal(m.Data, &reg); err != nil {
			slog.Warn("failed to unmarshal registration", "error", err)
			return
		}

		for _, schema := range reg.Manifest.Schemas {
			types.RegisterDomain(schema)
		}
		regMu.Lock()
		registry[reg.Manifest.ID] = pluginRecord{
			Registration: reg,
			Valid:        true,
		}
		regMu.Unlock()
		slog.Info("plugin registered", "plugin_id", reg.Manifest.ID)
	})
}

func selfRegister(rpcSubject string) {
	if rpcSubject == "" {
		return
	}
	gatewayID := strings.TrimPrefix(rpcSubject, types.SubjectRPCPrefix)
	manifest := types.Manifest{ID: gatewayID, Name: "SlideBolt Gateway", Version: "1.0.0"}
	reg := types.Registration{Manifest: manifest, RPCSubject: rpcSubject}
	regData, _ := json.Marshal(reg)

	_, _ = nc.Subscribe(rpcSubject, func(m *nats.Msg) {
		var req types.Request
		json.Unmarshal(m.Data, &req)
		var result any
		var rpcErr *types.RPCError
		if req.Method == types.RPCMethodHealthCheck {
			result = map[string]string{"status": "perfect", "service": "gateway"}
		} else {
			rpcErr = &types.RPCError{Code: -32601, Message: "method not found"}
		}
		var resBytes json.RawMessage
		if result != nil {
			resBytes, _ = json.Marshal(result)
		}
		resp := types.Response{JSONRPC: types.JSONRPCVersion, Result: resBytes, Error: rpcErr}
		if req.ID != nil {
			resp.ID = *req.ID
		}
		data, _ := json.Marshal(resp)
		if err := m.Respond(data); err != nil {
			slog.Warn("failed to respond to message", "error", err)
		}
	})

	_, _ = nc.Subscribe(types.SubjectSearchPlugins, func(m *nats.Msg) {
		res := types.SearchPluginsResponse{
			PluginID: gatewayID,
			Matches:  []types.Manifest{manifest},
		}
		data, _ := json.Marshal(res)
		_ = m.Respond(data)
	})

	_ = nc.Publish(types.SubjectRegistration, regData)
	_, _ = nc.Subscribe(types.SubjectDiscoveryProbe, func(m *nats.Msg) {
		_ = nc.Publish(types.SubjectRegistration, regData)
	})
}

func startDiscoveryProbe(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_ = nc.Publish(types.SubjectDiscoveryProbe, []byte("probe"))
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
}

func requireEnv(key string) (string, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return "", fmt.Errorf("required environment variable %s is not set", key)
	}
	return v, nil
}
