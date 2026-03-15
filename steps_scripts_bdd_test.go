//go:build bdd

package main

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"
)

func registerScriptSteps(sc *godog.ScenarioContext) {
	sc.Step(`^plugin "([^"]*)" stores a script for entity "([^"]*)":$`, stepStoreScript)
	sc.Step(`^I get the script for entity "([^"]*)" on plugin "([^"]*)"$`, stepGetScript)
	sc.Step(`^the script body contains "([^"]*)"$`, stepScriptBodyContains)
	sc.Step(`^I execute the script for entity "([^"]*)" on plugin "([^"]*)" with state "([^"]*)"$`, stepExecuteScript)
	sc.Step(`^the lua result is "([^"]*)"$`, stepLuaResultIs)
	sc.Step(`^I delete the script for entity "([^"]*)" on plugin "([^"]*)"$`, stepDeleteScript)
}

// scriptPath builds the canonical script path, looking up the device_id from the plugin.
func scriptPath(w *World, pluginID, entityID string) (string, error) {
	p := w.plugin(pluginID)
	deviceID := ""
	for _, e := range p.Entities {
		if e.ID == entityID {
			deviceID = e.DeviceID
			break
		}
	}
	if deviceID == "" {
		// Use a placeholder if device not found (e.g. testing 404 paths)
		deviceID = "unknown"
	}
	return fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/script", pluginID, deviceID, entityID), nil
}

func stepStoreScript(ctx context.Context, pluginID, entityID string, src *godog.DocString) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	p.StoreScript(entityID, src.Content)
	return nil
}

func stepGetScript(ctx context.Context, entityID, pluginID string) error {
	w := worldFrom(ctx)
	path, err := scriptPath(w, pluginID, entityID)
	if err != nil {
		return err
	}
	return w.do(w.Harness.Get(path))
}

func stepScriptBodyContains(ctx context.Context, substr string) error {
	w := worldFrom(ctx)
	body := string(w.LastBody)
	if !contains(body, substr) {
		return fmt.Errorf("expected script body to contain %q, got: %s", substr, body)
	}
	return nil
}

func stepExecuteScript(ctx context.Context, entityID, pluginID, state string) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	result, err := p.RunScript(entityID, state)
	if err != nil {
		return fmt.Errorf("execute script: %w", err)
	}
	w.LuaResult = result
	return nil
}

func stepLuaResultIs(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.LuaResult != want {
		return fmt.Errorf("expected lua result %q, got %q", want, w.LuaResult)
	}
	return nil
}

func stepDeleteScript(ctx context.Context, entityID, pluginID string) error {
	w := worldFrom(ctx)
	path, err := scriptPath(w, pluginID, entityID)
	if err != nil {
		return err
	}
	return w.do(w.Harness.Delete(path))
}
