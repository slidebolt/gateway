//go:build bdd

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

func registerEventSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a 10-plugin events environment is running$`, stepEventsEnvSetup)
	sc.Step(`^plugin "([^"]*)" ingests event (.*) for entity "([^"]*)"$`, stepIngestEvent)
	sc.Step(`^plugin "([^"]*)" ingests event (.*) for entity "([^"]*)" correlated with the last command$`, stepIngestEventCorrelated)
	sc.Step(`^entity "([^"]*)" on plugin "([^"]*)" has sync_status "([^"]*)"$`, stepEntitySyncStatus)
	sc.Step(`^entity "([^"]*)" on plugin "([^"]*)" has state field "([^"]*)" equal to "([^"]*)"$`, stepEntityStateField)
	sc.Step(`^entity "([^"]*)" on plugin "([^"]*)" last command ID matches the sent command$`, stepEntityLastCommandID)
	sc.Step(`^the HTTP GET entity for plugin "([^"]*)" device "([^"]*)" entity "([^"]*)" has sync_status "([^"]*)"$`, stepHTTPEntitySyncStatus)
	sc.Step(`^the HTTP GET entity effective state has field "([^"]*)" equal to "([^"]*)"$`, stepHTTPEntityStateField)
	sc.Step(`^registry sync delay passes$`, stepRegistrySyncDelay)
	sc.Step(`^the registry entity for plugin "([^"]*)" entity "([^"]*)" has sync_status "([^"]*)"$`, stepRegistryEntitySyncStatus)
	sc.Step(`^the registry entity effective state has field "([^"]*)" equal to "([^"]*)"$`, stepRegistryEntityStateField)
	sc.Step(`^plugin "([^"]*)" subscribes to entity events$`, stepPluginSubscribeEvents)
	sc.Step(`^all 10 event plugins subscribe to entity events$`, stepAllPluginsSubscribeEvents)
	sc.Step(`^plugin "([^"]*)" receives an event for entity "([^"]*)" from plugin "([^"]*)"$`, stepPluginReceivesEvent)
	sc.Step(`^all 10 event plugins receive an event for entity "([^"]*)"$`, stepAllPluginsReceiveEvent)
	sc.Step(`^the event ingest response has sync_status "([^"]*)"$`, stepIngestResponseSyncStatus)
	sc.Step(`^the event ingest response effective state has field "([^"]*)" equal to "([^"]*)"$`, stepIngestResponseStateField)
}

// ---------------------------------------------------------------------------
// Event environment names
// ---------------------------------------------------------------------------

var eventPluginDomains = map[string]string{
	"event-switch-01": "switch",
	"event-switch-02": "switch",
	"event-light-01":  "light",
	"event-light-02":  "light",
	"event-rgb-01":    "light.rgb",
	"event-rgb-02":    "light.rgb",
	"event-temp-01":   "sensor.temperature",
	"event-temp-02":   "sensor.temperature",
	"event-motion-01": "binary_sensor",
	"event-motion-02": "binary_sensor",
}

// actionsForDomain returns the commands a domain supports.
func actionsForDomain(domain string) []string {
	switch domain {
	case "switch":
		return []string{"turn_on", "turn_off"}
	case "light":
		return []string{"turn_on", "turn_off", "set_brightness"}
	case "light.rgb":
		return []string{"turn_on", "turn_off", "set_color"}
	default:
		// sensor.temperature, binary_sensor — observation-only
		return nil
	}
}

// ---------------------------------------------------------------------------
// Background setup
// ---------------------------------------------------------------------------

func stepEventsEnvSetup(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	for pluginID, domain := range eventPluginDomains {
		p := w.plugin(pluginID)
		p.Devices = []types.Device{{
			ID:        "main-device",
			PluginID:  pluginID,
			LocalName: pluginID + " main device",
		}}
		p.Entities = []types.Entity{{
			ID:       "main-entity",
			PluginID: pluginID,
			DeviceID: "main-device",
			Domain:   domain,
			Actions:  actionsForDomain(domain),
		}}
		if err := p.Start(); err != nil {
			return ctx, fmt.Errorf("start %s: %w", pluginID, err)
		}
		if err := p.SeedRegistry(); err != nil {
			return ctx, fmt.Errorf("seed %s: %w", pluginID, err)
		}
	}
	return ctx, nil
}

// ---------------------------------------------------------------------------
// Event ingest steps
// ---------------------------------------------------------------------------

// ingestEventHTTP posts a raw JSON payload to the events endpoint and returns
// the HTTP response. commandID may be empty (no X-Command-ID header).
func ingestEventHTTP(w *World, pluginID, entityID, rawPayload, commandID string) error {
	var payload map[string]any
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
		return fmt.Errorf("bad event payload %q: %w", rawPayload, err)
	}
	path := fmt.Sprintf("/api/plugins/%s/devices/main-device/entities/%s/events", pluginID, entityID)
	headers := map[string]string{}
	if commandID != "" {
		headers["X-Command-ID"] = commandID
	}
	resp, err := w.Harness.PostWithHeaders(path, payload, headers)
	if err != nil {
		return err
	}
	if err := w.do(resp, nil); err != nil {
		return err
	}
	if w.LastResp.StatusCode != 200 {
		return fmt.Errorf("expected 200 from event ingest, got %d\nbody: %s", w.LastResp.StatusCode, w.LastBody)
	}
	// Decode and stash the returned entity.
	var ent types.Entity
	if err := json.Unmarshal(w.LastBody, &ent); err != nil {
		return fmt.Errorf("decode entity response: %w\nbody: %s", err, w.LastBody)
	}
	w.LastHTTPEntity = &ent
	return nil
}

