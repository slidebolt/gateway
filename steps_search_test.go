package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

func registerSearchSteps(sc *godog.ScenarioContext) {
	sc.Step(`^I search entities$`, stepSearchEntities)
	sc.Step(`^I search entities with domain "([^"]*)"$`, stepSearchEntitiesDomain)
	sc.Step(`^I search entities with plugin "([^"]*)"$`, stepSearchEntitiesPlugin)
	sc.Step(`^I search entities with plugin "([^"]*)" and domain "([^"]*)"$`, stepSearchEntitiesPluginDomain)
	sc.Step(`^I search devices with plugin "([^"]*)"$`, stepSearchDevicesPlugin)
	sc.Step(`^the entity results contain (\d+) items$`, stepEntityResultsCount)
	sc.Step(`^the device results contain (\d+) items$`, stepDeviceResultsCount)
	sc.Step(`^every entity result has domain "([^"]*)"$`, stepEveryEntityDomain)
	sc.Step(`^entity result "([^"]*)" exists$`, stepEntityResultExists)
	sc.Step(`^entity results include plugin "([^"]*)"$`, stepEntityResultsIncludePlugin)
}

func stepSearchEntities(ctx context.Context) error {
	w := worldFrom(ctx)
	if err := w.do(w.Harness.Get("/api/search/entities")); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchEntitiesDomain(ctx context.Context, domain string) error {
	w := worldFrom(ctx)
	path := "/api/search/entities?" + url.Values{"domain": {domain}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchEntitiesPlugin(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	path := "/api/search/entities?" + url.Values{"plugin_id": {pluginID}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchEntitiesPluginDomain(ctx context.Context, pluginID, domain string) error {
	w := worldFrom(ctx)
	path := "/api/search/entities?" + url.Values{"plugin_id": {pluginID}, "domain": {domain}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchDevicesPlugin(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	path := "/api/search/devices?" + url.Values{"plugin_id": {pluginID}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Devices)
}

func stepEntityResultsCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	if w.LastResp != nil && w.LastResp.StatusCode != 200 {
		return fmt.Errorf("cannot check count: HTTP %d", w.LastResp.StatusCode)
	}
	if len(w.Entities) != want {
		return fmt.Errorf("expected %d entities, got %d: %v", want, len(w.Entities), entityIDs(w.Entities))
	}
	return nil
}

func stepDeviceResultsCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	if len(w.Devices) != want {
		return fmt.Errorf("expected %d devices, got %d", want, len(w.Devices))
	}
	return nil
}

func stepEveryEntityDomain(ctx context.Context, domain string) error {
	w := worldFrom(ctx)
	for _, e := range w.Entities {
		if e.Domain != domain {
			return fmt.Errorf("entity %s has domain %q, expected %q", e.ID, e.Domain, domain)
		}
	}
	return nil
}

func stepEntityResultExists(ctx context.Context, entityID string) error {
	w := worldFrom(ctx)
	for _, e := range w.Entities {
		if e.ID == entityID {
			return nil
		}
	}
	return fmt.Errorf("entity %q not found in results: %v", entityID, entityIDs(w.Entities))
}

func stepEntityResultsIncludePlugin(ctx context.Context, pluginID string) error {
	w := worldFrom(ctx)
	for _, e := range w.Entities {
		if e.PluginID == pluginID {
			return nil
		}
	}
	return fmt.Errorf("no entity from plugin %q in results: %v", pluginID, entityIDs(w.Entities))
}

func entityIDs(entities []types.Entity) []string {
	ids := make([]string, len(entities))
	for i, e := range entities {
		ids[i] = e.ID + "(" + e.PluginID + ")"
	}
	return ids
}
