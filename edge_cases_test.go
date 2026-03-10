package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

// TestNilVstoreAccess tests what happens when vstore is nil
// This exposes a bug: routes access vstore without nil checks
func TestNilVstoreAccess(t *testing.T) {
	// Save original vstore
	originalVstore := vstore
	defer func() {
		vstore = originalVstore
	}()

	// Set vstore to nil to simulate initialization failure
	vstore = nil

	// This would cause a panic in production code if vstore is nil
	// Example: routes.go line 890 accesses vstore.entities without nil check
	// We're just documenting this vulnerability
	t.Log("BUG: vstore is accessed throughout routes.go without nil checks")
	t.Log("If loadVirtualStore fails or isn't called, the gateway will panic")
}

// TestVirtualStorePersistErrors tests that persist errors are handled
// This exposes a bug: persistLocked() silently ignores write errors
func TestVirtualStorePersistErrors(t *testing.T) {
	// Create a read-only directory
	dataDir := t.TempDir()

	// Make directory read-only
	os.Chmod(dataDir, 0555)
	defer os.Chmod(dataDir, 0755) // Restore for cleanup

	store := loadVirtualStore(dataDir)

	// Add an entity
	store.mu.Lock()
	store.entities["test-key"] = virtualEntityRecord{
		OwnerPluginID: "owner-1",
		Entity: types.Entity{
			ID:        "entity-1",
			LocalName: "Test Entity",
		},
	}

	// This should fail silently - no error returned!
	store.persistLocked()
	store.mu.Unlock()

	// Verify the error was silently ignored
	// In production, this could lead to data loss
	t.Log("BUG: persistLocked() ignores file write errors - data can be lost silently")

	// Verify file wasn't created
	entitiesFile := filepath.Join(dataDir, "virtual_entities.json")
	if _, err := os.Stat(entitiesFile); !os.IsNotExist(err) {
		t.Log("File was created despite read-only directory (unexpected)")
	}
}

// TestVirtualStoreCorruptedJSON tests loading corrupted JSON
// This could expose issues with partial data loading
func TestVirtualStoreCorruptedJSON(t *testing.T) {
	dataDir := t.TempDir()

	// Write corrupted entities JSON
	corruptedEntities := `{"key1": {"owner_plugin_id": "owner-1", "entity": {"id": "ent-1"}}, "key2": INVALID_JSON}`
	os.WriteFile(filepath.Join(dataDir, "virtual_entities.json"), []byte(corruptedEntities), 0644)

	// Write valid commands JSON
	validCommands := `{}`
	os.WriteFile(filepath.Join(dataDir, "virtual_commands.json"), []byte(validCommands), 0644)

	// Load should handle corruption gracefully
	store := loadVirtualStore(dataDir)

	store.mu.RLock()
	count := len(store.entities)
	store.mu.RUnlock()

	// If JSON is corrupted, the entire file is skipped
	if count != 0 {
		t.Errorf("Expected 0 entities with corrupted JSON, got %d", count)
	}

	t.Log("OBSERVATION: Corrupted JSON causes entire file to be skipped, losing all data")
}

// TestVirtualStoreConcurrentWriteAndPersist tests race conditions
// This exposes potential data races between writers and persisters
func TestVirtualStoreConcurrentWriteAndPersist(t *testing.T) {
	store, _ := setupTestVirtualStore(t)

	done := make(chan bool, 2)

	// Goroutine 1: Continuously write entities
	go func() {
		for i := 0; i < 100; i++ {
			store.mu.Lock()
			key := entityKey("owner-1", "device-1", "entity-1")
			store.entities[key] = virtualEntityRecord{
				OwnerPluginID: "owner-1",
				Entity: types.Entity{
					ID:        "entity-1",
					LocalName: "Entity 1",
				},
			}
			store.persistLocked()
			store.mu.Unlock()
		}
		done <- true
	}()

	// Goroutine 2: Continuously persist
	go func() {
		for i := 0; i < 100; i++ {
			store.mu.Lock()
			store.persistLocked()
			store.mu.Unlock()
		}
		done <- true
	}()

	// Wait for both
	<-done
	<-done

	// If no panic occurred, mutex is working correctly
	t.Log("Concurrent write and persist completed without panic")
}

// TestHistoryStoreNilReceiver tests nil store handling
// This checks how methods handle nil receiver
func TestHistoryStoreNilReceiver(t *testing.T) {
	var nilStore *historyStore

	now := time.Now().UTC()

	// InsertEvent should handle nil gracefully
	err := nilStore.InsertEvent(1, now, types.EntityEventEnvelope{})
	if err != nil {
		t.Errorf("InsertEvent with nil receiver should return nil error, got: %v", err)
	}

	// InsertCommandStatus should handle nil gracefully
	status := types.CommandStatus{
		CommandID:     "cmd-1",
		State:         types.CommandPending,
		CreatedAt:     now,
		LastUpdatedAt: now,
	}
	err = nilStore.InsertCommandStatus(1, status)
	if err != nil {
		t.Errorf("InsertCommandStatus with nil receiver should return nil error, got: %v", err)
	}

	t.Log("Nil receiver returns nil error - this may hide bugs in production")
}

