package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

// setupTestServer creates an embedded NATS server for testing
func setupTestServer(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()

	opts := server.Options{
		Port: -1, // Random port
	}

	s, err := server.NewServer(&opts)
	if err != nil {
		t.Fatalf("Failed to create NATS server: %v", err)
	}

	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server failed to start")
	}

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatalf("Failed to connect to NATS: %v", err)
	}

	return s, nc
}

// teardownTestServer shuts down NATS server and connection
func teardownTestServer(t *testing.T, s *server.Server, nc *nats.Conn) {
	t.Helper()
	if nc != nil {
		nc.Close()
	}
	if s != nil {
		s.Shutdown()
	}
}

// TestRouteRPC_Success tests successful RPC call to plugin
func TestRouteRPC_Success(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	// Create and register mock plugin
	plugin := NewMockPlugin(t, conn, "test-plugin")
	plugin.Register()
	defer plugin.Unregister()

	// Set up entity response
	expectedEntity := types.Entity{
		ID:        "entity-1",
		DeviceID:  "device-1",
		LocalName: "Test Entity",
		Domain:    "test",
	}
	plugin.SetHandler("entities/get", func(params json.RawMessage) (any, error) {
		return expectedEntity, nil
	})

	// Call routeRPC
	params := map[string]any{
		"id":        "entity-1",
		"device_id": "device-1",
	}
	resp := routeRPC("test-plugin", "entities/get", params)

	// Assert
	if resp.Error != nil {
		t.Errorf("Expected no error, got: %v", resp.Error)
	}

	if resp.Result == nil {
		t.Fatal("Expected result, got nil")
	}

	var entity types.Entity
	if err := json.Unmarshal(resp.Result, &entity); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if entity.ID != expectedEntity.ID {
		t.Errorf("Expected entity ID %q, got %q", expectedEntity.ID, entity.ID)
	}

	if entity.LocalName != expectedEntity.LocalName {
		t.Errorf("Expected entity name %q, got %q", expectedEntity.LocalName, entity.LocalName)
	}
}

// TestRouteRPC_PluginNotRegistered tests call to unregistered plugin
func TestRouteRPC_PluginNotRegistered(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	// Clear registry
	ResetGlobals()

	// Call routeRPC with non-existent plugin
	resp := routeRPC("nonexistent-plugin", "entities/get", nil)

	// Assert
	if resp.Error == nil {
		t.Fatal("Expected error for unregistered plugin, got nil")
	}

	if resp.Error.Code != -32000 {
		t.Errorf("Expected error code -32000, got %d", resp.Error.Code)
	}

	if resp.Error.Message != "plugin not registered" {
		t.Errorf("Expected 'plugin not registered', got %q", resp.Error.Message)
	}
}

// TestRouteRPC_PluginTimeout tests timeout when plugin doesn't respond
func TestRouteRPC_PluginTimeout(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	// Create plugin but don't set up any handlers (so it won't respond)
	plugin := NewMockPlugin(t, conn, "slow-plugin")
	plugin.Register()

	// Override the subscription to do nothing (simulating slow plugin)
	plugin.Sub.Unsubscribe()

	// Call routeRPC with short timeout context
	resp := routeRPC("slow-plugin", "entities/get", nil)

	// Assert
	if resp.Error == nil {
		t.Fatal("Expected timeout error, got nil")
	}

	if resp.Error.Code != -32000 {
		t.Errorf("Expected error code -32000, got %d", resp.Error.Code)
	}

	if resp.Error.Message != "plugin timeout" {
		t.Errorf("Expected 'plugin timeout', got %q", resp.Error.Message)
	}
}

// TestRouteRPC_PluginError tests when plugin returns RPC error
func TestRouteRPC_PluginError(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	plugin := NewMockPlugin(t, conn, "error-plugin")
	plugin.Register()
	defer plugin.Unregister()

	// Set up handler that returns error
	plugin.SetHandler("entities/get", func(params json.RawMessage) (any, error) {
		return nil, errors.New("entity not found")
	})

	resp := routeRPC("error-plugin", "entities/get", map[string]any{"id": "missing"})

	if resp.Error == nil {
		t.Fatal("Expected error, got nil")
	}

	if resp.Error.Code != -32000 {
		t.Errorf("Expected error code -32000, got %d", resp.Error.Code)
	}

	if resp.Error.Message != "entity not found" {
		t.Errorf("Expected 'entity not found', got %q", resp.Error.Message)
	}
}

// TestRouteRPC_MalformedResponse tests handling of invalid JSON response
func TestRouteRPC_MalformedResponse(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	subj := "plugins.bad-plugin.rpc"
	sub, err := conn.Subscribe(subj, func(msg *nats.Msg) {
		_ = msg.Respond([]byte("{not-json"))
	})
	if err != nil {
		t.Fatalf("Failed to subscribe malformed responder: %v", err)
	}
	defer sub.Unsubscribe()

	regMu.Lock()
	registry["bad-plugin"] = pluginRecord{
		Registration: types.Registration{
			Manifest: types.Manifest{
				ID:      "bad-plugin",
				Name:    "bad-plugin",
				Version: "1.0.0",
			},
			RPCSubject: subj,
		},
		Valid: true,
	}
	regMu.Unlock()
	defer ResetGlobals()

	resp := routeRPC("bad-plugin", "entities/get", nil)

	if resp.Error == nil {
		t.Fatal("Expected error for malformed response, got nil")
	}

	if resp.Error.Code != -32700 {
		t.Errorf("Expected error code -32700 (parse error), got %d", resp.Error.Code)
	}

	if resp.Error.Message != "malformed response from plugin" {
		t.Errorf("Expected 'malformed response from plugin', got %q", resp.Error.Message)
	}
}

