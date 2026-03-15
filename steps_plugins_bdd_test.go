//go:build bdd

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

func registerPluginSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a running plugin "([^"]*)"$`, stepRunningPlugin)
	sc.Step(`^a running plugin "([^"]*)" with devices:$`, stepRunningPluginWithDevices)
	sc.Step(`^plugin "([^"]*)" has entities:$`, stepPluginHasEntities)
	sc.Step(`^I list all plugins$`, stepListAllPlugins)
	sc.Step(`^the plugin list contains "([^"]*)"$`, stepPluginListContains)
	sc.Step(`^I list devices for plugin "([^"]*)"$`, stepListDevicesForPlugin)
	sc.Step(`^I check health for plugin "([^"]*)"$`, stepCheckHealthForPlugin)
	sc.Step(`^I search for plugins$`, stepSearchPlugins)
	sc.Step(`^the results contain at least (\d+) manifests$`, stepResultsContainAtLeastManifests)
}

func stepRunningPlugin(ctx context.Context, pluginID string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	if err := p.Start(); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func stepRunningPluginWithDevices(ctx context.Context, pluginID string, table *godog.Table) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	for _, row := range table.Rows[1:] { // skip header
		p.Devices = append(p.Devices, types.Device{
			ID:        row.Cells[0].Value,
			LocalName: row.Cells[1].Value,
		})
	}
	if err := p.Start(); err != nil {
		return ctx, err
	}
	if err := p.SeedRegistry(); err != nil {
		return ctx, fmt.Errorf("seed registry for %s: %w", pluginID, err)
	}
	return ctx, nil
}

func stepPluginHasEntities(ctx context.Context, pluginID string, table *godog.Table) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	for _, row := range table.Rows[1:] {
		ent := types.Entity{
			ID:        row.Cells[0].Value,
			DeviceID:  row.Cells[1].Value,
			Domain:    row.Cells[2].Value,
			LocalName: row.Cells[3].Value,
		}
		// actions column is optional (5th column)
		if len(row.Cells) > 4 && row.Cells[4].Value != "" {
			actions := splitComma(row.Cells[4].Value)
			ent.Actions = actions
		}
		p.Entities = append(p.Entities, ent)
	}
	// re-seed registry with updated entities
	if err := p.SeedRegistry(); err != nil {
		return ctx, fmt.Errorf("seed registry for %s: %w", pluginID, err)
	}
	return ctx, nil
}

func stepListAllPlugins(ctx context.Context) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/api/plugins"))
}

func stepPluginListContains(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	var body map[string]types.Registration
	if err := w.decodeLastBody(&body); err != nil {
		return err
	}
	if _, ok := body[pluginID]; !ok {
		return fmt.Errorf("plugin %q not found in list: %v", pluginID, keys(body))
	}
	return nil
}

func stepListDevicesForPlugin(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/api/plugins/" + pluginID + "/devices"))
}

func stepCheckHealthForPlugin(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/_internal/health?id=" + pluginID))
}

func stepSearchPlugins(ctx context.Context) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/api/search/plugins"))
}

func stepResultsContainAtLeastManifests(ctx context.Context, minCount int) error {
	w := worldFrom(ctx)
	if err := w.decodeLastBody(&w.Manifests); err != nil {
		return err
	}
	if len(w.Manifests) < minCount {
		return fmt.Errorf("expected at least %d manifests, got %d", minCount, len(w.Manifests))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helpers used across step files
// ---------------------------------------------------------------------------

func keys[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func stepResponseStatusIs(ctx context.Context, want int) error {
	return worldFrom(ctx).assertLastStatus(want)
}

// registerCommonSteps adds the generic "the response status is N" step.
// Called once from plugins so it is available everywhere.
func registerCommonSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the response status is (\d+)$`, stepResponseStatusIs)
	sc.Step(`^the response body contains "([^"]*)"$`, func(ctx context.Context, substr string) error {
		w := worldFrom(ctx)
		body := string(w.LastBody)
		if !contains(body, substr) {
			return fmt.Errorf("expected body to contain %q, got: %s", substr, body)
		}
		return nil
	})
}

// stepResponseStatusIs is also used via registerCommonSteps — register it once in InitializeScenario.
func init() {}

// contains is a simple substring check.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

var _ = http.StatusOK // keep net/http import for apiHarness.get return type
