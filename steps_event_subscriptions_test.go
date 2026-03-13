package main

// BDD step definitions for dynamic event subscriptions.
// Tests cover EventFilter{EntityQuery, Action} matching at the gateway level.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Context state
// ---------------------------------------------------------------------------

type dynSubCtxKey struct{}

type dynSubState struct {
	subID string
}

func withDynSubState(ctx context.Context) (context.Context, *dynSubState) {
	s := &dynSubState{}
	return context.WithValue(ctx, dynSubCtxKey{}, s), s
}

func getDynSubState(ctx context.Context) *dynSubState {
	if v := ctx.Value(dynSubCtxKey{}); v != nil {
		return v.(*dynSubState)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// ensurePlugin starts plugin if not already running and seeds registry.
func ensureDynPlugin(w *World, pluginID string) *SimulatedPlugin {
	p := w.plugin(pluginID)
	if len(p.subs) == 0 {
		if err := p.Start(); err != nil {
			panic(fmt.Sprintf("start plugin %s: %v", pluginID, err))
		}
	}
	return p
}

// addEntityToPlugin adds entity with given label+domain to the plugin and re-seeds.
func addEntityToPlugin(w *World, pluginID, entityID, label, domain string) error {
	k, v, ok := parseLabel(label)
	if !ok {
		return fmt.Errorf("invalid label %q", label)
	}
	p := ensureDynPlugin(w, pluginID)
	// Use first device or create default.
	deviceID := "default-device"
	if len(p.Devices) > 0 {
		deviceID = p.Devices[0].ID
	} else {
		p.Devices = append(p.Devices, types.Device{ID: deviceID, LocalName: deviceID})
	}
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
		Domain:    domain,
		Labels:    map[string][]string{k: {v}},
	})
	return p.SeedRegistry()
}

// ingestEvent posts an event for entity entityID on the first plugin that owns it.
func ingestEvent(w *World, entityID, action string) error {
	// Find which plugin+device owns this entity.
	pluginID, deviceID := "", ""
	for _, p := range w.Plugins {
		for _, e := range p.Entities {
			if e.ID == entityID {
				pluginID = p.ID
				deviceID = e.DeviceID
				break
			}
		}
		if pluginID != "" {
			break
		}
	}
	if pluginID == "" {
		return fmt.Errorf("entity %q not found in any plugin", entityID)
	}

	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/events",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID))
	if err := w.do(w.Harness.Post(path, map[string]any{"type": action})); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond) // allow NATS delivery to the subscription handler
	return nil
}

