package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/slidebolt/sdk-types"
)

func parseQueryString(raw string) (types.SearchQuery, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return types.SearchQuery{}, fmt.Errorf("query is required")
	}
	if !strings.HasPrefix(s, "?") {
		s = "?" + s
	}
	u, err := url.Parse("http://localhost" + s)
	if err != nil {
		return types.SearchQuery{}, fmt.Errorf("invalid query: %w", err)
	}
	q := u.Query()
	pattern := strings.TrimSpace(firstValue(q, "q", "pattern"))
	if pattern == "" {
		pattern = "*"
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(firstValue(q, "limit")))
	return types.SearchQuery{
		Pattern:  pattern,
		Labels:   parseLabels(append(q["label"], q["Label"]...)),
		PluginID: strings.TrimSpace(firstValue(q, "plugin_id", "PluginID")),
		DeviceID: strings.TrimSpace(firstValue(q, "device_id", "DeviceID")),
		EntityID: strings.TrimSpace(firstValue(q, "entity_id", "EntityID")),
		Domain:   strings.TrimSpace(firstValue(q, "domain", "Domain")),
		Limit:    limit,
	}, nil
}

func firstValue(q url.Values, keys ...string) string {
	for _, k := range keys {
		if v := q.Get(k); v != "" {
			return v
		}
	}
	return ""
}

func projectionQueryForDevice(pluginID, deviceID string) (types.SearchQuery, bool, error) {
	dev, err := findDevice(pluginID, deviceID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "device not found") {
			return types.SearchQuery{}, false, nil
		}
		return types.SearchQuery{}, false, err
	}
	if strings.TrimSpace(dev.EntityQuery) == "" {
		return types.SearchQuery{}, false, nil
	}
	q, err := parseQueryString(dev.EntityQuery)
	if err != nil {
		return types.SearchQuery{}, true, err
	}
	return q, true, nil
}

func projectedEntityResults(pluginID, deviceID string) ([]entityWithPlugin, bool, error) {
	q, ok, err := projectionQueryForDevice(pluginID, deviceID)
	if err != nil || !ok {
		return nil, ok, err
	}
	return performEntitySearch(q), true, nil
}

func projectedEntitiesForDevice(pluginID, deviceID string) ([]types.Entity, bool, error) {
	results, ok, err := projectedEntityResults(pluginID, deviceID)
	if err != nil || !ok {
		return nil, ok, err
	}
	out := make([]types.Entity, 0, len(results))
	for _, r := range results {
		e := r.Entity
		e.DeviceID = deviceID
		out = append(out, e)
	}
	return out, true, nil
}

func resolveProjectedEntity(pluginID, deviceID, entityID string) (entityWithPlugin, bool, error) {
	results, ok, err := projectedEntityResults(pluginID, deviceID)
	if err != nil || !ok {
		return entityWithPlugin{}, ok, err
	}
	for _, r := range results {
		if r.ID == entityID {
			return r, true, nil
		}
	}
	return entityWithPlugin{}, true, nil
}

func sendCommandToAddress(pluginID, deviceID, entityID string, payload json.RawMessage) (types.CommandStatus, error) {
	return sendCommandToAddressRecursive(pluginID, deviceID, entityID, payload, map[string]bool{})
}

func sendCommandToAddressRecursive(pluginID, deviceID, entityID string, payload json.RawMessage, visited map[string]bool) (types.CommandStatus, error) {
	key := entityKey(pluginID, deviceID, entityID)
	if visited[key] {
		return types.CommandStatus{}, fmt.Errorf("circular virtual reference detected")
	}
	visited[key] = true
	defer delete(visited, key)

	vstore.mu.RLock()
	vrec, isVirtual := vstore.entities[key]
	vstore.mu.RUnlock()

	if isVirtual {
		actionType, err := parseActionType(payload)
		if err != nil {
			return types.CommandStatus{}, err
		}
		if len(vrec.Entity.Actions) > 0 && !containsAction(vrec.Entity.Actions, actionType) {
			return types.CommandStatus{}, fmt.Errorf("action %q not supported by this virtual entity", actionType)
		}
		if strings.TrimSpace(vrec.SourceQuery) != "" {
			return sendCommandToGroupVirtual(vrec, key, payload, visited)
		}
		return sendCommandToMirrorVirtual(vrec, key, payload, visited)
	}

	params := map[string]any{"device_id": deviceID, "entity_id": entityID, "payload": payload}
	resp := routeRPC(pluginID, "entities/commands/create", params)
	if resp.Error != nil {
		return types.CommandStatus{}, errors.New(resp.Error.Message)
	}
	var status types.CommandStatus
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		return types.CommandStatus{}, fmt.Errorf("invalid command status")
	}
	if status.CommandID != "" {
		_ = history.StoreCommandPayload(status.CommandID, payload)
	}
	return status, nil
}

