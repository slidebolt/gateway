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

func parseLabels(pairs []string) map[string][]string {
	if len(pairs) == 0 {
		return nil
	}
	labels := make(map[string][]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			labels[k] = append(labels[k], v)
		}
	}
	return labels
}

func saveDeviceToRegistry(raw json.RawMessage, fallback types.Device) {
	if registryService == nil {
		return
	}
	var dev types.Device
	if len(raw) > 0 && json.Unmarshal(raw, &dev) == nil && strings.TrimSpace(dev.ID) != "" {
		_ = registryService.SaveDevice(dev)
		return
	}
	if strings.TrimSpace(fallback.ID) != "" {
		_ = registryService.SaveDevice(fallback)
	}
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

type entityWithPlugin struct {
	types.Entity
	PluginID string `json:"plugin_id"`
}

func performEntitySearch(query types.SearchQuery) []entityWithPlugin {
	f := regsvc.Filter{
		Pattern:  query.Pattern,
		Labels:   query.Labels,
		PluginID: query.PluginID,
		DeviceID: query.DeviceID,
		EntityID: query.EntityID,
		Domain:   query.Domain,
		Limit:    query.Limit,
	}
	registryResults, err := regsvc.QueryService(registryService).System().FindEntities(f)
	if err != nil {
		return nil
	}

	results := make([]entityWithPlugin, 0, len(registryResults))
	for _, r := range registryResults {
		results = append(results, entityWithPlugin{
			Entity:   r.Entity,
			PluginID: r.PluginID,
		})
	}
	return results
}
