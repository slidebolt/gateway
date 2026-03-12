package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCommandService_SubmitEntityNotFound(t *testing.T) {
	// Use a minimal harness: just need nc, registryService, commandService initialized
	// We test that submitting to a non-existent entity returns errCommandTargetNotFound
	setupCommandServiceHarness(t)

	_, err := commandService.Submit("plugin-foo", "device-foo", "missing-entity", json.RawMessage(`{"type":"turn_on"}`))
	if err == nil {
		t.Fatal("expected error for missing entity")
	}
	if !errors.Is(err, errCommandTargetNotFound) {
		t.Fatalf("expected errCommandTargetNotFound, got %v", err)
	}
}
