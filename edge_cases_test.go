package main

import (
	"testing"

	"github.com/slidebolt/sdk-types"

	"github.com/slidebolt/gateway/internal/history"
)

// TestHistoryNilReceiver tests that *history.History nil receiver is safe on public methods.
func TestHistoryNilReceiver(t *testing.T) {
	var h *history.History

	if err := h.Close(); err != nil {
		t.Errorf("Close on nil History should return nil, got: %v", err)
	}

	if err := h.Prune(); err != nil {
		t.Errorf("Prune on nil History should return nil, got: %v", err)
	}

	t.Log("Nil receiver returns nil error for safe public methods")
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