// TestParseEntities_Success tests parsing entities from response
func TestParseEntities_Success(t *testing.T) {
	entities := []types.Entity{
		{ID: "entity-1", DeviceID: "device-1", LocalName: "Entity 1"},
		{ID: "entity-2", DeviceID: "device-1", LocalName: "Entity 2"},
	}

	result, _ := json.Marshal(entities)
	resp := types.Response{
		JSONRPC: types.JSONRPCVersion,
		Result:  result,
	}

	parsed, err := parseEntities(resp)
	if err != nil {
		t.Fatalf("Failed to parse entities: %v", err)
	}

	if len(parsed) != 2 {
		t.Errorf("Expected 2 entities, got %d", len(parsed))
	}

	if parsed[0].ID != "entity-1" {
		t.Errorf("Expected first entity ID 'entity-1', got %q", parsed[0].ID)
	}
}

// TestParseEntities_Error tests parsing when response contains error
func TestParseEntities_Error(t *testing.T) {
	resp := types.Response{
		JSONRPC: types.JSONRPCVersion,
		Error:   &types.RPCError{Code: -32000, Message: "database error"},
	}

	_, err := parseEntities(resp)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != "database error" {
		t.Errorf("Expected 'database error', got %q", err.Error())
	}
}

// TestFindEntity_Success tests finding specific entity
func TestFindEntity_Success(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	plugin := NewMockPlugin(t, conn, "test-plugin")
	plugin.Register()
	defer plugin.Unregister()

	expectedEntity := types.Entity{
		ID:        "target-entity",
		DeviceID:  "device-1",
		LocalName: "Target Entity",
	}

	plugin.SetHandler("entities/list", func(params json.RawMessage) (any, error) {
		return []types.Entity{expectedEntity}, nil
	})

	entity, err := findEntity("test-plugin", "device-1", "target-entity")
	if err != nil {
		t.Fatalf("Failed to find entity: %v", err)
	}

	if entity.ID != "target-entity" {
		t.Errorf("Expected entity ID 'target-entity', got %q", entity.ID)
	}
}

// TestFindEntity_NotFound tests when entity doesn't exist
func TestFindEntity_NotFound(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	plugin := NewMockPlugin(t, conn, "test-plugin")
	plugin.Register()
	defer plugin.Unregister()

	plugin.SetHandler("entities/list", func(params json.RawMessage) (any, error) {
		return []types.Entity{}, nil
	})

	_, err := findEntity("test-plugin", "device-1", "missing-entity")
	if err == nil {
		t.Fatal("Expected error for missing entity, got nil")
	}

	if err.Error() != "entity not found" {
		t.Errorf("Expected 'entity not found', got %q", err.Error())
	}
}

// TestFetchCommandStatus_Success tests fetching command status
func TestFetchCommandStatus_Success(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	plugin := NewMockPlugin(t, conn, "test-plugin")
	plugin.Register()
	defer plugin.Unregister()

	expectedStatus := types.CommandStatus{
		CommandID: "cmd-123",
		State:     types.CommandSucceeded,
	}

	plugin.SetHandler("commands/status/get", func(params json.RawMessage) (any, error) {
		return expectedStatus, nil
	})

	status, err := fetchCommandStatus("test-plugin", "cmd-123")
	if err != nil {
		t.Fatalf("Failed to fetch command status: %v", err)
	}

	if status.CommandID != "cmd-123" {
		t.Errorf("Expected command ID 'cmd-123', got %q", status.CommandID)
	}

	if status.State != types.CommandSucceeded {
		t.Errorf("Expected state SUCCEEDED, got %v", status.State)
	}
}

// TestFetchCommandStatus_Error tests error handling
func TestFetchCommandStatus_Error(t *testing.T) {
	s, conn := setupTestServer(t)
	defer teardownTestServer(t, s, conn)
	nc = conn

	plugin := NewMockPlugin(t, conn, "test-plugin")
	plugin.Register()
	defer plugin.Unregister()

	plugin.SetHandler("commands/status/get", func(params json.RawMessage) (any, error) {
		return nil, errors.New("command not found")
	})

	_, err := fetchCommandStatus("test-plugin", "missing-cmd")
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != "command not found" {
		t.Errorf("Expected 'command not found', got %q", err.Error())
	}
}

// TestParseActionType_Success tests extracting action type from payload
func TestParseActionType_Success(t *testing.T) {
	payload := map[string]any{
		"type":       "turn_on",
		"brightness": 100,
	}

	action, err := parseActionType(MustMarshalJSON(t, payload))
	if err != nil {
		t.Fatalf("Failed to parse action type: %v", err)
	}

	if action != "turn_on" {
		t.Errorf("Expected action 'turn_on', got %q", action)
	}
}

// TestParseActionType_MissingField tests when type field is missing
func TestParseActionType_MissingField(t *testing.T) {
	payload := map[string]any{
		"brightness": 100,
	}

	_, err := parseActionType(MustMarshalJSON(t, payload))
	if err == nil {
		t.Fatal("Expected error for missing type field, got nil")
	}
}

// TestContainsAction_True tests when action exists in list
func TestContainsAction_True(t *testing.T) {
	actions := []string{"turn_on", "turn_off", "set_brightness"}
	if !containsAction(actions, "turn_off") {
		t.Error("Expected containsAction to return true for 'turn_off'")
	}
}

// TestContainsAction_False tests when action doesn't exist
func TestContainsAction_False(t *testing.T) {
	actions := []string{"turn_on", "turn_off"}
	if containsAction(actions, "set_color") {
		t.Error("Expected containsAction to return false for 'set_color'")
	}
}