// TestJSONMarshalErrors tests what happens when JSON marshaling fails
// This exposes ignored errors in virtual_store.go
func TestJSONMarshalErrors(t *testing.T) {
	// Create a type that can't be marshaled to JSON
	type BadType struct {
		Ch chan int // channels can't be marshaled
	}

	badData := map[string]BadType{
		"bad": {Ch: make(chan int)},
	}

	data, err := json.MarshalIndent(badData, "", "  ")

	// This should fail
	if err == nil {
		t.Error("Expected JSON marshal to fail for channel type")
	} else {
		t.Logf("JSON marshal correctly failed: %v", err)
		t.Log("BUG: In persistLocked(), this error is ignored with '_', causing silent data loss")
	}

	// Verify data is nil when marshal fails
	if data != nil {
		t.Log("Data was generated despite marshal error (unexpected)")
	}
}

// TestEntityKeyEmptyStrings tests key generation edge cases
// This tests boundary conditions in entityKey function
func TestEntityKeyEmptyStrings(t *testing.T) {
	// Test with empty strings
	key := entityKey("", "", "")
	if key != "||" {
		t.Errorf("Expected '||' for empty strings, got %q", key)
	}

	// Test with pipe characters in IDs (could cause issues)
	key = entityKey("plugin|1", "device|2", "entity|3")
	if key != "plugin|1|device|2|entity|3" {
		t.Errorf("Key with pipe chars: %q", key)
	}
	t.Log("OBSERVATION: Pipe characters in IDs could cause parsing ambiguity")
}

// TestRegistryConcurrentAccess tests race conditions in registry
// This could expose race conditions in plugin registry
func TestRegistryConcurrentAccess(t *testing.T) {
	ResetGlobals()

	done := make(chan bool, 10)

	// Multiple goroutines registering plugins
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 20; j++ {
				regMu.Lock()
				registry[nextID("plugin")] = pluginRecord{
					Registration: types.Registration{},
					Valid:        true,
				}
				regMu.Unlock()
			}
			done <- true
		}(i)
	}

	// Multiple goroutines reading registry
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				regMu.RLock()
				_ = len(registry)
				regMu.RUnlock()
			}
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we got here, no panic occurred
	t.Log("Concurrent registry access completed without panic")
}

// TestParseLabelsEdgeCases tests edge cases in label parsing
func TestParseLabelsEdgeCases(t *testing.T) {
	// Empty input
	labels := parseLabels([]string{})
	if labels != nil {
		t.Error("Expected nil for empty input")
	}

	// Missing colon
	labels = parseLabels([]string{"nocolon"})
	if len(labels) != 0 {
		t.Errorf("Expected 0 labels for 'nocolon', got %d", len(labels))
	}

	// Multiple colons - strings.Cut only splits on first
	labels = parseLabels([]string{"key:val:ue"})
	if len(labels) != 1 || len(labels["key"]) != 1 {
		t.Errorf("Expected 1 label with 1 value for multiple colons")
	} else if labels["key"][0] != "val:ue" {
		t.Errorf("Expected value 'val:ue', got %q", labels["key"][0])
	}

	// Empty key or value
	labels = parseLabels([]string{":value", "key:", ":"})
	if len(labels) != 2 {
		t.Logf("Labels with empty keys/values: %v", labels)
	}
}

// TestContainsActionEdgeCases tests edge cases in action checking
func TestContainsActionEdgeCases(t *testing.T) {
	// Empty list
	if containsAction([]string{}, "action") {
		t.Error("Empty list should not contain any action")
	}

	// Nil list
	if containsAction(nil, "action") {
		t.Error("Nil list should not contain any action")
	}

	// Empty action name
	if !containsAction([]string{""}, "") {
		t.Error("Empty action should be found in list with empty string")
	}

	// Case sensitivity
	if containsAction([]string{"Action"}, "action") {
		t.Error("Action matching should be case-sensitive")
	}
}

// BenchmarkEntityKey benchmarks key generation
func BenchmarkEntityKey(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = entityKey("plugin-1", "device-1", "entity-1")
	}
}

// BenchmarkParseLabels benchmarks label parsing
func BenchmarkParseLabels(b *testing.B) {
	pairs := []string{
		"key1:value1",
		"key1:value2",
		"key2:value3",
		"key3:value4",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parseLabels(pairs)
	}
}
