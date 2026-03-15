//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-entities/light"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

type runnerBackedPlugin struct {
	id  string
	ctx runner.PluginContext
}

type sessionStartedEvent struct {
	DeviceID  string
	EntityID  string
	Count     float64
	SessionID string
}

func (p *runnerBackedPlugin) Initialize(ctx runner.PluginContext) (types.Manifest, error) {
	p.ctx = ctx
	return types.Manifest{
		ID:      p.id,
		Name:    "Runner Script Test Plugin",
		Version: "1.0.0",
		Schemas: types.CoreDomains(),
	}, nil
}

func (p *runnerBackedPlugin) Start(ctx context.Context) error { return nil }

func (p *runnerBackedPlugin) Stop() error { return nil }

func (p *runnerBackedPlugin) OnReset() error { return nil }

func (p *runnerBackedPlugin) OnCommand(req types.Command, entity types.Entity) error {
	var payload map[string]any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return err
	}
	action, _ := payload["type"].(string)

	// Canonical state: no "type" key; "power" field is required by the runner.
	state := map[string]any{"power": true}
	switch action {
	case light.ActionTurnOn:
		state["power"] = true
	case light.ActionTurnOff:
		state["power"] = false
	case light.ActionSetBrightness:
		state["brightness"] = payload["brightness"]
	case light.ActionSetTemperature:
		state["temperature"] = payload["temperature"]
	case light.ActionSetRGB:
		state["rgb"] = payload["rgb"]
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return p.ctx.Events.PublishEvent(types.InboundEvent{
		DeviceID:      entity.DeviceID,
		EntityID:      entity.ID,
		CorrelationID: req.ID,
		Payload:       raw,
	})
}

