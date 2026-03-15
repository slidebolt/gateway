//go:build bdd

package main

// BDD step definitions for the Device EntityQuery feature.
// A device with EntityQuery surfaces entities from across the registry,
// unioned with the plugin's directly-registered entities.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cucumber/godog"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Context state
// ---------------------------------------------------------------------------

type eqCtxKey struct{}

type eqState struct {
	entityList []types.Entity
	lastStatus int
	stats      struct {
		TotalDevices  int `json:"total_devices"`
		TotalEntities int `json:"total_entities"`
	}
}

func withEQState(ctx context.Context) (context.Context, *eqState) {
	s := &eqState{}
	return context.WithValue(ctx, eqCtxKey{}, s), s
}

func getEQState(ctx context.Context) *eqState {
	if v := ctx.Value(eqCtxKey{}); v != nil {
		return v.(*eqState)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// seedDeviceWithEntityQuery saves a device carrying the given EntityQuery into
// the gateway's local registry so augmentWithEntityQuery can find it.
func seedDeviceWithEntityQuery(deviceID string, q types.SearchQuery) error {
	if registryService == nil {
		return fmt.Errorf("registryService not initialised")
	}
	dev := types.Device{
		ID:          deviceID,
		LocalName:   deviceID,
		EntityQuery: &q,
	}
	return registryService.SaveDevice(dev)
}

// seedEntityForPlugin adds an entity with the given label to a plugin and
// seeds the registry. It creates the plugin if it is not yet registered.
func seedEntityForPlugin(w *World, pluginID, deviceID, entityID, label string) error {
	k, v, ok := parseLabel(label)
	if !ok {
		return fmt.Errorf("invalid label %q", label)
	}
	p := w.plugin(pluginID)
	if len(p.subs) == 0 {
		p.Devices = append(p.Devices, types.Device{ID: deviceID, LocalName: deviceID})
		if err := p.Start(); err != nil {
			return fmt.Errorf("start plugin %s: %w", pluginID, err)
		}
	}
	// Ensure device is present.
	found := false
	for _, d := range p.Devices {
		if d.ID == deviceID {
			found = true
			break
		}
	}
	if !found {
		p.Devices = append(p.Devices, types.Device{ID: deviceID, LocalName: deviceID})
	}
	// Add entity, avoiding duplicates.
	for i, e := range p.Entities {
		if e.ID == entityID {
			if p.Entities[i].Labels == nil {
				p.Entities[i].Labels = make(map[string][]string)
			}
			p.Entities[i].Labels[k] = append(p.Entities[i].Labels[k], v)
			return p.SeedRegistry()
		}
	}
	p.Entities = append(p.Entities, types.Entity{
		ID:        entityID,
		PluginID:  pluginID,
		DeviceID:  deviceID,
		LocalName: entityID,
		Domain:    "light",
		Labels:    map[string][]string{k: {v}},
	})
	return p.SeedRegistry()
}

// seedEntityQueryDevice saves a device with EntityQuery via a registry service
// scoped to pluginID so the device appears in the aggregate too, then also
// saves it directly to the gateway's local registry for LoadDevice lookups.
func seedEntityQueryDevice(h *Harness, pluginID, deviceID string, q types.SearchQuery) error {
	pluginReg := regsvc.RegistryService(pluginID,
		regsvc.WithNATS(h.NC),
		regsvc.WithPersist(regsvc.PersistNever),
	)
	if err := pluginReg.Start(); err != nil {
		return fmt.Errorf("seedEntityQueryDevice start: %w", err)
	}
	defer pluginReg.Stop()
	dev := types.Device{
		ID:          deviceID,
		LocalName:   deviceID,
		EntityQuery: &q,
	}
	if err := pluginReg.SaveDevice(dev); err != nil {
		return fmt.Errorf("seedEntityQueryDevice save: %w", err)
	}
	time.Sleep(40 * time.Millisecond)
	// Also save to the gateway local registry so augmentWithEntityQuery finds it.
	return seedDeviceWithEntityQuery(deviceID, q)
}

// ---------------------------------------------------------------------------
// Steps
// ---------------------------------------------------------------------------

// "plugin "X" is registered"
func stepEQPluginRegistered(ctx context.Context, pluginID string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	if len(p.subs) == 0 {
		if err := p.Start(); err != nil {
			return ctx, fmt.Errorf("start plugin %s: %w", pluginID, err)
		}
	}
	return ctx, nil
}

// "plugin "lights-plugin" has device "basement" with entity "light-1" labeled "Expose:Alexa""
func stepEQPluginHasDeviceEntityLabeled(ctx context.Context, pluginID, deviceID, entityID, label string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, seedEntityForPlugin(w, pluginID, deviceID, entityID, label)
}

// "device "alexa-device" under plugin "alexa-plugin" has EntityQuery label "Expose:Alexa""
func stepEQDeviceHasEntityQueryLabel(ctx context.Context, deviceID, pluginID, label string) (context.Context, error) {
	w := worldFrom(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid label %q", label)
	}
	q := types.SearchQuery{Labels: map[string][]string{k: {v}}}
	return ctx, seedEntityQueryDevice(w.Harness, pluginID, deviceID, q)
}

// "I list entities for plugin "X" device "Y""
func stepEQListEntities(ctx context.Context, pluginID, deviceID string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEQState(ctx)
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities",
		url.PathEscape(pluginID), url.PathEscape(deviceID))
	resp, err := w.Harness.Get(path)
	if err != nil {
		return ctx, err
	}
	body, _ := readBody(resp)
	s.lastStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		return ctx, nil
	}
	var wrapper struct {
		Entities []types.Entity `json:"entities"`
	}
	// Response is a JSON array of EntityResponse objects; decode into Entity slice.
	var raw []json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return ctx, fmt.Errorf("decode entity list: %w\nbody: %s", err, body)
	}
	s.entityList = make([]types.Entity, 0, len(raw))
	_ = wrapper
	for _, r := range raw {
		var e types.Entity
		_ = json.Unmarshal(r, &e)
		s.entityList = append(s.entityList, e)
	}
	return ctx, nil
}

