//go:build bdd

package main

// BDD step definitions for the Entity Meta Store feature.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/cucumber/godog"
)

// ---------------------------------------------------------------------------
// Context state
// ---------------------------------------------------------------------------

type entityMetaCtxKey struct{}

type entityMetaState struct {
	pluginID string
	deviceID string
	entityID string
}

func withEntityMetaState(ctx context.Context) (context.Context, *entityMetaState) {
	s := &entityMetaState{}
	return context.WithValue(ctx, entityMetaCtxKey{}, s), s
}

func getEntityMetaState(ctx context.Context) *entityMetaState {
	if v := ctx.Value(entityMetaCtxKey{}); v != nil {
		return v.(*entityMetaState)
	}
	return &entityMetaState{}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func entityMetaPath(pluginID, deviceID, entityID string) string {
	return fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/meta",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID))
}

func entityPath(pluginID, deviceID, entityID string) string {
	return fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID))
}

// jsonEqual compares two JSON values for semantic equality.
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	am, _ := json.Marshal(av)
	bm, _ := json.Marshal(bv)
	return string(am) == string(bm)
}

// pollEntityMeta repeatedly GETs an entity until the meta predicate returns
// true or the deadline passes.
func pollEntityMeta(w *World, pluginID, deviceID, entityID string,
	predicate func(meta map[string]json.RawMessage) bool,
	errMsg func(body []byte) string,
) error {
	path := entityPath(pluginID, deviceID, entityID)
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		if err := w.do(w.Harness.Get(path)); err != nil {
			return err
		}
		var body map[string]json.RawMessage
		if err := w.decodeLastBody(&body); err == nil {
			var meta map[string]json.RawMessage
			if raw, ok := body["meta"]; ok {
				_ = json.Unmarshal(raw, &meta)
			}
			if predicate(meta) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s", errMsg(w.LastBody))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Step functions
// ---------------------------------------------------------------------------

// "I patch meta on entity "X" plugin "Y" device "Z" with key "K" and value {json}"
func stepPatchEntityMeta(ctx context.Context, entityID, pluginID, deviceID, key, valueJSON string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEntityMetaState(ctx)
	s.pluginID, s.deviceID, s.entityID = pluginID, deviceID, entityID
	body := map[string]json.RawMessage{key: json.RawMessage(valueJSON)}
	return ctx, w.do(w.Harness.Patch(entityMetaPath(pluginID, deviceID, entityID), body))
}

// "I patch meta on entity "X" plugin "Y" device "Z" with empty body"
func stepPatchEntityMetaEmpty(ctx context.Context, entityID, pluginID, deviceID string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEntityMetaState(ctx)
	s.pluginID, s.deviceID, s.entityID = pluginID, deviceID, entityID
	return ctx, w.do(w.Harness.Patch(entityMetaPath(pluginID, deviceID, entityID), map[string]json.RawMessage{}))
}

// "I delete meta key "K" on entity "X" plugin "Y" device "Z""
func stepDeleteEntityMetaKey(ctx context.Context, key, entityID, pluginID, deviceID string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEntityMetaState(ctx)
	s.pluginID, s.deviceID, s.entityID = pluginID, deviceID, entityID
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/meta/%s",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID), url.PathEscape(key))
	return ctx, w.do(w.Harness.Delete(path))
}

// "entity "X" plugin "Y" device "Z" has meta key "K" with value {json}" (Given setup step)
func stepGivenEntityHasMetaKey(ctx context.Context, entityID, pluginID, deviceID, key, valueJSON string) (context.Context, error) {
	return stepPatchEntityMeta(ctx, entityID, pluginID, deviceID, key, valueJSON)
}

// "the entity meta contains key "K" with value {json}"
func stepEntityMetaContainsKey(ctx context.Context, key, valueJSON string) error {
	s := getEntityMetaState(ctx)
	if s.pluginID == "" {
		return fmt.Errorf("no entity context: call a PATCH/DELETE/GET entity step first")
	}
	return pollEntityMeta(worldFrom(ctx), s.pluginID, s.deviceID, s.entityID,
		func(meta map[string]json.RawMessage) bool {
			v, ok := meta[key]
			return ok && jsonEqual(v, json.RawMessage(valueJSON))
		},
		func(body []byte) string {
			return fmt.Sprintf("entity meta key %q with value %s not found; body: %s", key, valueJSON, body)
		},
	)
}

// "the entity meta does not contain key "K""
func stepEntityMetaNotContainsKey(ctx context.Context, key string) error {
	s := getEntityMetaState(ctx)
	if s.pluginID == "" {
		return fmt.Errorf("no entity context: call a PATCH/DELETE/GET entity step first")
	}
	return pollEntityMeta(worldFrom(ctx), s.pluginID, s.deviceID, s.entityID,
		func(meta map[string]json.RawMessage) bool {
			_, present := meta[key]
			return !present
		},
		func(body []byte) string {
			return fmt.Sprintf("entity meta still contains key %q; body: %s", key, body)
		},
	)
}

// "I patch labels on entity "X" plugin "Y" device "Z" with "K" "V""
func stepPatchEntityLabelsForMeta(ctx context.Context, entityID, pluginID, deviceID, labelKey, labelVal string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEntityMetaState(ctx)
	s.pluginID, s.deviceID, s.entityID = pluginID, deviceID, entityID
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/labels",
		url.PathEscape(pluginID), url.PathEscape(deviceID), url.PathEscape(entityID))
	body := map[string]any{"labels": map[string][]string{labelKey: {labelVal}}}
	return ctx, w.do(w.Harness.Patch(path, body))
}

// "I get entity "X" on plugin "Y" device "Z""
func stepGetEntityForMeta(ctx context.Context, entityID, pluginID, deviceID string) (context.Context, error) {
	w := worldFrom(ctx)
	s := getEntityMetaState(ctx)
	s.pluginID, s.deviceID, s.entityID = pluginID, deviceID, entityID
	return ctx, w.do(w.Harness.Get(entityPath(pluginID, deviceID, entityID)))
}

// ---------------------------------------------------------------------------
// Step registration
// ---------------------------------------------------------------------------

func registerEntityMetaSteps(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ctx, _ = withEntityMetaState(ctx)
		return ctx, nil
	})

	sc.Step(`^I patch meta on entity "([^"]*)" plugin "([^"]*)" device "([^"]*)" with key "([^"]*)" and value (.+)$`,
		stepPatchEntityMeta)
	sc.Step(`^I patch meta on entity "([^"]*)" plugin "([^"]*)" device "([^"]*)" with empty body$`,
		stepPatchEntityMetaEmpty)
	sc.Step(`^I delete meta key "([^"]*)" on entity "([^"]*)" plugin "([^"]*)" device "([^"]*)"$`,
		stepDeleteEntityMetaKey)
	sc.Step(`^entity "([^"]*)" plugin "([^"]*)" device "([^"]*)" has meta key "([^"]*)" with value (.+)$`,
		stepGivenEntityHasMetaKey)
	sc.Step(`^the entity meta contains key "([^"]*)" with value (.+)$`,
		stepEntityMetaContainsKey)
	sc.Step(`^the entity meta does not contain key "([^"]*)"$`,
		stepEntityMetaNotContainsKey)
	sc.Step(`^I patch labels on entity "([^"]*)" plugin "([^"]*)" device "([^"]*)" with "([^"]*)" "([^"]*)"$`,
		stepPatchEntityLabelsForMeta)
	sc.Step(`^I get entity "([^"]*)" on plugin "([^"]*)" device "([^"]*)"$`,
		stepGetEntityForMeta)
}