// pollSubEvents calls GET /api/events/subscriptions/{id}/events and decodes the response.
func pollSubEvents(w *World, subID string) ([]types.EntityEventEnvelope, error) {
	path := fmt.Sprintf("/api/events/subscriptions/%s/events", url.PathEscape(subID))
	resp, err := w.Harness.Get(path)
	if err != nil {
		return nil, err
	}
	var body []byte
	if resp.Body != nil {
		defer resp.Body.Close()
		tmp := make([]byte, 4096)
		for {
			n, e := resp.Body.Read(tmp)
			body = append(body, tmp[:n]...)
			if e != nil {
				break
			}
		}
	}
	if resp.StatusCode == 404 {
		return nil, nil // subscription gone
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Events []types.EntityEventEnvelope `json:"events"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}
	return out.Events, nil
}

// ---------------------------------------------------------------------------
// Steps
// ---------------------------------------------------------------------------

func stepDynPluginRegistered(ctx context.Context, pluginID string) (context.Context, error) {
	w := worldFrom(ctx)
	ensureDynPlugin(w, pluginID)
	return ctx, nil
}

func stepDynPluginHasEntityLabeled(ctx context.Context, pluginID, entityID, label, domain string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, addEntityToPlugin(w, pluginID, entityID, label, domain)
}

func stepCreateSubWithLabelQuery(ctx context.Context, label string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid label %q", label)
	}
	filter := EventFilter{
		EntityQuery: types.SearchQuery{
			Labels: map[string][]string{k: {v}},
		},
	}
	if err := w.do(w.Harness.Post("/api/events/subscriptions", filter)); err != nil {
		return ctx, err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := w.decodeLastBody(&out); err != nil {
		return ctx, err
	}
	s.subID = out.ID
	return ctx, nil
}

func stepCreateSubWithLabelAndAction(ctx context.Context, label, action string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid label %q", label)
	}
	filter := EventFilter{
		EntityQuery: types.SearchQuery{
			Labels: map[string][]string{k: {v}},
		},
		Action: action,
	}
	if err := w.do(w.Harness.Post("/api/events/subscriptions", filter)); err != nil {
		return ctx, err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := w.decodeLastBody(&out); err != nil {
		return ctx, err
	}
	s.subID = out.ID
	return ctx, nil
}

func stepCreateSubWithDomain(ctx context.Context, domain string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	filter := EventFilter{
		EntityQuery: types.SearchQuery{Domain: domain},
	}
	if err := w.do(w.Harness.Post("/api/events/subscriptions", filter)); err != nil {
		return ctx, err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := w.decodeLastBody(&out); err != nil {
		return ctx, err
	}
	s.subID = out.ID
	return ctx, nil
}

func stepIngestEventForEntity(ctx context.Context, action, entityID string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, ingestEvent(w, entityID, action)
}

func stepSubHasNBufferedEvents(ctx context.Context, n int) error {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	// Poll with retry to handle async delivery.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, err := pollSubEvents(w, s.subID)
		if err != nil {
			return err
		}
		if len(events) == n {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Final check with drain.
	events, err := pollSubEvents(w, s.subID)
	if err != nil {
		return err
	}
	if len(events) != n {
		return fmt.Errorf("expected %d buffered events, got %d", n, len(events))
	}
	return nil
}

func stepDeleteSub(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	path := fmt.Sprintf("/api/events/subscriptions/%s", url.PathEscape(s.subID))
	resp, err := w.Harness.Delete(path)
	if err != nil {
		return ctx, err
	}
	_ = resp.Body.Close()
	return ctx, nil
}

func stepSubIsGone(ctx context.Context) error {
	w := worldFrom(ctx)
	s := getDynSubState(ctx)
	events, err := pollSubEvents(w, s.subID)
	if err != nil {
		return err
	}
	if events != nil {
		return fmt.Errorf("expected subscription to be gone, but got %d events", len(events))
	}
	return nil
}

func stepPluginRegistersNewEntityWithLabel(ctx context.Context, pluginID, entityID, label string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, addEntityToPlugin(w, pluginID, entityID, label, "light")
}

// ---------------------------------------------------------------------------
// Step registration
// ---------------------------------------------------------------------------

func registerEventSubscriptionSteps(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ctx, _ = withDynSubState(ctx)
		return ctx, nil
	})

	sc.Step(`^plugin "([^"]*)" is registered$`, stepDynPluginRegistered)
	sc.Step(`^plugin "([^"]*)" has entity "([^"]*)" with label "([^"]*)" and domain "([^"]*)"$`,
		stepDynPluginHasEntityLabeled)
	sc.Step(`^I create a subscription with label query "([^"]*)"$`,
		stepCreateSubWithLabelQuery)
	sc.Step(`^I create a subscription with label query "([^"]*)" and action "([^"]*)"$`,
		stepCreateSubWithLabelAndAction)
	sc.Step(`^I create a subscription with domain "([^"]*)"$`,
		stepCreateSubWithDomain)
	sc.Step(`^event "([^"]*)" is ingested for entity "([^"]*)"$`,
		stepIngestEventForEntity)
	sc.Step(`^the subscription has (\d+) buffered events$`,
		stepSubHasNBufferedEvents)
	sc.Step(`^I delete the subscription$`, stepDeleteSub)
	sc.Step(`^the subscription is gone$`, stepSubIsGone)
	sc.Step(`^plugin "([^"]*)" registers a new entity "([^"]*)" with label "([^"]*)"$`,
		stepPluginRegistersNewEntityWithLabel)
}
