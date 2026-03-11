package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

// setupTestVirtualStore creates a virtual store in temp directory
func setupTestVirtualStore(t *testing.T) (*virtualStore, string) {
	t.Helper()

	dataDir := t.TempDir()
	store := loadVirtualStore(dataDir)

	return store, dataDir
}

// TestVirtualStore_LoadAndPersist tests loading from disk and persisting
func TestVirtualStore_LoadAndPersist(t *testing.T) {
	dataDir := t.TempDir()

	// Pre-populate with entity file
	entity := virtualEntityRecord{
		OwnerPluginID:  "owner-1",
		OwnerDeviceID:  "device-1",
		SourcePluginID: "source-1",
		SourceDeviceID: "source-device-1",
		SourceEntityID: "source-entity-1",
		MirrorSource:   true,
		Entity: types.Entity{
			ID:        "virtual-entity-1",
			DeviceID:  "device-1",
			LocalName: "Test Virtual Entity",
			Domain:    "test",
		},
	}

	entities := map[string]virtualEntityRecord{
		"owner-1|device-1|virtual-entity-1": entity,
	}

	entsData, _ := json.MarshalIndent(entities, "", "  ")
	os.WriteFile(filepath.Join(dataDir, "virtual_entities.json"), entsData, 0644)

	// Load store
	store := loadVirtualStore(dataDir)

	// Verify entity loaded
	key := entityKey("owner-1", "device-1", "virtual-entity-1")
	store.mu.RLock()
	loadedEntity, exists := store.entities[key]
	store.mu.RUnlock()

	if !exists {
		t.Fatal("Expected entity to be loaded from file")
	}

	if loadedEntity.Entity.LocalName != "Test Virtual Entity" {
		t.Errorf("Expected entity name 'Test Virtual Entity', got %q", loadedEntity.Entity.LocalName)
	}
}

// TestVirtualStore_ConcurrentAccess tests thread safety
func TestVirtualStore_ConcurrentAccess(t *testing.T) {
	store, _ := setupTestVirtualStore(t)

	var wg sync.WaitGroup
	numGoroutines := 50
	numOperations := 20

	// Writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := entityKey("owner-1", "device-1", "entity-1")
				store.mu.Lock()
				store.entities[key] = virtualEntityRecord{
					OwnerPluginID: "owner-1",
					OwnerDeviceID: "device-1",
					Entity: types.Entity{
						ID:        "entity-1",
						LocalName: "Entity 1",
					},
				}
				store.mu.Unlock()
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := entityKey("owner-1", "device-1", "entity-1")
				store.mu.RLock()
				_ = store.entities[key]
				store.mu.RUnlock()
			}
		}()
	}

	wg.Wait()

	// If we got here without deadlock or panic, test passes
	t.Log("Concurrent access completed without race conditions")
}

// TestVirtualStore_Persistence tests that changes are persisted to disk
func TestVirtualStore_Persistence(t *testing.T) {
	store, dataDir := setupTestVirtualStore(t)

	// Add entity
	key := entityKey("owner-1", "device-1", "entity-1")
	store.mu.Lock()
	store.entities[key] = virtualEntityRecord{
		OwnerPluginID:  "owner-1",
		OwnerDeviceID:  "device-1",
		SourcePluginID: "source-1",
		SourceDeviceID: "source-device-1",
		SourceEntityID: "source-entity-1",
		MirrorSource:   true,
		Entity: types.Entity{
			ID:        "entity-1",
			DeviceID:  "device-1",
			LocalName: "Persisted Entity",
			Domain:    "test",
		},
	}
	store.persistLocked()
	store.mu.Unlock()

	// Verify file exists
	entitiesFile := filepath.Join(dataDir, "virtual_entities.json")
	if _, err := os.Stat(entitiesFile); os.IsNotExist(err) {
		t.Fatal("Expected entities file to be created")
	}

	// Read and verify content
	data, err := os.ReadFile(entitiesFile)
	if err != nil {
		t.Fatalf("Failed to read entities file: %v", err)
	}

	var loaded map[string]virtualEntityRecord
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Failed to unmarshal entities: %v", err)
	}

	entity, exists := loaded[key]
	if !exists {
		t.Fatal("Expected entity to be persisted")
	}

	if entity.Entity.LocalName != "Persisted Entity" {
		t.Errorf("Expected name 'Persisted Entity', got %q", entity.Entity.LocalName)
	}
}

// TestVirtualStore_EmptyDataDir tests loading from empty directory
func TestVirtualStore_EmptyDataDir(t *testing.T) {
	dataDir := t.TempDir()
	store := loadVirtualStore(dataDir)

	store.mu.RLock()
	entityCount := len(store.entities)
	commandCount := len(store.commands)
	store.mu.RUnlock()

	if entityCount != 0 {
		t.Errorf("Expected 0 entities, got %d", entityCount)
	}

	if commandCount != 0 {
		t.Errorf("Expected 0 commands, got %d", commandCount)
	}
}