func sendCommandToMirrorVirtual(vrec virtualEntityRecord, virtualKey string, payload json.RawMessage, visited map[string]bool) (types.CommandStatus, error) {
	sourceStatus, err := sendCommandToAddressRecursive(vrec.SourcePluginID, vrec.SourceDeviceID, vrec.SourceEntityID, payload, visited)
	if err != nil {
		return types.CommandStatus{}, err
	}
	now := time.Now().UTC()
	virtualCID := nextID("vcmd")
	status := types.CommandStatus{
		CommandID: virtualCID, PluginID: vrec.OwnerPluginID, DeviceID: vrec.OwnerDeviceID, EntityID: vrec.Entity.ID,
		EntityType: vrec.Entity.Domain, State: types.CommandPending, CreatedAt: now, LastUpdatedAt: now,
	}
	vstore.mu.Lock()
	vstore.commands[virtualCID] = virtualCommandRecord{
		OwnerPluginID: vrec.OwnerPluginID, SourcePluginID: vrec.SourcePluginID,
		SourceCommand: sourceStatus.CommandID, VirtualKey: virtualKey, Status: status,
		Downstream: []virtualDownstreamCommand{{
			PluginID: vrec.SourcePluginID, DeviceID: vrec.SourceDeviceID, EntityID: vrec.SourceEntityID, CommandID: sourceStatus.CommandID,
		}},
	}
	vrec.Entity.Data.LastCommandID = virtualCID
	vrec.Entity.Data.SyncStatus = types.SyncStatusPending
	vrec.Entity.Data.UpdatedAt = now
	vstore.entities[virtualKey] = vrec
	vstore.persistLocked()
	vstore.mu.Unlock()
	storeVirtualCommandStatus(status)
	_ = history.StoreCommandPayload(virtualCID, payload)
	go monitorVirtualCommand(virtualCID)
	return status, nil
}

func sendCommandToGroupVirtual(vrec virtualEntityRecord, virtualKey string, payload json.RawMessage, visited map[string]bool) (types.CommandStatus, error) {
	query, err := parseQueryString(vrec.SourceQuery)
	if err != nil {
		return types.CommandStatus{}, err
	}
	results := performEntitySearch(query)
	downstream := make([]virtualDownstreamCommand, 0, len(results))
	for _, match := range results {
		childKey := entityKey(match.PluginID, match.DeviceID, match.ID)
		if childKey == virtualKey {
			continue
		}
		status, err := sendCommandToAddressRecursive(match.PluginID, match.DeviceID, match.ID, payload, visited)
		if err != nil {
			continue
		}
		downstream = append(downstream, virtualDownstreamCommand{
			PluginID:  status.PluginID,
			DeviceID:  status.DeviceID,
			EntityID:  status.EntityID,
			CommandID: status.CommandID,
			State:     status.State,
			Error:     status.Error,
		})
	}
	if len(downstream) == 0 {
		return types.CommandStatus{}, fmt.Errorf("query-backed group has no routable members")
	}

	now := time.Now().UTC()
	virtualCID := nextID("vcmd")
	status := types.CommandStatus{
		CommandID:     virtualCID,
		PluginID:      vrec.OwnerPluginID,
		DeviceID:      vrec.OwnerDeviceID,
		EntityID:      vrec.Entity.ID,
		EntityType:    vrec.Entity.Domain,
		State:         types.CommandPending,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	vstore.mu.Lock()
	vstore.commands[virtualCID] = virtualCommandRecord{
		OwnerPluginID: vrec.OwnerPluginID,
		VirtualKey:    virtualKey,
		Status:        status,
		Downstream:    downstream,
	}
	vrec.Entity.Data.LastCommandID = virtualCID
	vrec.Entity.Data.SyncStatus = types.SyncStatusPending
	vrec.Entity.Data.UpdatedAt = now
	vstore.entities[virtualKey] = vrec
	vstore.persistLocked()
	vstore.mu.Unlock()
	storeVirtualCommandStatus(status)
	_ = history.StoreCommandPayload(virtualCID, payload)
	go monitorVirtualCommand(virtualCID)
	return status, nil
}

func broadcastProjectedMirrors(env types.EntityEventEnvelope) {
	if !hasProjectionDevices.Load() {
		return
	}

	regMu.RLock()
	pluginIDs := make([]string, 0, len(registry))
	for id, rec := range registry {
		if rec.Valid {
			pluginIDs = append(pluginIDs, id)
		}
	}
	regMu.RUnlock()

	for _, ownerPluginID := range pluginIDs {
		resp := routeRPC(ownerPluginID, "devices/list", nil)
		devices, err := parseDevices(resp)
		if err != nil {
			continue
		}
		for _, dev := range devices {
			if strings.TrimSpace(dev.EntityQuery) == "" {
				continue
			}
			query, err := parseQueryString(dev.EntityQuery)
			if err != nil {
				continue
			}
			matches := performEntitySearch(query)
			if !containsSearchMatch(matches, env.PluginID, env.DeviceID, env.EntityID) {
				continue
			}
			if ownerPluginID == env.PluginID && dev.ID == env.DeviceID {
				continue
			}
			broker.broadcast(sseMessage{Type: "entity", PluginID: ownerPluginID, DeviceID: dev.ID, EntityID: env.EntityID})
		}
	}
}

func containsSearchMatch(matches []entityWithPlugin, pluginID, deviceID, entityID string) bool {
	for _, m := range matches {
		if m.PluginID == pluginID && m.DeviceID == deviceID && m.ID == entityID {
			return true
		}
	}
	return false
}
