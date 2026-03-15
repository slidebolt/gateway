//go:build bdd

package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

func registerBroadSearchSteps(sc *godog.ScenarioContext) {
	sc.Step(`^(\d+) simulated plugins each with (\d+) devices and (\d+) entities$`, stepScaleSetup)
	sc.Step(`^I search entities matching "([^"]*)"$`, stepSearchEntitiesMatching)
	sc.Step(`^I search entities with label "([^"]*)"$`, stepSearchEntitiesLabel)
	sc.Step(`^the search returns (\d+) entities$`, stepSearchReturnsCount)
	sc.Step(`^plugin "([^"]*)" queries the registry for entities matching "([^"]*)"$`, stepPluginQueryRegistryMatching)
	sc.Step(`^plugin "([^"]*)" queries the registry for entities with label "([^"]*)"$`, stepPluginQueryRegistryLabel)
	sc.Step(`^the registry query returns (\d+) entities$`, stepRegistryQueryReturnsCount)
}

// stepScaleSetup creates N plugins, each with D devices, each device with E entities.
// Naming: plugin-01 … plugin-N
//
//	device: plugin-01-device-01 … plugin-01-device-D
//	entity: plugin-01-device-01-entity-01 … plugin-01-device-01-entity-E
//
// Labels (per entity from plugin P, device D):
//
//	Plugin : Plugin-P   (e.g. Plugin-01)
//	Device : plugin-P-device-D (globally unique device identifier)
func stepScaleSetup(ctx context.Context, nPlugins, nDevices, nEntities int) (context.Context, error) {
	w := worldFrom(ctx)

	for pi := 1; pi <= nPlugins; pi++ {
		pluginID := fmt.Sprintf("plugin-%02d", pi)
		pluginLabel := fmt.Sprintf("Plugin-%02d", pi)

		p := w.plugin(pluginID)

		for di := 1; di <= nDevices; di++ {
			deviceID := fmt.Sprintf("%s-device-%02d", pluginID, di)
			deviceLabel := fmt.Sprintf("%s-device-%02d", pluginID, di)

			p.Devices = append(p.Devices, types.Device{
				ID:        deviceID,
				LocalName: deviceID,
				Labels: map[string][]string{
					"Plugin": {pluginLabel},
					"Device": {deviceLabel},
				},
			})

			for ei := 1; ei <= nEntities; ei++ {
				entityID := fmt.Sprintf("%s-entity-%02d", deviceID, ei)
				p.Entities = append(p.Entities, types.Entity{
					ID:        entityID,
					DeviceID:  deviceID,
					LocalName: entityID,
					Domain:    "sensor",
					Labels: map[string][]string{
						"Plugin": {pluginLabel},
						"Device": {deviceLabel},
					},
				})
			}
		}

		if err := p.Start(); err != nil {
			return ctx, fmt.Errorf("start %s: %w", pluginID, err)
		}
		if err := p.SeedRegistry(); err != nil {
			return ctx, fmt.Errorf("seed %s: %w", pluginID, err)
		}
	}

	// Brief settle time for all NATS events to propagate into the aggregate.
	time.Sleep(50 * time.Millisecond)
	return ctx, nil
}

func stepSearchEntitiesMatching(ctx context.Context, pattern string) error {
	w := worldFrom(ctx)
	path := "/api/search/entities?" + url.Values{"q": {pattern}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchEntitiesLabel(ctx context.Context, labelPair string) error {
	w := worldFrom(ctx)
	path := "/api/search/entities?" + url.Values{"label": {labelPair}}.Encode()
	if err := w.do(w.Harness.Get(path)); err != nil {
		return err
	}
	return w.decodeLastBody(&w.Entities)
}

func stepSearchReturnsCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	if w.LastResp != nil && w.LastResp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", w.LastResp.StatusCode, w.LastBody)
	}
	if len(w.Entities) != want {
		return fmt.Errorf("expected %d entities, got %d", want, len(w.Entities))
	}
	return nil
}

// stepPluginQueryRegistryMatching simulates a plugin directly querying the NATS
// aggregate — bypassing the HTTP gateway entirely.
func stepPluginQueryRegistryMatching(ctx context.Context, pluginID, pattern string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	results, err := p.QueryRegistry(types.SearchQuery{Pattern: pattern})
	if err != nil {
		return ctx, fmt.Errorf("plugin %s registry query: %w", pluginID, err)
	}
	w.Entities = results
	return ctx, nil
}

func stepPluginQueryRegistryLabel(ctx context.Context, pluginID, labelPair string) (context.Context, error) {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	results, err := p.QueryRegistry(types.SearchQuery{Labels: types.ParseLabels([]string{labelPair})})
	if err != nil {
		return ctx, fmt.Errorf("plugin %s registry label query %q: %w", pluginID, labelPair, err)
	}
	w.Entities = results
	return ctx, nil
}

func stepRegistryQueryReturnsCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	if len(w.Entities) != want {
		return fmt.Errorf("expected %d entities from registry query, got %d", want, len(w.Entities))
	}
	return nil
}

var _ = &godog.ScenarioContext{}
var _ = types.Entity{}
var _ = types.SearchQuery{}
