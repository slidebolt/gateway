package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

// TestConfig holds test configuration
type TestConfig struct {
	DataDir    string
	NATSServer string
	SQLitePath string
}

// NewTestConfig creates test configuration with temp directories
func NewTestConfig(t *testing.T) *TestConfig {
	t.Helper()

	dataDir := t.TempDir()

	return &TestConfig{
		DataDir:    dataDir,
		NATSServer: "nats://localhost:4223",
		SQLitePath: filepath.Join(dataDir, "test_history.db"),
	}
}

// Cleanup removes test data
func (tc *TestConfig) Cleanup() {
	os.RemoveAll(tc.DataDir)
}

// MockPlugin simulates a plugin for testing
type MockPlugin struct {
	ID      string
	Methods map[string]func(params json.RawMessage) (any, error)
	NC      *nats.Conn
	Sub     *nats.Subscription
}

// NewMockPlugin creates a mock plugin that listens for RPC calls
func NewMockPlugin(t *testing.T, nc *nats.Conn, id string) *MockPlugin {
	t.Helper()

	mp := &MockPlugin{
		ID:      id,
		Methods: make(map[string]func(params json.RawMessage) (any, error)),
		NC:      nc,
	}

	// Register default handlers
	mp.Methods["entities/list"] = func(params json.RawMessage) (any, error) {
		return []types.Entity{}, nil
	}

	mp.Methods["entities/get"] = func(params json.RawMessage) (any, error) {
		return types.Entity{}, nil
	}

	// Subscribe to RPC subject
	subject := fmt.Sprintf("plugins.%s.rpc", id)
	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var req types.Request
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			resp := types.Response{
				JSONRPC: types.JSONRPCVersion,
				Error:   &types.RPCError{Code: -32700, Message: "parse error"},
			}
			data, _ := json.Marshal(resp)
			msg.Respond(data)
			return
		}

		handler, ok := mp.Methods[req.Method]
		if !ok {
			resp := types.Response{
				JSONRPC: types.JSONRPCVersion,
				Error:   &types.RPCError{Code: -32601, Message: "method not found"},
			}
			if req.ID != nil {
				resp.ID = *req.ID
			}
			data, _ := json.Marshal(resp)
			msg.Respond(data)
			return
		}

		result, err := handler(req.Params)
		var resp types.Response
		if err != nil {
			resp = types.Response{
				JSONRPC: types.JSONRPCVersion,
				Error:   &types.RPCError{Code: -32000, Message: err.Error()},
			}
		} else {
			data, _ := json.Marshal(result)
			resp = types.Response{
				JSONRPC: types.JSONRPCVersion,
				Result:  data,
			}
		}
		if req.ID != nil {
			resp.ID = *req.ID
		}

		respData, _ := json.Marshal(resp)
		msg.Respond(respData)
	})

	if err != nil {
		t.Fatalf("Failed to subscribe mock plugin: %v", err)
	}

	mp.Sub = sub
	return mp
}

// Register registers the mock plugin with the gateway registry
func (mp *MockPlugin) Register() {
	manifest := types.Manifest{
		ID:      mp.ID,
		Name:    mp.ID,
		Version: "1.0.0",
	}
	reg := types.Registration{
		Manifest:   manifest,
		RPCSubject: fmt.Sprintf("plugins.%s.rpc", mp.ID),
	}

	regMu.Lock()
	registry[mp.ID] = pluginRecord{
		Registration: reg,
		Valid:        true,
	}
	regMu.Unlock()
}

// Unregister removes the mock plugin from registry
func (mp *MockPlugin) Unregister() {
	regMu.Lock()
	delete(registry, mp.ID)
	regMu.Unlock()

	if mp.Sub != nil {
		mp.Sub.Unsubscribe()
	}
}

// SetHandler sets a custom method handler
func (mp *MockPlugin) SetHandler(method string, handler func(params json.RawMessage) (any, error)) {
	mp.Methods[method] = handler
}

// MustMarshalJSON marshals to JSON or fails test
func MustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}
	return data
}

// AssertJSONEqual compares two JSON strings
func AssertJSONEqual(t *testing.T, expected, actual []byte) {
	t.Helper()

	var exp, act map[string]any
	if err := json.Unmarshal(expected, &exp); err != nil {
		t.Fatalf("Failed to unmarshal expected JSON: %v", err)
	}
	if err := json.Unmarshal(actual, &act); err != nil {
		t.Fatalf("Failed to unmarshal actual JSON: %v", err)
	}

	expStr, _ := json.MarshalIndent(exp, "", "  ")
	actStr, _ := json.MarshalIndent(act, "", "  ")

	if string(expStr) != string(actStr) {
		t.Errorf("JSON not equal:\nExpected:\n%s\nActual:\n%s", expStr, actStr)
	}
}

// WaitForCondition waits for a condition to be true with timeout
func WaitForCondition(t *testing.T, timeout time.Duration, condition func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// CreateTestDB creates an in-memory SQLite database for testing
func CreateTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

// ResetGlobals resets global state between tests
func ResetGlobals() {
	if commandService != nil {
		commandService.Close()
		commandService = nil
	}
	regMu.Lock()
	registry = make(map[string]pluginRecord)
	regMu.Unlock()
}

// setupCommandServiceHarness sets up nc, registryService, and commandService for command tests
func setupCommandServiceHarness(t *testing.T) {
	t.Helper()
	s, conn := setupTestServer(t)
	t.Cleanup(func() {
		teardownTestServer(t, s, conn)
	})
	nc = conn

	registryService = regsvc.RegistryService(
		"test-gateway",
		regsvc.WithNATS(conn),
		regsvc.WithAggregate(),
		regsvc.WithPersist(regsvc.PersistNever),
	)
	_ = registryService.LoadAll()
	_ = registryService.Start()
	t.Cleanup(func() { registryService.Stop() })

	commandService = CommandService()
	t.Cleanup(func() { commandService.Close() })

	ResetGlobals()
	// Re-register after reset
	nc = conn
	commandService = CommandService()
	t.Cleanup(func() { commandService.Close() })
}
