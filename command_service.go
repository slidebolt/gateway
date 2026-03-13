package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/slidebolt/sdk-types"
)

// gatewayPluginID is the sentinel PluginID for gateway-owned entities with no
// backing hardware.
const gatewayPluginID = "__gateway__"

var errCommandTargetNotFound = errors.New("entity not found")

func entityVisitKey(pluginID, deviceID, entityID string) string {
	return pluginID + "\x00" + deviceID + "\x00" + entityID
}

type commandTarget struct {
	PluginID string
	DeviceID string
	EntityID string
}

type commandJob struct {
	rootStatus types.CommandStatus
	payload    json.RawMessage
	target     commandTarget
	onComplete func(types.CommandStatus)
}

type Command struct {
	mu         sync.RWMutex
	statuses   map[string]types.CommandStatus
	closed     bool
	resolver   *Resolver
	dispatcher *Dispatcher
}

func CommandService() *Command {
	return &Command{
		statuses:   make(map[string]types.CommandStatus),
		resolver:   AddressResolver(),
		dispatcher: CommandDispatcher(),
	}
}

func (s *Command) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *Command) IsGatewayCommand(commandID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.statuses[commandID]
	return ok
}

func (s *Command) GetStatus(commandID string) (types.CommandStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status, ok := s.statuses[commandID]
	return status, ok
}

func (s *Command) updateStatus(status types.CommandStatus) {
	s.mu.Lock()
	s.statuses[status.CommandID] = status
	s.mu.Unlock()
	publishCommandStatus(status)
}

func publishCommandStatus(status types.CommandStatus) {
	data, _ := json.Marshal(status)
	_ = nc.Publish(types.SubjectCommandStatus, data)
}

// Submit validates the target entity and queues a command for dispatch.
// If the entity has a CommandQuery, the command fans out to all matching
// entities (recursively, with cycle detection) on a single background goroutine.
func (s *Command) Submit(pluginID, deviceID, entityID string, payload json.RawMessage) (types.CommandStatus, error) {
	started := time.Now()

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return types.CommandStatus{}, fmt.Errorf("command service is closed")
	}

	// Extract the action type from the payload for pre-flight validation.
	var cmd struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(payload, &cmd)

	ent, err := s.resolver.ResolveEntity(pluginID, deviceID, entityID, cmd.Type)
	if err != nil {
		return types.CommandStatus{}, err
	}

	now := time.Now().UTC()
	status := types.CommandStatus{
		CommandID:     nextID("gcmd"),
		PluginID:      pluginID,
		DeviceID:      deviceID,
		EntityID:      entityID,
		EntityType:    ent.Domain,
		State:         types.CommandPending,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	slog.Info("submit accepted", "command_id", status.CommandID, "plugin_id", pluginID, "device_id", deviceID, "entity_id", entityID, "resolve_ms", time.Since(started).Milliseconds())

	s.updateStatus(status)
	if scriptRuntime != nil {
		scriptRuntime.NotifyCommand(pluginID, deviceID, entityID, payload)
	}

	if ent.CommandQuery != nil {
		// Query-backed group entity: the entire fan-out tree runs on a single
		// goroutine so the visited set is never touched concurrently (no races, no
		// new goroutines spawned inside the tree). Group entities are virtual
		// routers and can belong to any plugin; they do not receive an additional
		// direct plugin RPC after fan-out.
		go func() {
			visited := map[string]bool{entityVisitKey(pluginID, deviceID, entityID): true}
			s.fanOut(visited, *ent.CommandQuery, payload)
			status.State = types.CommandSucceeded
			status.LastUpdatedAt = time.Now().UTC()
			s.updateStatus(status)
		}()
		return status, nil
	}

	job := commandJob{
		rootStatus: status,
		payload:    payload,
		target:     commandTarget{pluginID, deviceID, entityID},
		onComplete: s.updateStatus,
	}
	go s.dispatcher.Execute(job)
	return status, nil
}

// fanOut resolves all entities matching query and dispatches the payload to each,
// skipping any entity already in visited (cycle detection). It runs synchronously
// on the caller's goroutine so the visited map is never accessed concurrently.
func (s *Command) fanOut(visited map[string]bool, query types.SearchQuery, payload json.RawMessage) {
	entities := performEntitySearch(query)
	for _, ent := range entities {
		key := entityVisitKey(ent.PluginID, ent.DeviceID, ent.ID)
		if visited[key] {
			continue
		}
		visited[key] = true
		s.dispatchFanOutEntity(visited, ent, payload)
	}
}

// dispatchFanOutEntity handles a single entity during fan-out. If the entity is
// itself a group it recurses synchronously before dispatching to its plugin.
func (s *Command) dispatchFanOutEntity(visited map[string]bool, ent types.Entity, payload json.RawMessage) {
	action, err := parseActionType(payload)
	if err == nil && ent.CommandQuery == nil && len(ent.Actions) > 0 && !containsAction(ent.Actions, action) {
		slog.Debug("fan-out skipping unsupported leaf action", "plugin_id", ent.PluginID, "device_id", ent.DeviceID, "entity_id", ent.ID, "action", action)
		return
	}

	now := time.Now().UTC()
	status := types.CommandStatus{
		CommandID:     nextID("gcmd"),
		PluginID:      ent.PluginID,
		DeviceID:      ent.DeviceID,
		EntityID:      ent.ID,
		EntityType:    ent.Domain,
		State:         types.CommandPending,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	s.updateStatus(status)
	if scriptRuntime != nil {
		scriptRuntime.NotifyCommand(ent.PluginID, ent.DeviceID, ent.ID, payload)
	}

	if ent.CommandQuery != nil {
		// Nested query-backed group: recurse synchronously (same goroutine, shared
		// visited set), then mark the virtual group command complete.
		s.fanOut(visited, *ent.CommandQuery, payload)
		status.State = types.CommandSucceeded
		status.LastUpdatedAt = time.Now().UTC()
		s.updateStatus(status)
		return
	}

	if !isGatewayOwned(ent.PluginID) {
		job := commandJob{
			rootStatus: status,
			payload:    payload,
			target:     commandTarget{ent.PluginID, ent.DeviceID, ent.ID},
			onComplete: s.updateStatus,
		}
		s.dispatcher.Execute(job)
	} else {
		slog.Debug("fan-out skipping gateway-owned leaf", "entity_id", ent.ID)
	}
}

// isGatewayOwned reports whether an entity is owned by the gateway itself
// (no backing plugin on NATS).
func isGatewayOwned(pluginID string) bool {
	return pluginID == "" || pluginID == gatewayPluginID
}
