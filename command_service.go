package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/slidebolt/sdk-types"
)

var errCommandTargetNotFound = errors.New("entity not found")

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

func (s *Command) Submit(pluginID, deviceID, entityID string, payload json.RawMessage) (types.CommandStatus, error) {
	started := time.Now()

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return types.CommandStatus{}, fmt.Errorf("command service is closed")
	}

	entityType, err := s.resolver.ResolveEntityType(pluginID, deviceID, entityID)
	if err != nil {
		return types.CommandStatus{}, err
	}

	now := time.Now().UTC()
	status := types.CommandStatus{
		CommandID:     nextID("gcmd"),
		PluginID:      pluginID,
		DeviceID:      deviceID,
		EntityID:      entityID,
		EntityType:    entityType,
		State:         types.CommandPending,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	log.Printf("gateway cmd: submit accepted command_id=%s owner=%s/%s/%s resolve_ms=%d", status.CommandID, pluginID, deviceID, entityID, time.Since(started).Milliseconds())

	s.updateStatus(status)

	job := commandJob{
		rootStatus: status,
		payload:    payload,
		target:     commandTarget{pluginID, deviceID, entityID},
		onComplete: s.updateStatus,
	}
	go s.dispatcher.Execute(job)
	return status, nil
}