// TestVirtualStore_CommandRecords tests command record operations
func TestVirtualStore_CommandRecords(t *testing.T) {
	store, _ := setupTestVirtualStore(t)

	status := types.CommandStatus{
		CommandID:     "cmd-123",
		PluginID:      "owner-1",
		DeviceID:      "device-1",
		EntityID:      "entity-1",
		State:         types.CommandPending,
		CreatedAt:     time.Now().UTC(),
		LastUpdatedAt: time.Now().UTC(),
	}

	cmdRec := virtualCommandRecord{
		OwnerPluginID:  "owner-1",
		SourcePluginID: "source-1",
		SourceCommand:  "source-cmd-456",
		VirtualKey:     "owner-1|device-1|entity-1",
		Status:         status,
	}

	store.mu.Lock()
	store.commands["cmd-123"] = cmdRec
	store.persistLocked()
	store.mu.Unlock()

	// Verify command stored
	store.mu.RLock()
	storedCmd, exists := store.commands["cmd-123"]
	store.mu.RUnlock()

	if !exists {
		t.Fatal("Expected command to be stored")
	}

	if storedCmd.Status.CommandID != "cmd-123" {
		t.Errorf("Expected command ID 'cmd-123', got %q", storedCmd.Status.CommandID)
	}

	if storedCmd.Status.State != types.CommandPending {
		t.Errorf("Expected state PENDING, got %v", storedCmd.Status.State)
	}
}

// TestVirtualStore_MirrorSourceTrue tests mirrored entity behavior
func TestVirtualStore_MirrorSourceTrue(t *testing.T) {
	store, _ := setupTestVirtualStore(t)

	entity := virtualEntityRecord{
		OwnerPluginID:  "owner-1",
		OwnerDeviceID:  "device-1",
		SourcePluginID: "source-1",
		SourceDeviceID: "source-device-1",
		SourceEntityID: "source-entity-1",
		MirrorSource:   true,
		Entity: types.Entity{
			ID:        "virtual-entity-1",
			DeviceID:  "device-1",
			LocalName: "Mirrored Entity",
			Domain:    "test",
		},
	}

	key := entityKey("owner-1", "device-1", "virtual-entity-1")
	store.mu.Lock()
	store.entities[key] = entity
	store.mu.Unlock()

	// Verify mirror_source flag
	store.mu.RLock()
	stored, _ := store.entities[key]
	store.mu.RUnlock()

	if !stored.MirrorSource {
		t.Error("Expected MirrorSource to be true")
	}

	if stored.SourcePluginID != "source-1" {
		t.Errorf("Expected source plugin ID 'source-1', got %q", stored.SourcePluginID)
	}
}

// TestVirtualStore_EntityKey tests key generation
func TestVirtualStore_EntityKey(t *testing.T) {
	key := entityKey("plugin-1", "device-1", "entity-1")
	expected := "plugin-1|device-1|entity-1"

	if key != expected {
		t.Errorf("Expected key %q, got %q", expected, key)
	}
}

// TestVirtualStore_VirtualFile tests file path generation
func TestVirtualStore_VirtualFile(t *testing.T) {
	dataDir := "/tmp/test-data"
	path := virtualFile(dataDir, "test.json")
	expected := filepath.Join(dataDir, "test.json")

	if path != expected {
		t.Errorf("Expected path %q, got %q", expected, path)
	}
}

// TestVirtualStore_CorruptedFile tests handling of corrupted JSON files
func TestVirtualStore_CorruptedFile(t *testing.T) {
	dataDir := t.TempDir()

	// Write corrupted JSON
	os.WriteFile(filepath.Join(dataDir, "virtual_entities.json"), []byte("{invalid json}"), 0644)

	// Should not panic
	store := loadVirtualStore(dataDir)

	store.mu.RLock()
	count := len(store.entities)
	store.mu.RUnlock()

	// Should start with empty map if file is corrupted
	if count != 0 {
		t.Errorf("Expected 0 entities with corrupted file, got %d", count)
	}
}

// BenchmarkVirtualStore_ConcurrentAccess benchmarks concurrent operations
func BenchmarkVirtualStore_ConcurrentAccess(b *testing.B) {
	store, _ := setupTestVirtualStore(&testing.T{})

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := entityKey("owner-1", "device-1", "entity-1")

			if i%2 == 0 {
				// Write
				store.mu.Lock()
				store.entities[key] = virtualEntityRecord{
					OwnerPluginID: "owner-1",
					Entity: types.Entity{
						ID:        "entity-1",
						LocalName: "Entity",
					},
				}
				store.mu.Unlock()
			} else {
				// Read
				store.mu.RLock()
				_ = store.entities[key]
				store.mu.RUnlock()
			}
			i++
		}
	})
}
