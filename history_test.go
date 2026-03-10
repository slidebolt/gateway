package main

import (
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

// TestHistoryStore_InsertAndRetrieveEvent tests event persistence
func TestHistoryStore_InsertAndRetrieveEvent(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert event
	env := types.EntityEventEnvelope{
		PluginID: "test-plugin",
		DeviceID: "device-1",
		EntityID: "entity-1",
		Payload:  []byte(`{"type":"state","on":true}`),
	}

	err = store.InsertEvent(1, time.Now().UTC(), env)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Retrieve events
	events, err := store.ListEvents("test-plugin", "device-1", "entity-1", 100)
	if err != nil {
		t.Fatalf("Failed to list events: %v", err)
	}

	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}

	if events[0].PluginID != "test-plugin" {
		t.Errorf("Expected plugin ID 'test-plugin', got %q", events[0].PluginID)
	}
}

// TestHistoryStore_InsertCommandStatus tests command status persistence
func TestHistoryStore_InsertCommandStatus(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	status := types.CommandStatus{
		CommandID:     "cmd-123",
		PluginID:      "test-plugin",
		DeviceID:      "device-1",
		EntityID:      "entity-1",
		State:         types.CommandPending,
		CreatedAt:     time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}

	err = store.InsertCommandStatus(1, status)
	if err != nil {
		t.Fatalf("Failed to insert command status: %v", err)
	}

	// Retrieve latest status
	latest, found, err := store.LatestCommandStatus("cmd-123")
	if err != nil {
		t.Fatalf("Failed to get latest command status: %v", err)
	}

	if !found {
		t.Fatal("Expected to find command status, got not found")
	}

	if latest.CommandID != "cmd-123" {
		t.Errorf("Expected command ID 'cmd-123', got %q", latest.CommandID)
	}

	if latest.State != types.CommandPending {
		t.Errorf("Expected state PENDING, got %v", latest.State)
	}
}

// TestHistoryStore_LatestCommandStatus_NotFound tests when command doesn't exist
func TestHistoryStore_LatestCommandStatus_NotFound(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	_, found, err := store.LatestCommandStatus("nonexistent-cmd")
	if err != nil {
		t.Fatalf("Failed to query command status: %v", err)
	}

	if found {
		t.Error("Expected not found for nonexistent command")
	}
}

// TestHistoryStore_ListEvents_Limit tests pagination
func TestHistoryStore_ListEvents_Limit(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert multiple events
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		store.InsertEvent(uint64(i+1), now.Add(time.Duration(i)*time.Second), env)
	}

	// Query with limit
	events, err := store.ListEvents("test-plugin", "device-1", "entity-1", 5)
	if err != nil {
		t.Fatalf("Failed to list events: %v", err)
	}

	if len(events) != 5 {
		t.Errorf("Expected 5 events (limit), got %d", len(events))
	}
}

// TestHistoryStore_Stats tests statistics calculation
func TestHistoryStore_Stats(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert events and commands
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		store.InsertEvent(uint64(i+1), now, env)
	}

	for i := 0; i < 5; i++ {
		status := types.CommandStatus{
			CommandID:     nextID("cmd"),
			PluginID:      "test-plugin",
			DeviceID:      "device-1",
			EntityID:      "entity-1",
			State:         types.CommandSucceeded,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}
		store.InsertCommandStatus(uint64(i+100), status)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.EventCount != 10 {
		t.Errorf("Expected 10 events, got %d", stats.EventCount)
	}

	if stats.CommandCount != 5 {
		t.Errorf("Expected 5 commands, got %d", stats.CommandCount)
	}
}

// TestHistoryStore_PluginRates tests rate calculation for plugins
func TestHistoryStore_PluginRates(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert 60 events over 60 seconds
	now := time.Now().UTC()
	for i := 0; i < 60; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.InsertEvent(uint64(i+1), ts, env)
	}

	rates, err := store.PluginRates(60)
	if err != nil {
		t.Fatalf("Failed to get plugin rates: %v", err)
	}

	if len(rates) != 1 {
		t.Fatalf("Expected 1 plugin rate entry, got %d", len(rates))
	}

	// Rate should be approximately 1.0 events/sec
	if rates[0].EventsPerSec < 0.9 || rates[0].EventsPerSec > 1.1 {
		t.Errorf("Expected rate ~1.0, got %f", rates[0].EventsPerSec)
	}

	if rates[0].PluginID != "test-plugin" {
		t.Errorf("Expected plugin ID 'test-plugin', got %q", rates[0].PluginID)
	}
}

// TestHistoryStore_DeviceRates tests rate calculation for devices
func TestHistoryStore_DeviceRates(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert events for multiple devices
	now := time.Now().UTC()
	for i := 0; i < 30; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.InsertEvent(uint64(i+1), ts, env)
	}

	for i := 0; i < 30; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-2",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.InsertEvent(uint64(i+100), ts, env)
	}

	rates, err := store.DeviceRates("test-plugin", 60)
	if err != nil {
		t.Fatalf("Failed to get device rates: %v", err)
	}

	if len(rates) != 2 {
		t.Errorf("Expected 2 device rate entries, got %d", len(rates))
	}
}

// TestHistoryStore_EntityRates tests rate calculation for entities
func TestHistoryStore_EntityRates(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	// Insert events for multiple entities
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.InsertEvent(uint64(i+1), ts, env)
	}

	for i := 0; i < 20; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-2",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.InsertEvent(uint64(i+100), ts, env)
	}

	rates, err := store.EntityRates("test-plugin", "device-1", 60)
	if err != nil {
		t.Fatalf("Failed to get entity rates: %v", err)
	}

	if len(rates) != 2 {
		t.Errorf("Expected 2 entity rate entries, got %d", len(rates))
	}
}

// TestHistoryStore_ConcurrentAccess tests concurrent read/write
func TestHistoryStore_ConcurrentAccess(t *testing.T) {
	config := NewTestConfig(t)
	defer config.Cleanup()

	store, err := openHistoryStore(config.SQLitePath)
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	defer store.Close()

	done := make(chan bool)

	// Writer goroutines
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 20; j++ {
				env := types.EntityEventEnvelope{
					PluginID: "test-plugin",
					DeviceID: "device-1",
					EntityID: "entity-1",
					Payload:  []byte(`{"type":"state"}`),
				}
				store.InsertEvent(uint64(id*100+j), time.Now().UTC(), env)
			}
			done <- true
		}(i)
	}

	// Reader goroutines
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				store.ListEvents("test-plugin", "device-1", "entity-1", 100)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all events were inserted
	stats, _ := store.Stats()
	if stats.EventCount != 100 {
		t.Errorf("Expected 100 events after concurrent writes, got %d", stats.EventCount)
	}
}