func stepIngestEvent(ctx context.Context, pluginID, rawPayload, entityID string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, ingestEventHTTP(w, pluginID, entityID, rawPayload, "")
}

func stepIngestEventCorrelated(ctx context.Context, pluginID, rawPayload, entityID string) (context.Context, error) {
	w := worldFrom(ctx)
	return ctx, ingestEventHTTP(w, pluginID, entityID, rawPayload, w.CommandID)
}

// ---------------------------------------------------------------------------
// Entity state assertion helpers
// ---------------------------------------------------------------------------

// effectiveStateMap returns the Effective state of an entity as a map.
func effectiveStateMap(ent types.Entity) (map[string]any, error) {
	if len(ent.Data.Effective) == 0 {
		return nil, fmt.Errorf("entity %q/%q has no effective state", ent.PluginID, ent.ID)
	}
	var m map[string]any
	if err := json.Unmarshal(ent.Data.Effective, &m); err != nil {
		return nil, fmt.Errorf("unmarshal effective state: %w", err)
	}
	return m, nil
}

// assertStateField checks that field key has the expected value (string representation).
func assertStateField(ent types.Entity, key, wantStr string) error {
	m, err := effectiveStateMap(ent)
	if err != nil {
		return err
	}
	v, ok := m[key]
	if !ok {
		return fmt.Errorf("state field %q not found; state: %s", key, ent.Data.Effective)
	}
	// Normalise: JSON numbers become float64, booleans become bool.
	var gotStr string
	switch tv := v.(type) {
	case bool:
		gotStr = strconv.FormatBool(tv)
	case float64:
		// Avoid unnecessary ".0" suffix for integers.
		if tv == float64(int64(tv)) {
			gotStr = strconv.FormatInt(int64(tv), 10)
		} else {
			gotStr = strconv.FormatFloat(tv, 'f', -1, 64)
		}
	case string:
		gotStr = tv
	default:
		gotStr = fmt.Sprintf("%v", tv)
	}
	if gotStr != wantStr {
		return fmt.Errorf("state field %q: expected %q, got %q", key, wantStr, gotStr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Entity state step implementations
// ---------------------------------------------------------------------------

func stepEntitySyncStatus(ctx context.Context, entityID, pluginID, wantStatus string) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	ent, ok := p.GetEntity("main-device", entityID)
	if !ok {
		return fmt.Errorf("entity %q not found in plugin %q", entityID, pluginID)
	}
	got := string(ent.Data.SyncStatus)
	if got != wantStatus {
		return fmt.Errorf("sync_status: expected %q, got %q", wantStatus, got)
	}
	return nil
}

func stepEntityStateField(ctx context.Context, entityID, pluginID, key, wantStr string) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	ent, ok := p.GetEntity("main-device", entityID)
	if !ok {
		return fmt.Errorf("entity %q not found in plugin %q", entityID, pluginID)
	}
	return assertStateField(ent, key, wantStr)
}

func stepEntityLastCommandID(ctx context.Context, entityID, pluginID string) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	ent, ok := p.GetEntity("main-device", entityID)
	if !ok {
		return fmt.Errorf("entity %q not found in plugin %q", entityID, pluginID)
	}
	if w.CommandID == "" {
		return fmt.Errorf("no command ID recorded in world")
	}
	if ent.Data.LastCommandID != w.CommandID {
		return fmt.Errorf("entity.LastCommandID: expected %q, got %q", w.CommandID, ent.Data.LastCommandID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// HTTP GET entity assertions
// ---------------------------------------------------------------------------

func stepHTTPEntitySyncStatus(ctx context.Context, pluginID, deviceID, entityID, wantStatus string) (context.Context, error) {
	w := worldFrom(ctx)
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s", pluginID, deviceID, entityID)
	if err := w.do(w.Harness.Get(path)); err != nil {
		return ctx, err
	}
	if w.LastResp.StatusCode != 200 {
		return ctx, fmt.Errorf("GET entity: expected 200, got %d\nbody: %s", w.LastResp.StatusCode, w.LastBody)
	}
	var ent types.Entity
	if err := json.Unmarshal(w.LastBody, &ent); err != nil {
		return ctx, fmt.Errorf("decode entity: %w\nbody: %s", err, w.LastBody)
	}
	w.LastHTTPEntity = &ent
	got := string(ent.Data.SyncStatus)
	if got != wantStatus {
		return ctx, fmt.Errorf("HTTP entity sync_status: expected %q, got %q", wantStatus, got)
	}
	return ctx, nil
}

func stepHTTPEntityStateField(ctx context.Context, key, wantStr string) error {
	w := worldFrom(ctx)
	if w.LastHTTPEntity == nil {
		return fmt.Errorf("no HTTP entity stored; run an HTTP GET entity step first")
	}
	return assertStateField(*w.LastHTTPEntity, key, wantStr)
}

// ---------------------------------------------------------------------------
// Registry aggregate assertions
// ---------------------------------------------------------------------------

func stepRegistrySyncDelay(_ context.Context) error {
	time.Sleep(60 * time.Millisecond)
	return nil
}

func stepRegistryEntitySyncStatus(ctx context.Context, pluginID, entityID, wantStatus string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	results, err := p.QueryRegistry(types.SearchQuery{PluginID: pluginID, EntityID: entityID})
	if err != nil {
		return ctx, fmt.Errorf("QueryRegistry: %w", err)
	}
	if len(results) == 0 {
		return ctx, fmt.Errorf("registry returned 0 entities for plugin=%s entity=%s", pluginID, entityID)
	}
	ent := results[0]
	// Stash for subsequent field assertions.
	w.LastHTTPEntity = &ent
	got := string(ent.Data.SyncStatus)
	if got != wantStatus {
		return ctx, fmt.Errorf("registry sync_status: expected %q, got %q", wantStatus, got)
	}
	return ctx, nil
}

func stepRegistryEntityStateField(ctx context.Context, key, wantStr string) error {
	w := worldFrom(ctx)
	if w.LastHTTPEntity == nil {
		return fmt.Errorf("no registry entity stored; run a registry entity step first")
	}
	return assertStateField(*w.LastHTTPEntity, key, wantStr)
}

// ---------------------------------------------------------------------------
// Cross-plugin subscription steps
// ---------------------------------------------------------------------------

func stepPluginSubscribeEvents(ctx context.Context, pluginID string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	ch, err := p.SubscribeEvents()
	if err != nil {
		return ctx, fmt.Errorf("plugin %s SubscribeEvents: %w", pluginID, err)
	}
	w.EventChannels[pluginID] = ch
	return ctx, nil
}

func stepAllPluginsSubscribeEvents(ctx context.Context) (context.Context, error) {
	w := worldFrom(ctx)
	for pluginID := range eventPluginDomains {
		p := w.plugin(pluginID)
		ch, err := p.SubscribeEvents()
		if err != nil {
			return ctx, fmt.Errorf("plugin %s SubscribeEvents: %w", pluginID, err)
		}
		w.EventChannels[pluginID] = ch
	}
	return ctx, nil
}

// waitForEvent reads from ch until an envelope matching entityID and
// optionally fromPluginID arrives, or the timeout expires.
func waitForEvent(ch <-chan types.EntityEventEnvelope, entityID, fromPluginID string, timeout time.Duration) (types.EntityEventEnvelope, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				return types.EntityEventEnvelope{}, false
			}
			if env.EntityID != entityID {
				continue
			}
			if fromPluginID != "" && env.PluginID != fromPluginID {
				continue
			}
			return env, true
		case <-deadline:
			return types.EntityEventEnvelope{}, false
		}
	}
}

