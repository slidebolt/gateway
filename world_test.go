package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// World — per-scenario state bag passed through context.
// ---------------------------------------------------------------------------

type worldKey struct{}

// World holds all state accumulated during a single Godog scenario.
type World struct {
	Harness        *Harness
	Plugins        map[string]*SimulatedPlugin
	LastResp       *http.Response
	LastBody       []byte
	Entities       []types.Entity
	Devices        []types.Device
	Manifests      []types.Manifest
	CommandID      string
	CommandState   types.CommandState
	LuaResult      string
	EventChannels  map[string]<-chan types.EntityEventEnvelope
	LastHTTPEntity *types.Entity
}

func newWorld(tmpDir string) (*World, error) {
	h, err := NewHarness(tmpDir)
	if err != nil {
		return nil, err
	}
	return &World{
		Harness:       h,
		Plugins:       make(map[string]*SimulatedPlugin),
		EventChannels: make(map[string]<-chan types.EntityEventEnvelope),
	}, nil
}

func (w *World) close() {
	for _, p := range w.Plugins {
		p.Stop()
	}
	w.Harness.Close()
}

func (w *World) plugin(id string) *SimulatedPlugin {
	if p, ok := w.Plugins[id]; ok {
		return p
	}
	p := &SimulatedPlugin{ID: id, nc: w.Harness.NC}
	w.Plugins[id] = p
	return p
}

// do executes an HTTP request and captures the response + body for step assertions.
func (w *World) do(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	w.LastResp = resp
	w.LastBody, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	return nil
}

// decodeLastBody unmarshals the captured response body into dst.
func (w *World) decodeLastBody(dst any) error {
	if err := json.Unmarshal(w.LastBody, dst); err != nil {
		return fmt.Errorf("decode body: %w\nbody was: %s", err, w.LastBody)
	}
	return nil
}

// assertLastStatus returns an error if the last response status doesn't match.
func (w *World) assertLastStatus(want int) error {
	if w.LastResp == nil {
		return fmt.Errorf("no response recorded")
	}
	if w.LastResp.StatusCode != want {
		return fmt.Errorf("expected HTTP %d, got %d\nbody: %s", want, w.LastResp.StatusCode, w.LastBody)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

func worldFrom(ctx context.Context) *World {
	return ctx.Value(worldKey{}).(*World)
}

func withWorld(ctx context.Context, w *World) context.Context {
	return context.WithValue(ctx, worldKey{}, w)
}

// ---------------------------------------------------------------------------
// Scenario initialiser — registers all step definitions.
// ---------------------------------------------------------------------------

func InitializeScenario(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, scenario *godog.Scenario) (context.Context, error) {
		tmp, err := os.MkdirTemp("", "gateway-godog-*")
		if err != nil {
			return ctx, fmt.Errorf("tmpdir: %w", err)
		}
		w, err := newWorld(tmp)
		if err != nil {
			os.RemoveAll(tmp)
			return ctx, err
		}
		// stash tmpDir path for cleanup
		ctx = context.WithValue(ctx, tmpDirKey{}, tmp)
		return withWorld(ctx, w), nil
	})

	sc.After(func(ctx context.Context, scenario *godog.Scenario, err error) (context.Context, error) {
		if w, ok := ctx.Value(worldKey{}).(*World); ok && w != nil {
			w.close()
		}
		if tmp, ok := ctx.Value(tmpDirKey{}).(string); ok {
			os.RemoveAll(tmp)
		}
		return ctx, nil
	})

	registerCommonSteps(sc)
	registerPluginSteps(sc)
	registerSearchSteps(sc)
	registerCommandSteps(sc)
	registerScriptSteps(sc)
	registerHistorySteps(sc)
	registerBroadSearchSteps(sc)
	registerEventSteps(sc)
	registerScriptingAPISteps(sc)
	registerScriptingLuaSteps(sc)
	registerGroupSteps(sc)
	registerEntityQuerySteps(sc)
	registerEventSubscriptionSteps(sc)
	registerEntityMetaSteps(sc)
}

type tmpDirKey struct{}
