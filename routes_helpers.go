package main

import (
	"encoding/json"
	"strings"

	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

// EntityResponse is an explicit (non-embedding) struct used in API outputs.
// Using embedding + a field named "Schema" triggers a reflect.StructOf duplicate
// field panic in Huma's OpenAPI schema generator.
type EntityResponse struct {
	ID           string                          `json:"id"`
	SourceID     string                          `json:"source_id,omitempty"`
	SourceName   string                          `json:"source_name,omitempty"`
	DeviceID     string                          `json:"device_id"`
	Domain       string                          `json:"domain"`
	LocalName    string                          `json:"local_name"`
	Actions      []string                        `json:"actions,omitempty"`
	Data         types.EntityData                `json:"data"`
	Labels       map[string][]string             `json:"labels,omitempty"`
	Snapshots    map[string]types.EntitySnapshot `json:"snapshots,omitempty"`
	CommandQuery *types.SearchQuery              `json:"command_query,omitempty"`
	Meta         map[string]json.RawMessage      `json:"meta,omitempty"`
	DomainSchema *types.DomainDescriptor         `json:"schema,omitempty"`
}

func toEntityResponse(e types.Entity) EntityResponse {
	r := EntityResponse{
		ID:           e.ID,
		SourceID:     e.SourceID,
		SourceName:   e.SourceName,
		DeviceID:     e.DeviceID,
		Domain:       e.Domain,
		LocalName:    e.LocalName,
		Actions:      e.Actions,
		Data:         e.Data,
		Labels:       e.Labels,
		Snapshots:    e.Snapshots,
		CommandQuery: e.CommandQuery,
		Meta:         e.Meta,
	}
	if desc, ok := types.GetDomainDescriptor(e.Domain); ok {
		filtered := filterDescriptor(desc, e.Actions)
		r.DomainSchema = &filtered
	}
	return r
}

func withSchema(entities []types.Entity) []EntityResponse {
	out := make([]EntityResponse, len(entities))
	for i, e := range entities {
		out[i] = toEntityResponse(e)
	}
	return out
}

func filterDescriptor(desc types.DomainDescriptor, actions []string) types.DomainDescriptor {
	if len(actions) == 0 {
		return desc
	}
	allowed := make(map[string]bool, len(actions))
	for _, a := range actions {
		allowed[a] = true
	}
	filtered := types.DomainDescriptor{Domain: desc.Domain}
	for _, cmd := range desc.Commands {
		if allowed[cmd.Action] {
			filtered.Commands = append(filtered.Commands, cmd)
		}
	}
	for _, evt := range desc.Events {
		if allowed[evt.Action] {
			filtered.Events = append(filtered.Events, evt)
		}
	}
	return filtered
}

func pluginRegistryForSave(pluginID string) *regsvc.Registry {
	if strings.TrimSpace(pluginID) == "" || nc == nil {
		return nil
	}
	return regsvc.RegistryService(pluginID, regsvc.WithNATS(nc), regsvc.WithPersist(regsvc.PersistNever))
}

func saveDeviceToRegistry(pluginID string, raw json.RawMessage, fallback types.Device) {
	reg := pluginRegistryForSave(pluginID)
	if reg == nil {
		return
	}
	if err := reg.Start(); err != nil {
		return
	}
	defer reg.Stop()

	var dev types.Device
	if len(raw) > 0 && json.Unmarshal(raw, &dev) == nil && strings.TrimSpace(dev.ID) != "" {
		// Preserve gateway-managed fields not returned by the plugin.
		if dev.EntityQuery == nil {
			dev.EntityQuery = fallback.EntityQuery
		}
		_ = reg.SaveDevice(dev)
		return
	}
	if strings.TrimSpace(fallback.ID) != "" {
		_ = reg.SaveDevice(fallback)
	}
}

// augmentWithEntityQuery adds entities matching a device's EntityQuery to base,
// deduplicating by entity ID. Returns base unchanged if the device has no
// EntityQuery or cannot be found in the local registry.
func augmentWithEntityQuery(deviceID string, base []types.Entity) []types.Entity {
	if registryService == nil {
		return base
	}
	dev, ok := registryService.LoadDevice(deviceID)
	if !ok || dev.EntityQuery == nil {
		return base
	}
	extra := performEntitySearch(*dev.EntityQuery)
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	for _, e := range base {
		seen[e.ID] = true
	}
	for _, e := range extra {
		if !seen[e.ID] {
			seen[e.ID] = true
			base = append(base, e)
		}
	}
	return base
}

func saveEntityToRegistry(pluginID, fallbackDeviceID string, raw json.RawMessage, fallback types.Entity) {
	reg := pluginRegistryForSave(pluginID)
	if reg == nil {
		return
	}
	if err := reg.Start(); err != nil {
		return
	}
	defer reg.Stop()

	var ent types.Entity
	if len(raw) > 0 && json.Unmarshal(raw, &ent) == nil && strings.TrimSpace(ent.ID) != "" {
		if strings.TrimSpace(ent.DeviceID) == "" {
			ent.DeviceID = fallbackDeviceID
		}
		if strings.TrimSpace(ent.DeviceID) != "" {
			// Preserve gateway-managed meta when the incoming entity doesn't carry it.
			if ent.Meta == nil && registryService != nil {
				if hits := registryService.FindEntities(types.SearchQuery{
					PluginID: pluginID, EntityID: ent.ID, DeviceID: ent.DeviceID, Limit: 1,
				}); len(hits) > 0 {
					ent.Meta = hits[0].Meta
				}
			}
			_ = reg.SaveEntity(ent)
			return
		}
	}
	if strings.TrimSpace(fallback.ID) == "" {
		return
	}
	if strings.TrimSpace(fallback.DeviceID) == "" {
		fallback.DeviceID = fallbackDeviceID
	}
	if strings.TrimSpace(fallback.DeviceID) == "" {
		return
	}
	// Preserve gateway-managed meta in the fallback path too.
	if fallback.Meta == nil && registryService != nil {
		if hits := registryService.FindEntities(types.SearchQuery{
			PluginID: pluginID, EntityID: fallback.ID, DeviceID: fallback.DeviceID, Limit: 1,
		}); len(hits) > 0 {
			fallback.Meta = hits[0].Meta
		}
	}
	_ = reg.SaveEntity(fallback)
}

func performEntitySearch(query types.SearchQuery) []types.Entity {
	return registryService.FindEntities(query)
}
