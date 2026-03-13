package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

func TestIntegration_HealthCheck(t *testing.T) {
	h := newAPIHarness(t)

	resp := h.get(t, types.RPCMethodHealthCheck)
	assertStatus(t, resp, http.StatusOK)

	var body map[string]any
	readJSON(t, resp, &body)
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", body)
	}
}

func TestIntegration_PluginRegistration(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{ID: "lights-plugin", nc: h.NC}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	resp := h.get(t, "/api/plugins")
	assertStatus(t, resp, http.StatusOK)

	var body map[string]types.Registration
	readJSON(t, resp, &body)

	if _, ok := body["lights-plugin"]; !ok {
		t.Errorf("expected lights-plugin in plugin list, got %v", body)
	}
}

func TestIntegration_ListDevicesViaRPC(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "zigbee-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "bulb-1", LocalName: "Living Room Bulb"},
			{ID: "bulb-2", LocalName: "Bedroom Bulb"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	resp := h.get(t, "/api/plugins/zigbee-plugin/devices")
	assertStatus(t, resp, http.StatusOK)

	var devices []types.Device
	readJSON(t, resp, &devices)

	if len(devices) != 2 {
		t.Errorf("expected 2 devices, got %d", len(devices))
	}
}

func TestIntegration_ListEntitiesViaRPC(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "zwave-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "switch-1", LocalName: "Kitchen Switch"},
		},
		Entities: []types.Entity{
			{ID: "switch-1-main", DeviceID: "switch-1", Domain: "switch", LocalName: "Kitchen Switch"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	resp := h.get(t, "/api/plugins/zwave-plugin/devices/switch-1/entities")
	assertStatus(t, resp, http.StatusOK)

	var entities []EntityResponse
	readJSON(t, resp, &entities)

	if len(entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(entities))
	}
	if entities[0].ID != "switch-1-main" {
		t.Errorf("expected entity ID switch-1-main, got %s", entities[0].ID)
	}
}

func TestIntegration_SearchEntities(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "hue-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "hue-bridge", LocalName: "Hue Bridge"},
		},
		Entities: []types.Entity{
			{ID: "light-1", DeviceID: "hue-bridge", Domain: "light", LocalName: "Ceiling Light"},
			{ID: "light-2", DeviceID: "hue-bridge", Domain: "light", LocalName: "Desk Lamp"},
			{ID: "scene-1", DeviceID: "hue-bridge", Domain: "scene", LocalName: "Evening"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	resp := h.get(t, "/api/search/entities")
	assertStatus(t, resp, http.StatusOK)
	var all []types.Entity
	readJSON(t, resp, &all)
	if len(all) != 3 {
		t.Errorf("expected 3 entities in total search, got %d", len(all))
	}

	resp = h.get(t, "/api/search/entities?domain=light")
	assertStatus(t, resp, http.StatusOK)
	var lights []types.Entity
	readJSON(t, resp, &lights)
	if len(lights) != 2 {
		t.Errorf("expected 2 lights, got %d", len(lights))
	}

	resp = h.get(t, "/api/search/entities?plugin_id=hue-plugin&domain=scene")
	assertStatus(t, resp, http.StatusOK)
	var scenes []types.Entity
	readJSON(t, resp, &scenes)
	if len(scenes) != 1 {
		t.Errorf("expected 1 scene, got %d", len(scenes))
	}
	if scenes[0].ID != "scene-1" {
		t.Errorf("expected scene-1, got %s", scenes[0].ID)
	}
}

func TestIntegration_SearchDevices(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "matter-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "thermostat-1", LocalName: "Hallway Thermostat"},
			{ID: "thermostat-2", LocalName: "Bedroom Thermostat"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	resp := h.get(t, "/api/search/devices?plugin_id=matter-plugin")
	assertStatus(t, resp, http.StatusOK)

	var devices []types.Device
	readJSON(t, resp, &devices)

	if len(devices) != 2 {
		t.Errorf("expected 2 devices, got %d", len(devices))
	}
}

func TestIntegration_SearchPlugins(t *testing.T) {
	h := newAPIHarness(t)

	for _, id := range []string{"plugin-a", "plugin-b", "plugin-c"} {
		p := &SimulatedPlugin{ID: id, nc: h.NC}
		p.MustStart(t)
		t.Cleanup(p.Stop)
	}

	resp := h.get(t, "/api/search/plugins")
	assertStatus(t, resp, http.StatusOK)

	var manifests []types.Manifest
	readJSON(t, resp, &manifests)

	if len(manifests) < 3 {
		t.Errorf("expected at least 3 plugin manifests, got %d", len(manifests))
	}
}

func TestIntegration_SendCommandAndPollStatus(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID:       "smart-plug",
		nc:       h.NC,
		Devices:  []types.Device{{ID: "plug-1"}},
		Entities: []types.Entity{{ID: "outlet-1", DeviceID: "plug-1", Domain: "switch", Actions: []string{"turn_on", "turn_off"}}},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	resp := h.post(t, "/api/plugins/smart-plug/devices/plug-1/entities/outlet-1/commands",
		map[string]any{"type": "turn_on"})
	assertStatus(t, resp, http.StatusAccepted)

	var status types.CommandStatus
	readJSON(t, resp, &status)

	commandID := status.CommandID
	if commandID == "" {
		t.Fatal("expected a command ID in response")
	}
	if status.State != types.CommandPending {
		t.Errorf("expected state pending, got %s", status.State)
	}

	resp = h.get(t, fmt.Sprintf("/api/plugins/smart-plug/commands/%s", commandID))
	assertStatus(t, resp, http.StatusOK)

	var polled types.CommandStatus
	readJSON(t, resp, &polled)

	if polled.CommandID != commandID {
		t.Errorf("expected command ID %s, got %s", commandID, polled.CommandID)
	}
}

func TestIntegration_MultiplePlugins_Search(t *testing.T) {
	h := newAPIHarness(t)

	hue := &SimulatedPlugin{
		ID: "hue",
		nc: h.NC,
		Entities: []types.Entity{
			{ID: "hue-light-1", DeviceID: "hue-bridge", Domain: "light", LocalName: "Hall Light"},
		},
	}
	zigbee := &SimulatedPlugin{
		ID: "zigbee",
		nc: h.NC,
		Entities: []types.Entity{
			{ID: "zb-switch-1", DeviceID: "zb-hub", Domain: "switch", LocalName: "Porch Switch"},
			{ID: "zb-light-1", DeviceID: "zb-hub", Domain: "light", LocalName: "Garden Light"},
		},
	}

	for _, p := range []*SimulatedPlugin{hue, zigbee} {
		p.MustStart(t)
		t.Cleanup(p.Stop)
		p.seedRegistry(t)
	}

	resp := h.get(t, "/api/search/entities?domain=light")
	assertStatus(t, resp, http.StatusOK)

	var entities []types.Entity
	readJSON(t, resp, &entities)

	if len(entities) != 2 {
		t.Errorf("expected 2 lights across plugins, got %d", len(entities))
	}

	pluginIDs := make(map[string]bool)
	for _, e := range entities {
		pluginIDs[e.PluginID] = true
	}
	if !pluginIDs["hue"] || !pluginIDs["zigbee"] {
		t.Errorf("expected lights from both hue and zigbee, got plugin IDs: %v", pluginIDs)
	}
}

func TestIntegration_UnknownPlugin_Returns4xx(t *testing.T) {
	h := newAPIHarness(t)

	resp := h.get(t, "/api/plugins/nonexistent/devices")
	if resp.StatusCode < 400 {
		t.Errorf("expected 4xx for unknown plugin, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestIntegration_HistoryStats(t *testing.T) {
	h := newAPIHarness(t)

	resp := h.get(t, "/_internal/history/stats")
	assertStatus(t, resp, http.StatusOK)

	var body struct {
		EventCount   int64 `json:"event_count"`
		CommandCount int64 `json:"command_count"`
	}
	readJSON(t, resp, &body)
	if body.EventCount != 0 || body.CommandCount != 0 {
		t.Errorf("expected empty history store, got events=%d commands=%d",
			body.EventCount, body.CommandCount)
	}
}

func TestIntegration_GroupFanoutSkipsUnsupportedLeafActions(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "mixed-lights",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "bridge", LocalName: "Mixed Bridge"},
		},
		Entities: []types.Entity{
			{
				ID:        "rgb-light",
				PluginID:  "mixed-lights",
				DeviceID:  "bridge",
				Domain:    "light",
				LocalName: "RGB Light",
				Actions:   []string{"turn_on", "turn_off", "set_rgb"},
				Labels:    map[string][]string{"Room": {"Basement"}},
			},
			{
				ID:        "edison-light",
				PluginID:  "mixed-lights",
				DeviceID:  "bridge",
				Domain:    "light",
				LocalName: "Edison Light",
				Actions:   []string{"turn_on", "turn_off", "set_brightness"},
				Labels:    map[string][]string{"Room": {"Basement"}},
			},
			{
				ID:        "basement-group",
				PluginID:  "mixed-lights",
				DeviceID:  "bridge",
				Domain:    "light",
				LocalName: "Basement Group",
				CommandQuery: &types.SearchQuery{
					Labels: map[string][]string{"Room": {"Basement"}},
				},
			},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	resp := h.post(t, "/api/plugins/mixed-lights/devices/bridge/entities/basement-group/commands",
		map[string]any{"type": "set_rgb", "rgb": []int{255, 0, 0}})
	assertStatus(t, resp, http.StatusAccepted)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		plugin.mu.RLock()
		var entityIDs []string
		for _, status := range plugin.commands {
			entityIDs = append(entityIDs, status.EntityID)
		}
		plugin.mu.RUnlock()
		slices.Sort(entityIDs)
		if slices.Equal(entityIDs, []string{"rgb-light"}) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	var entityIDs []string
	for _, status := range plugin.commands {
		entityIDs = append(entityIDs, status.EntityID)
	}
	slices.Sort(entityIDs)
	t.Fatalf("fanout dispatched to entity IDs %v, want only rgb-light", entityIDs)
}
