package history

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

func openTestStore(t *testing.T) *History {
	t.Helper()
	dir := t.TempDir()
	h, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open history store: %v", err)
	}
	t.Cleanup(func() {
		h.Close()
		os.RemoveAll(dir)
	})
	return h
}

func TestHistoryStore_InsertAndRetrieveEvent(t *testing.T) {
	store := openTestStore(t)

	env := types.EntityEventEnvelope{
		PluginID: "test-plugin",
		DeviceID: "device-1",
		EntityID: "entity-1",
		Payload:  []byte(`{"type":"state","on":true}`),
	}

	if err := store.insertEvent(1, time.Now().UTC(), env); err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	events, err := store.listEvents("test-plugin", "device-1", "entity-1", 100)
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

func TestHistoryStore_InsertCommandStatus(t *testing.T) {
	store := openTestStore(t)

	status := types.CommandStatus{
		CommandID:     "cmd-123",
		PluginID:      "test-plugin",
		DeviceID:      "device-1",
		EntityID:      "entity-1",
		State:         types.CommandPending,
		CreatedAt:     time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}

	if err := store.insertCommandStatus(1, status); err != nil {
		t.Fatalf("Failed to insert command status: %v", err)
	}

	latest, found, err := store.latestCommandStatus("cmd-123")
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

func TestHistoryStore_LatestCommandStatus_NotFound(t *testing.T) {
	store := openTestStore(t)

	_, found, err := store.latestCommandStatus("nonexistent-cmd")
	if err != nil {
		t.Fatalf("Failed to query command status: %v", err)
	}

	if found {
		t.Error("Expected not found for nonexistent command")
	}
}

func TestHistoryStore_ListEvents_Limit(t *testing.T) {
	store := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		store.insertEvent(uint64(i+1), now.Add(time.Duration(i)*time.Second), env)
	}

	events, err := store.listEvents("test-plugin", "device-1", "entity-1", 5)
	if err != nil {
		t.Fatalf("Failed to list events: %v", err)
	}

	if len(events) != 5 {
		t.Errorf("Expected 5 events (limit), got %d", len(events))
	}
}

func TestHistoryStore_Stats(t *testing.T) {
	store := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		store.insertEvent(uint64(i+1), now, env)
	}

	for i := 0; i < 5; i++ {
		status := types.CommandStatus{
			CommandID:     fmt.Sprintf("cmd-%d", i),
			PluginID:      "test-plugin",
			DeviceID:      "device-1",
			EntityID:      "entity-1",
			State:         types.CommandSucceeded,
			CreatedAt:     now,
			LastUpdatedAt: now,
		}
		store.insertCommandStatus(uint64(i+100), status)
	}

	s, err := store.stats()
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if s.EventCount != 10 {
		t.Errorf("Expected 10 events, got %d", s.EventCount)
	}

	if s.CommandCount != 5 {
		t.Errorf("Expected 5 commands, got %d", s.CommandCount)
	}
}

func TestHistoryStore_PluginRates(t *testing.T) {
	store := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 60; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.insertEvent(uint64(i+1), ts, env)
	}

	rates, err := store.pluginRates(60)
	if err != nil {
		t.Fatalf("Failed to get plugin rates: %v", err)
	}

	if len(rates) != 1 {
		t.Fatalf("Expected 1 plugin rate entry, got %d", len(rates))
	}

	if rates[0].EventsPerSec < 0.9 || rates[0].EventsPerSec > 1.1 {
		t.Errorf("Expected rate ~1.0, got %f", rates[0].EventsPerSec)
	}

	if rates[0].PluginID != "test-plugin" {
		t.Errorf("Expected plugin ID 'test-plugin', got %q", rates[0].PluginID)
	}
}

func TestHistoryStore_DeviceRates(t *testing.T) {
	store := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 30; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.insertEvent(uint64(i+1), ts, env)
	}

	for i := 0; i < 30; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-2",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.insertEvent(uint64(i+100), ts, env)
	}

	rates, err := store.deviceRates("test-plugin", 60)
	if err != nil {
		t.Fatalf("Failed to get device rates: %v", err)
	}

	if len(rates) != 2 {
		t.Errorf("Expected 2 device rate entries, got %d", len(rates))
	}
}

func TestHistoryStore_EntityRates(t *testing.T) {
	store := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-1",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.insertEvent(uint64(i+1), ts, env)
	}

	for i := 0; i < 20; i++ {
		env := types.EntityEventEnvelope{
			PluginID: "test-plugin",
			DeviceID: "device-1",
			EntityID: "entity-2",
			Payload:  []byte(`{"type":"state"}`),
		}
		ts := now.Add(time.Duration(-i) * time.Second)
		store.insertEvent(uint64(i+100), ts, env)
	}

	rates, err := store.entityRates("test-plugin", "device-1", 60)
	if err != nil {
		t.Fatalf("Failed to get entity rates: %v", err)
	}

	if len(rates) != 2 {
		t.Errorf("Expected 2 entity rate entries, got %d", len(rates))
	}
}

func TestHistoryStore_ConcurrentAccess(t *testing.T) {
	store := openTestStore(t)

	done := make(chan bool)

	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 20; j++ {
				env := types.EntityEventEnvelope{
					PluginID: "test-plugin",
					DeviceID: "device-1",
					EntityID: "entity-1",
					Payload:  []byte(`{"type":"state"}`),
				}
				store.insertEvent(uint64(id*100+j), time.Now().UTC(), env)
			}
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				store.listEvents("test-plugin", "device-1", "entity-1", 100)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	s, _ := store.stats()
	if s.EventCount != 100 {
		t.Errorf("Expected 100 events after concurrent writes, got %d", s.EventCount)
	}
}
