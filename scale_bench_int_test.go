package main

// Scale benchmarks: gradually increase plugins, devices, entities, events and
// commands to find where throughput degrades. Each sub-benchmark is
// deterministic — same IDs, same sequence of actions every run.
//
// Two benchmark families:
//
//   BenchmarkScale      — serial HTTP (1 goroutine), reveals per-request latency
//   BenchmarkScaleConc  — concurrent HTTP (workerCount goroutines), reveals
//                         true gateway throughput by saturating the server
//
// Run all with:
//   GOWORK=off go test -bench=BenchmarkScale -benchtime=1x -benchmem -v -timeout 600s
//
// Metrics reported per sub-benchmark:
//   events/sec    — EntityEventEnvelope throughput through gateway
//   cmds/sec      — command dispatch throughput
//   sub_hits/sec  — dynamic subscription match throughput
//   errors        — HTTP-level errors (should stay 0)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slidebolt/sdk-types"
)

// scaleConfig defines one point in the scale curve.
type scaleConfig struct {
	plugins           int
	devicesPerPlugin  int
	entitiesPerDevice int
	eventsPerEntity   int
	dynamicSubs       int // subscriptions watching ALL scale entities via label
}

var scaleLevels = []scaleConfig{
	{plugins: 1,    devicesPerPlugin: 1, entitiesPerDevice: 5, eventsPerEntity: 3, dynamicSubs: 1},
	{plugins: 10,   devicesPerPlugin: 1, entitiesPerDevice: 5, eventsPerEntity: 3, dynamicSubs: 5},
	{plugins: 100,  devicesPerPlugin: 1, entitiesPerDevice: 5, eventsPerEntity: 3, dynamicSubs: 10},
	{plugins: 1000, devicesPerPlugin: 1, entitiesPerDevice: 5, eventsPerEntity: 3, dynamicSubs: 20},
}

const scaleLabelKey = "ScaleTest"
const scaleLabelVal = "true"

// workerCount is the concurrency level for BenchmarkScaleConc.
const workerCount = 32

// ---------------------------------------------------------------------------
// Shared setup
// ---------------------------------------------------------------------------

type scaleFixture struct {
	harness   *Harness
	plugins   []*SimulatedPlugin
	subIDs    []string
	eventJobs []scaleEventJob
	cmdJobs   []scaleCmdJob
	cfg       scaleConfig
}

type scaleEventJob struct {
	pluginID string
	deviceID string
	entityID string
	action   string
}

type scaleCmdJob struct {
	pluginID string
	deviceID string
	entityID string
}

