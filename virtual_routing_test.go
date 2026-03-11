package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

func setupVirtualRoutingHarness(t *testing.T) {
	t.Helper()

	s, conn := setupTestServer(t)
	nc = conn
	ResetGlobals()
	vstore = loadVirtualStore(t.TempDir())
	masterRegistry = regsvc.NewService()

	h, err := openHistoryStore(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("failed to open history store: %v", err)
	}
	history = h

	t.Cleanup(func() {
		if history != nil {
			_ = history.Close()
			history = nil
		}
		teardownTestServer(t, s, conn)
		ResetGlobals()
		vstore = nil
		masterRegistry = nil
	})
}

func TestParseQueryString_LabelVariants(t *testing.T) {
	q, err := parseQueryString("?Label=Type:Alexa&domain=light")
	if err != nil {
		t.Fatalf("parseQueryString error: %v", err)
	}
	if q.Pattern != "*" {
		t.Fatalf("expected default pattern '*', got %q", q.Pattern)
	}
	if q.Domain != "light" {
		t.Fatalf("expected domain light, got %q", q.Domain)
	}
	if got := q.Labels["Type"]; len(got) != 1 || got[0] != "Alexa" {
		t.Fatalf("expected label Type:Alexa, got %#v", q.Labels)
	}
}

func TestProjectedEntitiesForDevice_UsesEntityQuery(t *testing.T) {
	setupVirtualRoutingHarness(t)

	owner := NewMockPlugin(t, nc, "plugin-alexa")
	owner.Register()
	defer owner.Unregister()
	owner.SetHandler("devices/list", func(params json.RawMessage) (any, error) {
		return []types.Device{{
			ID:          "alexa-device",
			LocalName:   "Alexa",
			EntityQuery: "?label=Type:Alexa",
		}}, nil
	})

	src := types.Entity{
		ID:       "group-basement",
		DeviceID: "source-device",
		Domain:   "light",
		Labels: map[string][]string{
			"Type": {"Alexa"},
		},
	}
	masterRegistry.UpdateEntity("plugin-virtual", src)

	entities, projected, err := projectedEntitiesForDevice("plugin-alexa", "alexa-device")
	if err != nil {
		t.Fatalf("projectedEntitiesForDevice error: %v", err)
	}
	if !projected {
		t.Fatal("expected projected=true")
	}
	if len(entities) != 1 {
		t.Fatalf("expected 1 projected entity, got %d", len(entities))
	}
	if entities[0].ID != "group-basement" {
		t.Fatalf("unexpected projected entity id: %q", entities[0].ID)
	}
	if entities[0].DeviceID != "alexa-device" {
		t.Fatalf("expected remapped device_id alexa-device, got %q", entities[0].DeviceID)
	}

	resolved, hasProjection, err := resolveProjectedEntity("plugin-alexa", "alexa-device", "group-basement")
	if err != nil {
		t.Fatalf("resolveProjectedEntity error: %v", err)
	}
	if !hasProjection {
		t.Fatal("expected hasProjection=true")
	}
	if resolved.PluginID != "plugin-virtual" || resolved.DeviceID != "source-device" {
		t.Fatalf("expected original source coordinates, got plugin=%q device=%q", resolved.PluginID, resolved.DeviceID)
	}
}

