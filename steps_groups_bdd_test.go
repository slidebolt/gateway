//go:build bdd

package main

// BDD step definitions for the Groups feature.
// Group entities have CommandQuery set; commands fan out to all matching entities.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cucumber/godog"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Context state
// ---------------------------------------------------------------------------

type groupsCtxKey struct{}

type groupsState struct {
	lastCommandID string
	// gateway-seeded group entities keyed by entity ID
	groups map[string]types.Entity
}

func withGroupsState(ctx context.Context) (context.Context, *groupsState) {
	gs := &groupsState{groups: make(map[string]types.Entity)}
	return context.WithValue(ctx, groupsCtxKey{}, gs), gs
}

func getGroupsState(ctx context.Context) *groupsState {
	if v := ctx.Value(groupsCtxKey{}); v != nil {
		return v.(*groupsState)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Registry helpers for gateway-owned entities
// ---------------------------------------------------------------------------

// seedGatewayEntity saves an entity under the __gateway__ plugin in the
// aggregate registry (same pattern as SimulatedPlugin.SeedRegistry).
func seedGatewayEntity(h *Harness, e types.Entity) error {
	pluginReg := regsvc.RegistryService(gatewayPluginID,
		regsvc.WithNATS(h.NC),
		regsvc.WithPersist(regsvc.PersistNever),
	)
	if err := pluginReg.Start(); err != nil {
		return fmt.Errorf("seedGatewayEntity start: %w", err)
	}
	defer pluginReg.Stop()
	if err := pluginReg.SaveEntity(e); err != nil {
		return fmt.Errorf("seedGatewayEntity save: %w", err)
	}
	time.Sleep(40 * time.Millisecond) // let aggregate propagate
	return nil
}

// parseLabel splits "Key:Value" into (key, value, ok).
func parseLabel(s string) (string, string, bool) {
	k, v, ok := strings.Cut(s, ":")
	return k, v, ok
}

// ---------------------------------------------------------------------------
// Step functions
// ---------------------------------------------------------------------------

// "a group entity "basement" with domain "light" and CommandQuery label "Room:Basement""
func stepGroupEntityWithQueryLabel(ctx context.Context, entityID, domain, label string) (context.Context, error) {
	w := worldFrom(ctx)
	gs := getGroupsState(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid CommandQuery label %q", label)
	}
	ent := types.Entity{
		ID:        entityID,
		PluginID:  gatewayPluginID,
		DeviceID:  "groups",
		Domain:    domain,
		LocalName: entityID,
		CommandQuery: &types.SearchQuery{
			Labels: map[string][]string{k: {v}},
		},
	}
	gs.groups[entityID] = ent
	return ctx, seedGatewayEntity(w.Harness, ent)
}

// "a group entity "X" with domain "Y" and CommandQuery label "Z" and label "A:B""
// Group entity that also carries its own labels (used for chaining).
func stepGroupEntityWithQueryLabelAndOwnLabel(ctx context.Context, entityID, domain, queryLabel, ownLabel string) (context.Context, error) {
	w := worldFrom(ctx)
	gs := getGroupsState(ctx)
	qk, qv, ok := parseLabel(queryLabel)
	if !ok {
		return ctx, fmt.Errorf("invalid CommandQuery label %q", queryLabel)
	}
	lk, lv, ok := parseLabel(ownLabel)
	if !ok {
		return ctx, fmt.Errorf("invalid own label %q", ownLabel)
	}
	ent := types.Entity{
		ID:        entityID,
		PluginID:  gatewayPluginID,
		DeviceID:  "groups",
		Domain:    domain,
		LocalName: entityID,
		Labels:    map[string][]string{lk: {lv}},
		CommandQuery: &types.SearchQuery{
			Labels: map[string][]string{qk: {qv}},
		},
	}
	gs.groups[entityID] = ent
	return ctx, seedGatewayEntity(w.Harness, ent)
}

// "group entity "X" also has label "A:B""
// Adds an own-label to an already-seeded gateway group entity.
func stepGroupEntityAlsoHasLabel(ctx context.Context, entityID, label string) (context.Context, error) {
	w := worldFrom(ctx)
	gs := getGroupsState(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid label %q", label)
	}
	ent, exists := gs.groups[entityID]
	if !exists {
		return ctx, fmt.Errorf("group entity %q not found in this scenario", entityID)
	}
	if ent.Labels == nil {
		ent.Labels = make(map[string][]string)
	}
	ent.Labels[k] = append(ent.Labels[k], v)
	gs.groups[entityID] = ent
	return ctx, seedGatewayEntity(w.Harness, ent)
}

// "entities "bulb-01,bulb-02,bulb-03" on plugin "X" have label "Room:Basement""
// Adds a label to listed entities on a plugin and re-seeds.
func stepEntitiesOnPluginHaveLabel(ctx context.Context, entityList, pluginID, label string) (context.Context, error) {
	w := worldFrom(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid label %q", label)
	}
	p := w.plugin(pluginID)
	ids := strings.Split(entityList, ",")
	for _, id := range ids {
		id = strings.TrimSpace(id)
		for i := range p.Entities {
			if p.Entities[i].ID == id {
				if p.Entities[i].Labels == nil {
					p.Entities[i].Labels = make(map[string][]string)
				}
				p.Entities[i].Labels[k] = append(p.Entities[i].Labels[k], v)
			}
		}
	}
	if err := p.SeedRegistry(); err != nil {
		return ctx, fmt.Errorf("re-seed registry after label update: %w", err)
	}
	return ctx, nil
}

// "plugin "X" also registers a group entity "Y" with domain "Z" and CommandQuery label "A:B""
// Adds a group entity to a running plugin and seeds it.
func stepPluginRegistersGroupEntity(ctx context.Context, pluginID, entityID, domain, label string) (context.Context, error) {
	w := worldFrom(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return ctx, fmt.Errorf("invalid CommandQuery label %q", label)
	}
	p := w.plugin(pluginID)
	// Use the first device as the owning device.
	deviceID := ""
	if len(p.Devices) > 0 {
		deviceID = p.Devices[0].ID
	}
	ent := types.Entity{
		ID:        entityID,
		PluginID:  pluginID,
		DeviceID:  deviceID,
		Domain:    domain,
		LocalName: entityID,
		CommandQuery: &types.SearchQuery{
			Labels: map[string][]string{k: {v}},
		},
	}
	p.Entities = append(p.Entities, ent)
	if err := p.SeedRegistry(); err != nil {
		return ctx, fmt.Errorf("seed registry for plugin %s: %w", pluginID, err)
	}
	return ctx, nil
}

// "I send command "turn_on" to group entity "basement-group""
func stepSendCommandToGroupEntity(ctx context.Context, action, entityID string) (context.Context, error) {
	w := worldFrom(ctx)
	gs := getGroupsState(ctx)
	path := fmt.Sprintf("/api/plugins/%s/devices/groups/entities/%s/commands",
		url.PathEscape(gatewayPluginID), url.PathEscape(entityID))
	if err := w.do(w.Harness.Post(path, map[string]any{"type": action})); err != nil {
		return ctx, err
	}
	var status types.CommandStatus
	if err := w.decodeLastBody(&status); err == nil {
		gs.lastCommandID = status.CommandID
		w.CommandID = status.CommandID
	}
	return ctx, nil
}

// "the group command state is "succeeded""
// Polls the gateway command status with a short deadline.
func stepGroupCommandStateIs(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	gs := getGroupsState(ctx)
	if gs.lastCommandID == "" {
		// Fall back to world's last command ID
		gs.lastCommandID = w.CommandID
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, ok := commandService.GetStatus(gs.lastCommandID)
		if ok && string(status.State) == want {
			return nil
		}
		if time.Now().After(deadline) {
			state := "unknown"
			if ok {
				state = string(status.State)
			}
			return fmt.Errorf("group command state = %q, expected %q", state, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// "all members with label "Room:Basement" received command "turn_on""
// Verifies that every entity with the given label in all plugins has at least
// one recorded command. The command name is used for documentation only.
func stepMembersWithLabelReceivedCommand(ctx context.Context, label, _ string) error {
	return waitForMembersWithLabel(ctx, label, 2*time.Second)
}

// "eventually all members with label "X" received command "Y""
// Same as above but with a longer wait (used when nested fan-out is expected).
func stepEventuallyMembersWithLabelReceivedCommand(ctx context.Context, label, _ string) error {
	return waitForMembersWithLabel(ctx, label, 3*time.Second)
}

func waitForMembersWithLabel(ctx context.Context, label string, timeout time.Duration) error {
	w := worldFrom(ctx)
	k, v, ok := parseLabel(label)
	if !ok {
		return fmt.Errorf("invalid label %q", label)
	}

	deadline := time.Now().Add(timeout)
	for {
		missing := findMissingCommandedEntities(w, k, v)
		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("entities with label %s never received a command: %v", label, missing)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// findMissingCommandedEntities returns IDs of entities with the given label
// that have NOT received any command yet.
func findMissingCommandedEntities(w *World, labelKey, labelVal string) []string {
	var missing []string
	for _, p := range w.Plugins {
		p.mu.RLock()
		entities := append([]types.Entity{}, p.Entities...)
		commands := make(map[string]types.CommandStatus, len(p.commands))
		for k, v := range p.commands {
			commands[k] = v
		}
		p.mu.RUnlock()

		for _, e := range entities {
			vals := e.Labels[labelKey]
			hasLabel := false
			for _, lv := range vals {
				if lv == labelVal {
					hasLabel = true
					break
				}
			}
			if !hasLabel {
				continue
			}
			// Check if any command targets this entity.
			found := false
			for _, cs := range commands {
				if cs.EntityID == e.ID {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, e.ID)
			}
		}
	}
	return missing
}

// "plugin "X" received the command for entity "Y""
func stepPluginReceivedCommandForEntity(ctx context.Context, pluginID, entityID string) error {
	w := worldFrom(ctx)
	p := w.plugin(pluginID)
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.RLock()
		found := false
		for _, cs := range p.commands {
			if cs.EntityID == entityID {
				found = true
				break
			}
		}
		p.mu.RUnlock()
		if found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("plugin %q never received a command for entity %q", pluginID, entityID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// "the entity results include "X""
func stepEntityResultsInclude(ctx context.Context, entityID string) error {
	w := worldFrom(ctx)
	for _, e := range w.Entities {
		if e.ID == entityID {
			return nil
		}
	}
	return fmt.Errorf("entity %q not found in results: %v", entityID, entityIDs(w.Entities))
}

// ---------------------------------------------------------------------------
// Step registration
// ---------------------------------------------------------------------------

func registerGroupSteps(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		ctx, _ = withGroupsState(ctx)
		return ctx, nil
	})

	// Group entity setup
	sc.Step(`^a group entity "([^"]*)" with domain "([^"]*)" and CommandQuery label "([^"]*)"$`,
		stepGroupEntityWithQueryLabel)
	sc.Step(`^a group entity "([^"]*)" with domain "([^"]*)" and CommandQuery label "([^"]*)" and label "([^"]*)"$`,
		stepGroupEntityWithQueryLabelAndOwnLabel)
	sc.Step(`^group entity "([^"]*)" also has label "([^"]*)"$`,
		stepGroupEntityAlsoHasLabel)
	sc.Step(`^entities "([^"]*)" on plugin "([^"]*)" have label "([^"]*)"$`,
		stepEntitiesOnPluginHaveLabel)
	sc.Step(`^plugin "([^"]*)" also registers a group entity "([^"]*)" with domain "([^"]*)" and CommandQuery label "([^"]*)"$`,
		stepPluginRegistersGroupEntity)

	// Command sending
	sc.Step(`^I send command "([^"]*)" to group entity "([^"]*)"$`,
		stepSendCommandToGroupEntity)

	// Assertions
	sc.Step(`^the group command state is "([^"]*)"$`,
		stepGroupCommandStateIs)
	sc.Step(`^all members with label "([^"]*)" received command "([^"]*)"$`,
		stepMembersWithLabelReceivedCommand)
	sc.Step(`^eventually all members with label "([^"]*)" received command "([^"]*)"$`,
		stepEventuallyMembersWithLabelReceivedCommand)
	sc.Step(`^plugin "([^"]*)" received the command for entity "([^"]*)"$`,
		stepPluginReceivedCommandForEntity)
	sc.Step(`^the entity results include "([^"]*)"$`,
		stepEntityResultsInclude)
}
