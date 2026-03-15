//go:build integration

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

// findEntitiesPlugin is a minimal plugin that seeds entities on Initialize
// and exposes its PluginContext so the test can call FindEntities directly.
type findEntitiesPlugin struct {
	id  string
	ctx runner.PluginContext

	// seedDeviceID / seedEntityID / seedDomain are set before Initialize runs.
	seedDeviceID string
	seedEntityID string
	seedDomain   string
}

func (p *findEntitiesPlugin) Initialize(ctx runner.PluginContext) (types.Manifest, error) {
	p.ctx = ctx
	if p.seedDeviceID != "" {
		_ = ctx.Registry.SaveDevice(types.Device{ID: p.seedDeviceID, SourceID: p.seedDeviceID, LocalName: p.seedDeviceID})
		_ = ctx.Registry.SaveEntity(types.Entity{ID: p.seedEntityID, DeviceID: p.seedDeviceID, Domain: p.seedDomain})
	}
	return types.Manifest{ID: p.id, Name: p.id, Version: "1.0.0", Schemas: types.CoreDomains()}, nil
}
func (p *findEntitiesPlugin) Start(_ context.Context) error          { return nil }
func (p *findEntitiesPlugin) Stop() error                            { return nil }
func (p *findEntitiesPlugin) OnReset() error                         { return nil }
func (p *findEntitiesPlugin) OnCommand(types.Command, types.Entity) error { return nil }

// startFindEntitiesPlugin boots a runner-backed plugin in a goroutine and
// waits for it to appear in the gateway's plugin list.
func startFindEntitiesPlugin(t *testing.T, h *apiHarness, p *findEntitiesPlugin) {
	t.Helper()
	t.Setenv(types.EnvNATSURL, h.NC.ConnectedUrl())
	t.Setenv(types.EnvPluginRPCSbj, types.SubjectRPCPrefix+p.id)
	t.Setenv(types.EnvPluginDataDir, filepath.Join(t.TempDir(), p.id))

	r, err := runner.NewRunner(p)
	if err != nil {
		t.Fatalf("NewRunner(%s): %v", p.id, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- r.RunContext(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil && err != context.Canceled {
				t.Errorf("runner %s shutdown: %v", p.id, err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("runner %s did not stop in time", p.id)
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("runner %s exited early: %v", p.id, err)
		default:
		}
		var plugins map[string]types.Registration
		resp, err := h.Get("/api/plugins")
		if err == nil {
			_ = ReadJSON(resp, &plugins)
			if _, ok := plugins[p.id]; ok {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("plugin %q did not register with gateway", p.id)
}

// TestIntegration_FindEntities_CrossPlugin verifies that a runner-backed plugin
// can call pctx.Registry.FindEntities and see entities owned by a different plugin.
//
// This exercises the full AttachNATS → NATS routing → gateway aggregate path.
func TestIntegration_FindEntities_CrossPlugin(t *testing.T) {
	h := newAPIHarness(t)

	pluginA := &findEntitiesPlugin{
		id:           "find-plugin-a",
		seedDeviceID: "dev-a",
		seedEntityID: "ent-a1",
		seedDomain:   "light",
	}
	pluginB := &findEntitiesPlugin{id: "find-plugin-b"}

	startFindEntitiesPlugin(t, h, pluginA)
	startFindEntitiesPlugin(t, h, pluginB)

	// Plugin B queries for all "light" entities system-wide.
	// It should find plugin A's entity even though it lives in a different plugin.
	var found []types.Entity
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		found = pluginB.ctx.Registry.FindEntities(types.SearchQuery{Domain: "light"})
		if len(found) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if len(found) == 0 {
		t.Fatal("FindEntities(domain=light): plugin-b got no results, expected plugin-a's entity")
	}

	got := found[0]
	if got.ID != "ent-a1" {
		t.Errorf("entity ID: got %q, want %q", got.ID, "ent-a1")
	}
	if got.Domain != "light" {
		t.Errorf("entity domain: got %q, want %q", got.Domain, "light")
	}
}

// TestIntegration_FindEntities_LocalOnlyWithoutGateway verifies that a runner
// with NATS but no gateway returns only its own local entities (graceful degradation).
func TestIntegration_FindEntities_LocalOnly(t *testing.T) {
	h := newAPIHarness(t)

	// Only start plugin A — no plugin B — and verify it finds its own entities locally.
	pluginA := &findEntitiesPlugin{
		id:           "local-only-plugin-a",
		seedDeviceID: "dev-local",
		seedEntityID: "ent-local-1",
		seedDomain:   "switch",
	}
	startFindEntitiesPlugin(t, h, pluginA)

	// Find its own entities — should be visible in its local cache immediately.
	got := pluginA.ctx.Registry.FindEntities(types.SearchQuery{Domain: "switch"})
	if len(got) == 0 {
		t.Fatal("FindEntities(domain=switch): got no results, expected own local entity")
	}
	if got[0].ID != "ent-local-1" {
		t.Errorf("entity ID: got %q, want ent-local-1", got[0].ID)
	}
}
