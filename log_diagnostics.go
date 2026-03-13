package main

import (
	"hash/fnv"
	"log/slog"
	"sort"
	"sync"
	"time"
)

type diskWriteCounter struct {
	writes         uint64
	bytesWritten   uint64
	unchangedWrite uint64
}

type diskWriteState struct {
	diskWriteCounter
	lastHash uint64
	lastSize int
	seen     bool
}

type logDiagnostics struct {
	mu         sync.Mutex
	diskWrites map[string]*diskWriteState
}

var diag = &logDiagnostics{
	diskWrites: make(map[string]*diskWriteState),
}

func recordDiskWrite(path string, data []byte) {
	h := fnv.New64a()
	_, _ = h.Write(data)
	sum := h.Sum64()

	diag.mu.Lock()
	s, ok := diag.diskWrites[path]
	if !ok {
		s = &diskWriteState{}
		diag.diskWrites[path] = s
	}
	s.writes++
	s.bytesWritten += uint64(len(data))
	if s.seen && s.lastHash == sum && s.lastSize == len(data) {
		s.unchangedWrite++
	}
	s.lastHash = sum
	s.lastSize = len(data)
	s.seen = true
	diag.mu.Unlock()
}

func (d *logDiagnostics) snapshotAndResetDiskWrites() map[string]diskWriteCounter {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]diskWriteCounter, len(d.diskWrites))
	for path, state := range d.diskWrites {
		out[path] = state.diskWriteCounter
		state.diskWriteCounter = diskWriteCounter{}
	}
	return out
}

func startGatewayDiagnostics() {
	go func() {
		diskTicker := time.NewTicker(10 * time.Second)
		defer diskTicker.Stop()

		for {
			select {
			case <-diskTicker.C:
				logDiskWriteSummary(diag.snapshotAndResetDiskWrites())
			}
		}
	}()
}

func logDiskWriteSummary(stats map[string]diskWriteCounter) {
	if len(stats) == 0 {
		return
	}
	paths := make([]string, 0, len(stats))
	for path := range stats {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		s := stats[path]
		if s.writes == 0 {
			continue
		}
		slog.Debug("disk write summary", "window", "10s", "file", path, "writes", s.writes, "bytes_written", s.bytesWritten, "unchanged_writes", s.unchangedWrite)
	}
}