// "the entity list contains "light-1""
func stepEQEntityListContains(ctx context.Context, entityID string) error {
	s := getEQState(ctx)
	for _, e := range s.entityList {
		if e.ID == entityID {
			return nil
		}
	}
	return fmt.Errorf("entity %q not found in list of %d entities", entityID, len(s.entityList))
}

// "the entity list does not contain "light-3""
func stepEQEntityListNotContains(ctx context.Context, entityID string) error {
	s := getEQState(ctx)
	for _, e := range s.entityList {
		if e.ID == entityID {
			return fmt.Errorf("entity %q should not be in list", entityID)
		}
	}
	return nil
}

// "the entity list has no duplicate entity IDs"
func stepEQEntityListNoDuplicates(ctx context.Context) error {
	s := getEQState(ctx)
	seen := make(map[string]int)
	for _, e := range s.entityList {
		seen[e.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			return fmt.Errorf("entity %q appears %d times", id, count)
		}
	}
	return nil
}

// "I get entity "light-1" from plugin "X" device "Y""
func stepEQGetEntity(ctx context.Context, entityID, pluginID, deviceID string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEQState(ctx)
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID))
	resp, err := w.Harness.Get(path)
	if err != nil {
		return ctx, err
	}
	body, _ := readBody(resp)
	s.lastStatus = resp.StatusCode
	if resp.StatusCode == http.StatusOK {
		var e types.Entity
		if err := json.Unmarshal(body, &e); err == nil {
			s.entityList = []types.Entity{e}
		}
	}
	return ctx, nil
}

// "the entity response has ID "light-1""
func stepEQEntityResponseHasID(ctx context.Context, entityID string) error {
	s := getEQState(ctx)
	if s.lastStatus != http.StatusOK {
		return fmt.Errorf("expected 200 got %d", s.lastStatus)
	}
	if len(s.entityList) == 0 {
		return fmt.Errorf("no entity in response")
	}
	if s.entityList[0].ID != entityID {
		return fmt.Errorf("expected entity ID %q, got %q", entityID, s.entityList[0].ID)
	}
	return nil
}

// "the response is 404"
func stepEQResponseIs404(ctx context.Context) error {
	s := getEQState(ctx)
	if s.lastStatus != http.StatusNotFound {
		return fmt.Errorf("expected 404 got %d", s.lastStatus)
	}
	return nil
}

// "I request the system stats"
func stepEQRequestSystemStats(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEQState(ctx)
	var err error
	// Poll to allow aggregate to settle after seeding.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var resp *http.Response
		resp, err = w.Harness.Get("/api/system/stats")
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		body, _ := readBody(resp)
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("expected 200 got %d\nbody: %s", resp.StatusCode, body)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		err = json.Unmarshal(body, &s.stats)
		break
	}
	return ctx, err
}

// "the system has at least 2 devices"
func stepEQSystemHasAtLeastDevices(ctx context.Context, n int) error {
	s := getEQState(ctx)
	if s.stats.TotalDevices < n {
		return fmt.Errorf("expected at least %d devices, got %d", n, s.stats.TotalDevices)
	}
	return nil
}

// "the system has at least 3 entities"
func stepEQSystemHasAtLeastEntities(ctx context.Context, n int) error {
	s := getEQState(ctx)
	if s.stats.TotalEntities < n {
		return fmt.Errorf("expected at least %d entities, got %d", n, s.stats.TotalEntities)
	}
	return nil
}

// readBody reads and returns a response body.
func readBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	var buf []byte
	tmp := make([]byte, 512)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf, nil
}

// ---------------------------------------------------------------------------
// Step registration
// ---------------------------------------------------------------------------

func registerEntityQuerySteps(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ctx, _ = withEQState(ctx)
		return ctx, nil
	})

	sc.Step(`^plugin "([^"]*)" is registered$`, stepEQPluginRegistered)
	sc.Step(`^plugin "([^"]*)" has device "([^"]*)" with entity "([^"]*)" labeled "([^"]*)"$`,
		stepEQPluginHasDeviceEntityLabeled)
	sc.Step(`^device "([^"]*)" under plugin "([^"]*)" has EntityQuery label "([^"]*)"$`,
		stepEQDeviceHasEntityQueryLabel)
	sc.Step(`^I list entities for plugin "([^"]*)" device "([^"]*)"$`,
		stepEQListEntities)
	sc.Step(`^the entity list contains "([^"]*)"$`, stepEQEntityListContains)
	sc.Step(`^the entity list does not contain "([^"]*)"$`, stepEQEntityListNotContains)
	sc.Step(`^the entity list has no duplicate entity IDs$`, stepEQEntityListNoDuplicates)
	sc.Step(`^I get entity "([^"]*)" from plugin "([^"]*)" device "([^"]*)"$`,
		stepEQGetEntity)
	sc.Step(`^the entity response has ID "([^"]*)"$`, stepEQEntityResponseHasID)
	sc.Step(`^the response is 404$`, stepEQResponseIs404)
	sc.Step(`^I request the system stats$`, stepEQRequestSystemStats)
	sc.Step(`^the system has at least (\d+) devices$`, stepEQSystemHasAtLeastDevices)
	sc.Step(`^the system has at least (\d+) entities$`, stepEQSystemHasAtLeastEntities)
}