func TestSendCommandToGroupVirtual_FanoutAndRollup(t *testing.T) {
	setupVirtualRoutingHarness(t)

	makeCommandPlugin := func(id string) *MockPlugin {
		p := NewMockPlugin(t, nc, id)
		p.Register()
		statusByID := map[string]types.CommandStatus{}
		seq := 0
		p.SetHandler("entities/commands/create", func(params json.RawMessage) (any, error) {
			var in struct {
				DeviceID string `json:"device_id"`
				EntityID string `json:"entity_id"`
			}
			_ = json.Unmarshal(params, &in)
			seq++
			cid := fmt.Sprintf("%s-cmd-%d", id, seq)
			st := types.CommandStatus{
				CommandID:     cid,
				PluginID:      id,
				DeviceID:      in.DeviceID,
				EntityID:      in.EntityID,
				EntityType:    "light",
				State:         types.CommandPending,
				CreatedAt:     time.Now().UTC(),
				LastUpdatedAt: time.Now().UTC(),
			}
			statusByID[cid] = st
			return st, nil
		})
		p.SetHandler("commands/status/get", func(params json.RawMessage) (any, error) {
			var in struct {
				CommandID string `json:"command_id"`
			}
			_ = json.Unmarshal(params, &in)
			st, ok := statusByID[in.CommandID]
			if !ok {
				return nil, fmt.Errorf("unknown command %s", in.CommandID)
			}
			st.State = types.CommandSucceeded
			st.LastUpdatedAt = time.Now().UTC()
			statusByID[in.CommandID] = st
			return st, nil
		})
		t.Cleanup(func() { p.Unregister() })
		return p
	}

	_ = makeCommandPlugin("plugin-a")
	_ = makeCommandPlugin("plugin-b")

	masterRegistry.UpdateEntity("plugin-a", types.Entity{
		ID:       "light-a",
		DeviceID: "device-a",
		Domain:   "light",
		Labels: map[string][]string{
			"room": {"basement"},
		},
	})
	masterRegistry.UpdateEntity("plugin-b", types.Entity{
		ID:       "light-b",
		DeviceID: "device-b",
		Domain:   "light",
		Labels: map[string][]string{
			"room": {"basement"},
		},
	})

	groupKey := entityKey("plugin-owner", "owner-device", "basement")
	vstore.mu.Lock()
	vstore.entities[groupKey] = virtualEntityRecord{
		OwnerPluginID: "plugin-owner",
		OwnerDeviceID: "owner-device",
		SourceQuery:   "?label=room:basement&domain=light",
		SourceDomain:  "light",
		MirrorSource:  false,
		Entity: types.Entity{
			ID:       "basement",
			DeviceID: "owner-device",
			Domain:   "light",
			Actions:  []string{"turn_on", "turn_off"},
		},
	}
	vstore.mu.Unlock()

	status, err := sendCommandToAddress("plugin-owner", "owner-device", "basement", json.RawMessage(`{"type":"turn_on"}`))
	if err != nil {
		t.Fatalf("sendCommandToAddress error: %v", err)
	}
	if status.State != types.CommandPending {
		t.Fatalf("expected pending virtual status, got %s", status.State)
	}

	ok := WaitForCondition(t, 2*time.Second, func() bool {
		vstore.mu.RLock()
		defer vstore.mu.RUnlock()
		rec, exists := vstore.commands[status.CommandID]
		return exists && rec.Status.State == types.CommandSucceeded
	})
	if !ok {
		t.Fatal("virtual command did not reach succeeded state")
	}

	vstore.mu.RLock()
	rec := vstore.commands[status.CommandID]
	vstore.mu.RUnlock()
	if len(rec.Downstream) != 2 {
		t.Fatalf("expected 2 downstream commands, got %d", len(rec.Downstream))
	}
}

func TestSendCommandToAddressRecursive_CircularGuard(t *testing.T) {
	setupVirtualRoutingHarness(t)
	visited := map[string]bool{
		entityKey("p", "d", "e"): true,
	}
	_, err := sendCommandToAddressRecursive("p", "d", "e", json.RawMessage(`{"type":"turn_on"}`), visited)
	if err == nil {
		t.Fatal("expected circular reference error")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected circular error, got %v", err)
	}
}

func TestBroadcastProjectedMirrors_AlexaGetsNotificationOnSourceChange(t *testing.T) {
	setupVirtualRoutingHarness(t)
	hasProjectionDevices.Store(true)

	alexa := NewMockPlugin(t, nc, "plugin-alexa")
	alexa.Register()
	defer alexa.Unregister()
	alexa.SetHandler("devices/list", func(params json.RawMessage) (any, error) {
		return []types.Device{{
			ID:          "alexa-device",
			LocalName:   "Alexa Device",
			EntityQuery: "?label=Type:Alexa",
		}}, nil
	})

	masterRegistry.UpdateEntity("plugin-virtual", types.Entity{
		ID:       "group-basement",
		DeviceID: "virtual-device",
		Domain:   "light",
		Labels: map[string][]string{
			"Type": {"Alexa"},
		},
	})

	ch := make(chan string, 1)
	broker.addClient(ch)
	defer broker.removeClient(ch)

	broadcastProjectedMirrors(types.EntityEventEnvelope{
		PluginID: "plugin-virtual",
		DeviceID: "virtual-device",
		EntityID: "group-basement",
	})

	select {
	case line := <-ch:
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "data: ")
		var msg sseMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("failed to decode sse message: %v", err)
		}
		if msg.Type != "entity" {
			t.Fatalf("expected entity message, got %q", msg.Type)
		}
		if msg.PluginID != "plugin-alexa" || msg.DeviceID != "alexa-device" || msg.EntityID != "group-basement" {
			t.Fatalf("unexpected projected notification target: %#v", msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected projected notification for alexa, got none")
	}
}