func stepPluginReceivesEvent(ctx context.Context, subscriberID, entityID, senderID string) error {
	w := worldFrom(ctx)
	ch, ok := w.EventChannels[subscriberID]
	if !ok {
		return fmt.Errorf("plugin %q is not subscribed to events; add a subscribe step first", subscriberID)
	}
	if _, received := waitForEvent(ch, entityID, senderID, 2*time.Second); !received {
		return fmt.Errorf("plugin %q did not receive an event for entity %q from plugin %q within 2s", subscriberID, entityID, senderID)
	}
	return nil
}

func stepAllPluginsReceiveEvent(ctx context.Context, entityID string) error {
	w := worldFrom(ctx)
	var missing []string
	for pluginID := range eventPluginDomains {
		ch, ok := w.EventChannels[pluginID]
		if !ok {
			missing = append(missing, pluginID+" (not subscribed)")
			continue
		}
		if _, received := waitForEvent(ch, entityID, "", 2*time.Second); !received {
			missing = append(missing, pluginID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("plugins that did not receive the event: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Event ingest response assertions
// ---------------------------------------------------------------------------

func stepIngestResponseSyncStatus(ctx context.Context, wantStatus string) error {
	w := worldFrom(ctx)
	if w.LastHTTPEntity == nil {
		return fmt.Errorf("no event ingest response entity; run an ingest step first")
	}
	got := string(w.LastHTTPEntity.Data.SyncStatus)
	if got != wantStatus {
		return fmt.Errorf("ingest response sync_status: expected %q, got %q", wantStatus, got)
	}
	return nil
}

func stepIngestResponseStateField(ctx context.Context, key, wantStr string) error {
	w := worldFrom(ctx)
	if w.LastHTTPEntity == nil {
		return fmt.Errorf("no event ingest response entity; run an ingest step first")
	}
	return assertStateField(*w.LastHTTPEntity, key, wantStr)
}

// keep imports referenced
var _ = regsvc.WithNATS
