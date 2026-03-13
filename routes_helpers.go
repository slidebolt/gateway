package main

import (
	"encoding/json"
	"strings"

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
	DomainSchema *types.DomainDescriptor         `json:"schema,omitempty"`
}

func toEntityResponse(e types.Entity) EntityResponse {
	r := EntityResponse{
		ID:         e.ID,
		SourceID:   e.SourceID,
		SourceName: e.SourceName,
		DeviceID:   e.DeviceID,
		Domain:     e.Domain,
		LocalName:  e.LocalName,
		Actions:    e.Actions,
		Data:       e.Data,
		Labels:     e.Labels,
		Snapshots:  e.Snapshots,
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

func saveDeviceToRegistry(raw json.RawMessage, fallback types.Device) {
	if registryService == nil {
		return
	}
	var dev types.Device
	if len(raw) > 0 && json.Unmarshal(raw, &dev) == nil && strings.TrimSpace(dev.ID) != "" {
		// Preserve gateway-managed fields not returned by the plugin.
		if dev.EntityQuery == nil {
			dev.EntityQuery = fallback.EntityQuery
		}
		_ = registryService.SaveDevice(dev)
		return
	}
	if strings.TrimSpace(fallback.ID) != "" {
		_ = registryService.SaveDevice(fallback)
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

func saveEntityToRegistry(fallbackDeviceID string, raw json.RawMessage, fallback types.Entity) {
	if registryService == nil {
		return
	}
	var ent types.Entity
	if len(raw) > 0 && json.Unmarshal(raw, &ent) == nil && strings.TrimSpace(ent.ID) != "" {
		if strings.TrimSpace(ent.DeviceID) == "" {
			ent.DeviceID = fallbackDeviceID
		}
		if strings.TrimSpace(ent.DeviceID) != "" {
			_ = registryService.SaveEntity(ent)
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
	_ = registryService.SaveEntity(fallback)
}

func performEntitySearch(query types.SearchQuery) []types.Entity {
	return registryService.FindEntities(query)
}
