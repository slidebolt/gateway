package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/slidebolt/sdk-entities/light"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

type runnerBackedPlugin struct {
	id  string
	ctx runner.PluginContext
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
	event := map[string]any{"type": action}
	switch action {
	case light.ActionTurnOn:
		event["power"] = true
	case light.ActionTurnOff:
		event["power"] = false
	case light.ActionSetBrightness:
		event["brightness"] = payload["brightness"]
	case light.ActionSetTemperature:
		event["temperature"] = payload["temperature"]
	case light.ActionSetRGB:
		event["rgb"] = payload["rgb"]
	}
	raw, err := json.Marshal(event)
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