func TestIntegration_ScriptCRUD_ViaRunnerRPC(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin"
	startRunnerBackedPlugin(t, h, pluginID)

	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", nil)

	const source = "function OnInit(Ctx)\n  Ctx.ready = true\nend"

	resp, err := h.Put("/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script", map[string]any{
		"source": source,
	})
	if err != nil {
		t.Fatalf("PUT /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	var script map[string]any
	resp = h.get(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script")
	assertStatus(t, resp, http.StatusOK)
	readJSON(t, resp, &script)
	if got := script["source"]; got != source {
		t.Fatalf("script source = %q, want %q", got, source)
	}

	resp, err = h.Put("/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script/state", map[string]any{
		"state": map[string]any{"armed": true, "count": 3},
	})
	if err != nil {
		t.Fatalf("PUT /script/state: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	var state map[string]any
	resp = h.get(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script/state")
	assertStatus(t, resp, http.StatusOK)
	readJSON(t, resp, &state)
	if got := state["armed"]; got != true {
		t.Fatalf("state armed = %#v, want true", got)
	}
	if got := state["count"]; got != float64(3) {
		t.Fatalf("state count = %#v, want 3", got)
	}

	resp, err = h.Delete("/api/plugins/" + pluginID + "/devices/dev-1/entities/light-1/script")
	if err != nil {
		t.Fatalf("DELETE /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	resp = h.get(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script")
	assertStatus(t, resp, http.StatusNotFound)
}

func TestIntegration_ScriptExecution_OnCommand_ViaRunnerRPC(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin-exec"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", map[string][]string{"brand": {"runner"}})

	const source = `
This.OnCommand("turn_off", function(cmd)
  This.SendCommand("turn_on", {})
end)
`
	resp, err := h.Put("/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script", map[string]any{
		"source": source,
	})
	if err != nil {
		t.Fatalf("PUT /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_off"})
	assertStatus(t, resp, http.StatusAccepted)

	waitForReportedPower(t, h, pluginID, "dev-1", "light-1", true, 5*time.Second)
}

func TestIntegration_ScriptCanStartAndStopChildScript(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin-child"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", map[string][]string{"brand": {"runner"}})

	ensureScriptRuntime()
	if scriptRuntime == nil {
		t.Fatal("scriptRuntime not initialized")
	}
	scriptRuntime.RegisterNamedSource("Christmas", `
This.SendEvent({type = "child_started"})
local timer = TimerService.Scripting.Every(0.1, function()
  This.SendEvent({type = "child_tick"})
end)
This.OnCommand("stop", function(cmd)
  TimerService.Scripting.Cancel(timer)
  This.SendEvent({type = "child_stopped"})
end)
`)

	const hostSource = `
local pid = nil

This.OnCommand("turn_on", function(cmd)
  pid = This.RunScript("Christmas")
  This.SendEvent({type = "host_started_child"})
end)

This.OnCommand("turn_off", function(cmd)
  if pid ~= nil then
    This.StopScript(pid)
    pid = nil
    This.SendEvent({type = "host_stopped_child"})
  end
end)
`
	resp, err := h.Put("/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script", map[string]any{
		"source": hostSource,
	})
	if err != nil {
		t.Fatalf("PUT /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	nc, err := nats.Connect(h.NC.ConnectedUrl())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	events := make(chan string, 128)
	sub, err := nc.Subscribe(types.SubjectEntityEvents, func(msg *nats.Msg) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(msg.Data, &env) != nil {
			return
		}
		if env.PluginID != pluginID || env.DeviceID != "dev-1" || env.EntityID != "light-1" {
			return
		}
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil || payload.Type == "" {
			return
		}
		events <- payload.Type
	})
	if err != nil {
		t.Fatalf("subscribe entity events: %v", err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush nats subscription: %v", err)
	}

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)
	waitForEventTypes(t, events, 5*time.Second, "host_started_child", "child_started")
	waitForEventType(t, events, "child_tick", 5*time.Second)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_off"})
	assertStatus(t, resp, http.StatusAccepted)
	waitForEventType(t, events, "host_stopped_child", 5*time.Second)
	ensureNoEventType(t, events, "child_tick", 400*time.Millisecond)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)
	waitForEventTypes(t, events, 5*time.Second, "host_started_child", "child_started")
	waitForEventType(t, events, "child_tick", 5*time.Second)
}

func TestIntegration_ScriptModuleEntryRunsSelectedDefinition(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin-module"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", map[string][]string{"brand": {"runner"}})

	ensureScriptRuntime()
	if scriptRuntime == nil {
		t.Fatal("scriptRuntime not initialized")
	}
	scriptRuntime.RegisterNamedSource("HolidayEffects", `
DefineScript("Christmas", function()
  This.SendEvent({type = "christmas_started"})
end)

DefineScript("Fall", function()
  This.SendEvent({type = "fall_started"})
end)
`)

	const hostSource = `
This.OnCommand("turn_on", function(cmd)
  This.RunScript("HolidayEffects.Christmas")
  This.SendEvent({type = "host_started_module_child"})
end)
`
	resp, err := h.Put("/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/script", map[string]any{
		"source": hostSource,
	})
	if err != nil {
		t.Fatalf("PUT /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	nc, err := nats.Connect(h.NC.ConnectedUrl())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	events := make(chan string, 128)
	sub, err := nc.Subscribe(types.SubjectEntityEvents, func(msg *nats.Msg) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(msg.Data, &env) != nil {
			return
		}
		if env.PluginID != pluginID || env.DeviceID != "dev-1" || env.EntityID != "light-1" {
			return
		}
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil || payload.Type == "" {
			return
		}
		events <- payload.Type
	})
	if err != nil {
		t.Fatalf("subscribe entity events: %v", err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush nats subscription: %v", err)
	}

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)
	waitForEventTypes(t, events, 5*time.Second, "host_started_module_child", "christmas_started")
	ensureNoEventType(t, events, "fall_started", 400*time.Millisecond)
}

func TestIntegration_SameChildScriptUsesIsolatedSessionDataPerVM(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin-session"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", nil)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-2", "light-2", nil)

	ensureScriptRuntime()
	if scriptRuntime == nil {
		t.Fatal("scriptRuntime not initialized")
	}
	scriptRuntime.RegisterNamedSource("Counter", `
local state = This.LoadSession()
local count = 1
if state ~= nil and state.count ~= nil then
  count = state.count + 1
end
This.SaveSession({count = count})
This.SendEvent({type = "session_started", count = count, session_id = This.SessionID()})
`)

	const hostSource = `
local pid = nil

This.OnCommand("turn_on", function(cmd)
  pid = This.RunScript("Counter")
end)

This.OnCommand("turn_off", function(cmd)
  if pid ~= nil then
    This.StopScript(pid)
    pid = nil
  end
end)
`
	for _, tc := range []struct {
		deviceID string
		entityID string
	}{
		{deviceID: "dev-1", entityID: "light-1"},
		{deviceID: "dev-2", entityID: "light-2"},
	} {
		resp, err := h.Put("/api/plugins/"+pluginID+"/devices/"+tc.deviceID+"/entities/"+tc.entityID+"/script", map[string]any{
			"source": hostSource,
		})
		if err != nil {
			t.Fatalf("PUT /script %s/%s: %v", tc.deviceID, tc.entityID, err)
		}
		assertStatus(t, resp, http.StatusOK)
	}

	nc, err := nats.Connect(h.NC.ConnectedUrl())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	started := make(chan sessionStartedEvent, 16)
	sub, err := nc.Subscribe(types.SubjectEntityEvents, func(msg *nats.Msg) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(msg.Data, &env) != nil {
			return
		}
		if env.PluginID != pluginID {
			return
		}
		var payload struct {
			Type      string  `json:"type"`
			Count     float64 `json:"count"`
			SessionID string  `json:"session_id"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil || payload.Type != "session_started" {
			return
		}
		started <- sessionStartedEvent{
			DeviceID:  env.DeviceID,
			EntityID:  env.EntityID,
			Count:     payload.Count,
			SessionID: payload.SessionID,
		}
	})
	if err != nil {
		t.Fatalf("subscribe entity events: %v", err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush nats subscription: %v", err)
	}

	resp := h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)
	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-2/entities/light-2/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)

	got1 := waitForStartedEvent(t, started, "dev-1", "light-1", 5*time.Second)
	got2 := waitForStartedEvent(t, started, "dev-2", "light-2", 5*time.Second)
	if got1.Count != 1 || got2.Count != 1 {
		t.Fatalf("expected isolated session count=1 for both, got dev1=%v dev2=%v", got1.Count, got2.Count)
	}
	if got1.SessionID == "" || got2.SessionID == "" || got1.SessionID == got2.SessionID {
		t.Fatalf("expected distinct non-empty session ids, got dev1=%q dev2=%q", got1.SessionID, got2.SessionID)
	}
	if _, ok := registryService.LoadSession(got1.SessionID); !ok {
		t.Fatalf("session %q not present in registry after start", got1.SessionID)
	}
	if _, ok := registryService.LoadSession(got2.SessionID); !ok {
		t.Fatalf("session %q not present in registry after start", got2.SessionID)
	}

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-1/entities/light-1/commands", map[string]any{"type": "turn_off"})
	assertStatus(t, resp, http.StatusAccepted)
	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/dev-2/entities/light-2/commands", map[string]any{"type": "turn_off"})
	assertStatus(t, resp, http.StatusAccepted)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, ok1 := registryService.LoadSession(got1.SessionID)
		_, ok2 := registryService.LoadSession(got2.SessionID)
		if !ok1 && !ok2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("sessions were not cleaned up after stop: dev1=%q dev2=%q", got1.SessionID, got2.SessionID)
}

func TestIntegration_BuiltinChristmasScriptRunsViaChildVM(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-script-plugin-builtin"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "groups", "strip-1", nil)

	// Seed a virtual strip-like entity with strip_members metadata so the built-in
	// Christmas script can derive its length.
	if err := registryService.SaveEntity(types.Entity{
		ID:       "strip-1",
		PluginID: pluginID,
		DeviceID: "groups",
		Domain:   "light_strip",
		Actions: []string{
			"turn_on",
			"set_brightness",
			"set_segment",
		},
		Meta: map[string]json.RawMessage{
			"strip_members": json.RawMessage(`[
				{"index":0,"plugin_id":"plugin-a","device_id":"dev-a","entity_id":"entity-a"},
				{"index":1,"plugin_id":"plugin-a","device_id":"dev-b","entity_id":"entity-b"}
			]`),
		},
	}); err != nil {
		t.Fatalf("SaveEntity strip-1: %v", err)
	}

	const hostSource = `
local pid = nil

This.OnCommand("turn_on", function(cmd)
  pid = This.RunScript("Christmas")
  This.SendEvent({type = "builtin_started"})
end)

This.OnCommand("turn_off", function(cmd)
  if pid ~= nil then
    This.StopScript(pid)
    pid = nil
    This.SendEvent({type = "builtin_stopped"})
  end
end)
`
	resp, err := h.Put("/api/plugins/"+pluginID+"/devices/groups/entities/strip-1/script", map[string]any{
		"source": hostSource,
	})
	if err != nil {
		t.Fatalf("PUT /script: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)

	nc, err := nats.Connect(h.NC.ConnectedUrl())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	events := make(chan string, 64)
	sub, err := nc.Subscribe(types.SubjectEntityEvents, func(msg *nats.Msg) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(msg.Data, &env) != nil {
			return
		}
		if env.PluginID != pluginID || env.DeviceID != "groups" || env.EntityID != "strip-1" {
			return
		}
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(env.Payload, &payload) != nil || payload.Type == "" {
			return
		}
		events <- payload.Type
	})
	if err != nil {
		t.Fatalf("subscribe entity events: %v", err)
	}
	defer sub.Unsubscribe()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush nats subscription: %v", err)
	}

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/groups/entities/strip-1/commands", map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)
	waitForEventTypes(t, events, 5*time.Second, "builtin_started", "christmas_started")
}

func TestIntegration_SearchByLabel_ViaRunnerRPC(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-search-plugin"
	const searchLabel = "runner-search-plugin"
	startRunnerBackedPlugin(t, h, pluginID)
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light-1", map[string][]string{"brand": {searchLabel}, "room": {"lab"}})
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-2", "light-2", map[string][]string{"brand": {searchLabel}, "room": {"hall"}})

	resp := h.get(t, "/api/search/devices?label=brand:"+searchLabel)
	assertStatus(t, resp, http.StatusOK)
	var devices []types.Device
	readJSON(t, resp, &devices)
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}

	resp = h.get(t, "/api/search/entities?label=room:lab")
	assertStatus(t, resp, http.StatusOK)
	var entities []types.Entity
	readJSON(t, resp, &entities)
	if len(entities) != 1 || entities[0].ID != "light-1" {
		t.Fatalf("expected only light-1, got %+v", entities)
	}
}

func TestIntegration_GroupFanout_ViaRunnerRPC(t *testing.T) {
	h := newAPIHarness(t)
	const pluginID = "runner-group-plugin"
	startRunnerBackedPlugin(t, h, pluginID)
	const fanoutLabel = "runner-group-plugin"
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-1", "light", map[string][]string{"brand": {fanoutLabel}})
	createRunnerDeviceAndEntity(t, h, pluginID, "dev-2", "light", map[string][]string{"brand": {fanoutLabel}})

	resp := h.post(t, "/api/plugins/"+pluginID+"/devices", types.Device{
		PluginID:  pluginID,
		ID:        "groups",
		SourceID:  "groups",
		LocalName: "groups",
	})
	assertStatus(t, resp, http.StatusOK)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/groups/entities", types.Entity{
		ID:        "runner-all-lights",
		DeviceID:  "groups",
		Domain:    "light",
		PluginID:  pluginID,
		LocalName: "runner-all-lights",
		CommandQuery: &types.SearchQuery{
			Labels: map[string][]string{"brand": {fanoutLabel}},
		},
	})
	assertStatus(t, resp, http.StatusOK)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/groups/entities/runner-all-lights/commands", map[string]any{
		"type":       "set_brightness",
		"brightness": 5,
	})
	assertStatus(t, resp, http.StatusAccepted)

	waitForReportedBrightness(t, h, pluginID, "dev-1", "light", 5, 5*time.Second)
	waitForReportedBrightness(t, h, pluginID, "dev-2", "light", 5, 5*time.Second)
}

func startRunnerBackedPlugin(t *testing.T, h *apiHarness, pluginID string) {
	t.Helper()

	t.Setenv(types.EnvNATSURL, h.NC.ConnectedUrl())
	t.Setenv(types.EnvPluginRPCSbj, types.SubjectRPCPrefix+pluginID)
	t.Setenv(types.EnvPluginDataDir, filepath.Join(t.TempDir(), pluginID))

	r, err := runner.NewRunner(&runnerBackedPlugin{id: pluginID})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.RunContext(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("runner shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("runner did not stop within timeout")
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("runner exited before registration: %v", err)
		default:
		}

		resp := h.get(t, "/api/plugins")
		if resp.StatusCode == http.StatusOK {
			var plugins map[string]types.Registration
			readJSON(t, resp, &plugins)
			if _, ok := plugins[pluginID]; ok {
				return
			}
		} else {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("plugin %q did not register with gateway", pluginID)
}

func waitForEventType(t *testing.T, events <-chan string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-events:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", want)
		}
	}
}

func waitForEventTypes(t *testing.T, events <-chan string, timeout time.Duration, wants ...string) {
	t.Helper()
	remaining := make(map[string]struct{}, len(wants))
	for _, want := range wants {
		remaining[want] = struct{}{}
	}
	deadline := time.After(timeout)
	for len(remaining) > 0 {
		select {
		case got := <-events:
			delete(remaining, got)
		case <-deadline:
			t.Fatalf("timed out waiting for events %v", wants)
		}
	}
}

func ensureNoEventType(t *testing.T, events <-chan string, forbidden string, duration time.Duration) {
	t.Helper()
	deadline := time.After(duration)
	for {
		select {
		case got := <-events:
			if got == forbidden {
				t.Fatalf("unexpected event %q during quiet window", forbidden)
			}
		case <-deadline:
			return
		}
	}
}

func waitForStartedEvent(t *testing.T, events <-chan sessionStartedEvent, wantDeviceID, wantEntityID string, timeout time.Duration) sessionStartedEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-events:
			if got.DeviceID == wantDeviceID && got.EntityID == wantEntityID {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for session_started on %s/%s", wantDeviceID, wantEntityID)
		}
	}
}

func createRunnerDeviceAndEntity(t *testing.T, h *apiHarness, pluginID, deviceID, entityID string, labels map[string][]string) {
	t.Helper()

	resp := h.post(t, "/api/plugins/"+pluginID+"/devices", types.Device{
		PluginID:  pluginID,
		ID:        deviceID,
		SourceID:  deviceID,
		LocalName: deviceID,
		Labels:    labels,
	})
	assertStatus(t, resp, http.StatusOK)

	resp = h.post(t, "/api/plugins/"+pluginID+"/devices/"+deviceID+"/entities", types.Entity{
		PluginID:  pluginID,
		DeviceID:  deviceID,
		ID:        entityID,
		Domain:    "light",
		LocalName: entityID,
		Actions: []string{
			light.ActionTurnOn,
			light.ActionTurnOff,
			light.ActionSetBrightness,
			light.ActionSetTemperature,
			light.ActionSetRGB,
		},
		Labels: labels,
	})
	assertStatus(t, resp, http.StatusOK)
}

func waitForReportedPower(t *testing.T, h *apiHarness, pluginID, deviceID, entityID string, want bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ent := getRunnerEntity(t, h, pluginID, deviceID, entityID)
		var st struct {
			Type  string `json:"type"`
			Power bool   `json:"power"`
		}
		if len(ent.Data.Reported) > 0 && json.Unmarshal(ent.Data.Reported, &st) == nil && st.Power == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("entity %s/%s/%s did not reach power=%v", pluginID, deviceID, entityID, want)
}

func waitForReportedBrightness(t *testing.T, h *apiHarness, pluginID, deviceID, entityID string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ent := getRunnerEntity(t, h, pluginID, deviceID, entityID)
		var st struct {
			Type       string `json:"type"`
			Brightness int    `json:"brightness"`
		}
		if len(ent.Data.Reported) > 0 && json.Unmarshal(ent.Data.Reported, &st) == nil && st.Brightness == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("entity %s/%s/%s did not reach brightness=%d", pluginID, deviceID, entityID, want)
}

func getRunnerEntity(t *testing.T, h *apiHarness, pluginID, deviceID, entityID string) types.Entity {
	t.Helper()
	resp := h.get(t, "/api/plugins/"+pluginID+"/devices/"+deviceID+"/entities")
	assertStatus(t, resp, http.StatusOK)
	var entities []types.Entity
	readJSON(t, resp, &entities)
	for _, ent := range entities {
		if ent.ID == entityID {
			return ent
		}
	}
	t.Fatalf("entity %s/%s/%s not found", pluginID, deviceID, entityID)
	return types.Entity{}
}