func newScaleFixture(b *testing.B, cfg scaleConfig) (*scaleFixture, func()) {
	b.Helper()
	tmpDir, err := os.MkdirTemp("", "scale-bench-*")
	if err != nil {
		b.Fatalf("tmpDir: %v", err)
	}

	h, err := NewHarness(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		b.Fatalf("harness: %v", err)
	}

	plugins := make([]*SimulatedPlugin, cfg.plugins)
	for pi := 0; pi < cfg.plugins; pi++ {
		pluginID := fmt.Sprintf("scale-plugin-%04d", pi)
		p := &SimulatedPlugin{ID: pluginID, nc: h.NC}
		if err := p.Start(); err != nil {
			b.Fatalf("start plugin %s: %v", pluginID, err)
		}
		for di := 0; di < cfg.devicesPerPlugin; di++ {
			deviceID := fmt.Sprintf("%s-dev-%02d", pluginID, di)
			p.Devices = append(p.Devices, types.Device{ID: deviceID, LocalName: deviceID})
			for ei := 0; ei < cfg.entitiesPerDevice; ei++ {
				entityID := fmt.Sprintf("%s-dev-%02d-ent-%02d", pluginID, di, ei)
				p.Entities = append(p.Entities, types.Entity{
					ID:        entityID,
					PluginID:  pluginID,
					DeviceID:  deviceID,
					LocalName: entityID,
					Domain:    "light",
					Labels:    map[string][]string{scaleLabelKey: {scaleLabelVal}},
				})
			}
		}
		if err := p.SeedRegistry(); err != nil {
			b.Fatalf("seed plugin %s: %v", pluginID, err)
		}
		plugins[pi] = p
	}

	// Create dynamic subscriptions.
	subIDs := make([]string, cfg.dynamicSubs)
	for si := 0; si < cfg.dynamicSubs; si++ {
		resp, err := h.Post("/api/events/subscriptions", EventFilter{
			EntityQuery: types.SearchQuery{
				Labels: map[string][]string{scaleLabelKey: {scaleLabelVal}},
			},
		})
		if err != nil {
			b.Fatalf("create sub %d: %v", si, err)
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := ReadJSON(resp, &body); err != nil {
			b.Fatalf("decode sub %d: %v", si, err)
		}
		subIDs[si] = body.ID
	}

	// Build deterministic event + command lists.
	actions := []string{"on", "state", "off"}
	var eventJobs []scaleEventJob
	var cmdJobs []scaleCmdJob
	for pi := 0; pi < cfg.plugins; pi++ {
		p := plugins[pi]
		for di := 0; di < cfg.devicesPerPlugin; di++ {
			deviceID := fmt.Sprintf("%s-dev-%02d", p.ID, di)
			for ei := 0; ei < cfg.entitiesPerDevice; ei++ {
				entityID := fmt.Sprintf("%s-dev-%02d-ent-%02d", p.ID, di, ei)
				for ai := 0; ai < cfg.eventsPerEntity; ai++ {
					eventJobs = append(eventJobs, scaleEventJob{
						pluginID: p.ID,
						deviceID: deviceID,
						entityID: entityID,
						action:   actions[ai%len(actions)],
					})
				}
				cmdJobs = append(cmdJobs, scaleCmdJob{p.ID, deviceID, entityID})
			}
		}
	}

	totalEntities := cfg.plugins * cfg.devicesPerPlugin * cfg.entitiesPerDevice
	b.Logf("scale: %d plugins, %d entities, %d events, %d cmds, %d dynamic subs",
		cfg.plugins, totalEntities, len(eventJobs), len(cmdJobs), cfg.dynamicSubs)

	cleanup := func() {
		for _, p := range plugins {
			p.Stop()
		}
		h.Close()
		os.RemoveAll(tmpDir)
	}
	return &scaleFixture{
		harness:   h,
		plugins:   plugins,
		subIDs:    subIDs,
		eventJobs: eventJobs,
		cmdJobs:   cmdJobs,
		cfg:       cfg,
	}, cleanup
}

// drainAndReport waits for async NATS delivery then reports subscription hits.
func (f *scaleFixture) drainAndReport(b *testing.B, elapsedSec float64) {
	b.Helper()
	time.Sleep(150 * time.Millisecond)
	totalHits := 0
	for _, id := range f.subIDs {
		path := fmt.Sprintf("/api/events/subscriptions/%s/events", id)
		resp, err := f.harness.Get(path)
		if err != nil {
			b.Logf("drain sub %s: %v", id, err)
			continue
		}
		var body struct {
			Events []types.EntityEventEnvelope `json:"events"`
		}
		_ = ReadJSON(resp, &body)
		totalHits += len(body.Events)
	}
	b.ReportMetric(float64(totalHits)/elapsedSec, "sub_hits/sec")
	b.Logf("dynamic sub hits: %d across %d subs (expected ~%d per sub)",
		totalHits, len(f.subIDs), len(f.eventJobs))
}

// ---------------------------------------------------------------------------
// BenchmarkScale — serial (1 goroutine, reveals per-request latency)
// ---------------------------------------------------------------------------

func BenchmarkScale(b *testing.B) {
	for _, cfg := range scaleLevels {
		cfg := cfg
		name := fmt.Sprintf("%d_plugins_%d_entities_each",
			cfg.plugins, cfg.devicesPerPlugin*cfg.entitiesPerDevice)
		b.Run(name, func(b *testing.B) {
			f, cleanup := newScaleFixture(b, cfg)
			defer cleanup()

			b.ResetTimer()
			start := time.Now()

			for _, j := range f.eventJobs {
				path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/events",
					j.pluginID, j.deviceID, j.entityID)
				resp, err := f.harness.Post(path, map[string]any{"type": j.action})
				if err != nil {
					b.Fatalf("event POST: %v", err)
				}
				resp.Body.Close()
			}
			for _, c := range f.cmdJobs {
				path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/commands",
					c.pluginID, c.deviceID, c.entityID)
				resp, err := f.harness.Post(path, map[string]any{"type": "turn_on"})
				if err != nil {
					b.Fatalf("cmd POST: %v", err)
				}
				resp.Body.Close()
			}

			elapsed := time.Since(start)
			b.StopTimer()

			sec := elapsed.Seconds()
			b.ReportMetric(float64(len(f.eventJobs))/sec, "events/sec")
			b.ReportMetric(float64(len(f.cmdJobs))/sec, "cmds/sec")
			b.ReportMetric(float64(len(f.eventJobs)+len(f.cmdJobs))/sec, "ops/sec")
			f.drainAndReport(b, sec)
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkScaleConc — concurrent (workerCount goroutines, reveals throughput)
// ---------------------------------------------------------------------------

func BenchmarkScaleConc(b *testing.B) {
	for _, cfg := range scaleLevels {
		cfg := cfg
		name := fmt.Sprintf("%d_plugins_%d_entities_each_%dw",
			cfg.plugins, cfg.devicesPerPlugin*cfg.entitiesPerDevice, workerCount)
		b.Run(name, func(b *testing.B) {
			f, cleanup := newScaleFixture(b, cfg)
			defer cleanup()

			// Interleave events and commands into one flat job list so workers
			// exercise both code paths simultaneously.
			type genericJob struct {
				path    string
				payload map[string]any
			}
			jobs := make([]genericJob, 0, len(f.eventJobs)+len(f.cmdJobs))
			for _, j := range f.eventJobs {
				jobs = append(jobs, genericJob{
					path:    fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/events", j.pluginID, j.deviceID, j.entityID),
					payload: map[string]any{"type": j.action},
				})
			}
			for _, c := range f.cmdJobs {
				jobs = append(jobs, genericJob{
					path:    fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/commands", c.pluginID, c.deviceID, c.entityID),
					payload: map[string]any{"type": "turn_on"},
				})
			}

			// Feed jobs through a channel.
			jobCh := make(chan genericJob, len(jobs))
			for _, j := range jobs {
				jobCh <- j
			}
			close(jobCh)

			var errCount atomic.Int64
			var wg sync.WaitGroup
			workers := workerCount
			if workers > len(jobs) {
				workers = len(jobs)
			}

			b.ResetTimer()
			start := time.Now()

			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := range jobCh {
						resp, err := f.harness.Post(j.path, j.payload)
						if err != nil {
							errCount.Add(1)
							continue
						}
						if resp.StatusCode >= 500 {
							errCount.Add(1)
						}
						resp.Body.Close()
					}
				}()
			}
			wg.Wait()

			elapsed := time.Since(start)
			b.StopTimer()

			sec := elapsed.Seconds()
			totalOps := len(jobs)
			totalEvents := len(f.eventJobs)
			totalCmds := len(f.cmdJobs)

			b.ReportMetric(float64(totalEvents)/sec, "events/sec")
			b.ReportMetric(float64(totalCmds)/sec, "cmds/sec")
			b.ReportMetric(float64(totalOps)/sec, "ops/sec")
			b.ReportMetric(float64(errCount.Load()), "errors")
			if errCount.Load() > 0 {
				b.Logf("WARNING: %d HTTP errors", errCount.Load())
			}
			f.drainAndReport(b, sec)
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkScaleConcEventsOnly — pure event throughput, no commands, fully concurrent
// ---------------------------------------------------------------------------

// postJSON posts a JSON body using a caller-supplied *http.Client.
func postJSON(client *http.Client, url string, body any) (*http.Response, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

func BenchmarkScaleConcEventsOnly(b *testing.B) {
	// Use a fixed 100-plugin config to find the event pipeline ceiling.
	cfg := scaleConfig{
		plugins: 100, devicesPerPlugin: 1, entitiesPerDevice: 5,
		eventsPerEntity: 10, dynamicSubs: 5,
	}
	f, cleanup := newScaleFixture(b, cfg)
	defer cleanup()

	jobCh := make(chan scaleEventJob, len(f.eventJobs))
	for _, j := range f.eventJobs {
		jobCh <- j
	}
	close(jobCh)

	var errCount atomic.Int64
	var wg sync.WaitGroup

	b.ResetTimer()
	start := time.Now()

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker uses its own http.Client to avoid mutex contention on
			// http.DefaultTransport.
			client := &http.Client{Transport: &http.Transport{
				MaxIdleConnsPerHost: 8,
			}}
			for j := range jobCh {
				path := f.harness.Server.URL + fmt.Sprintf(
					"/api/plugins/%s/devices/%s/entities/%s/events",
					j.pluginID, j.deviceID, j.entityID)
				resp, err := postJSON(client, path, map[string]any{"type": j.action})
				if err != nil {
					errCount.Add(1)
					continue
				}
				if resp.StatusCode >= 500 {
					errCount.Add(1)
				}
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	elapsed := time.Since(start)
	b.StopTimer()

	sec := elapsed.Seconds()
	b.ReportMetric(float64(len(f.eventJobs))/sec, "events/sec")
	b.ReportMetric(float64(errCount.Load()), "errors")
	f.drainAndReport(b, sec)
}
