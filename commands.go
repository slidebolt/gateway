package main

import (
	"strings"
	"time"

	"github.com/slidebolt/sdk-types"
)

func storeVirtualCommandStatus(status types.CommandStatus) {
	if history == nil {
		return
	}
	// Keep synthetic virtual status rows in a disjoint positive stream_seq
	// range (well above JetStream sequence values, below int64 max).
	const baseSeq = uint64(4_000_000_000_000_000_000)
	seq := baseSeq + virtualStatusSeq.Add(1)
	if err := history.InsertCommandStatus(seq, status); err != nil {
		_ = history.InsertCommandStatus(uint64(time.Now().UnixNano()), status)
	}
}

func monitorVirtualCommand(commandID string) {
	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)

		vstore.mu.RLock()
		rec, ok := vstore.commands[commandID]
		vstore.mu.RUnlock()
		if !ok {
			return
		}
		downstream := append([]virtualDownstreamCommand(nil), rec.Downstream...)
		if len(downstream) == 0 && rec.SourceCommand != "" {
			downstream = []virtualDownstreamCommand{{
				PluginID: rec.SourcePluginID, CommandID: rec.SourceCommand,
			}}
		}
		if len(downstream) == 0 {
			continue
		}
		allTerminal := true
		anyFailed := false
		firstErr := ""
		updated := make([]virtualDownstreamCommand, 0, len(downstream))
		for _, ds := range downstream {
			st, err := fetchAnyCommandStatus(ds.PluginID, ds.CommandID)
			if err != nil {
				allTerminal = false
				updated = append(updated, ds)
				continue
			}
			ds.State = st.State
			ds.Error = st.Error
			updated = append(updated, ds)
			if st.State == types.CommandPending {
				allTerminal = false
				continue
			}
			if st.State == types.CommandFailed {
				anyFailed = true
				if firstErr == "" {
					firstErr = st.Error
				}
			}
		}
		if !allTerminal {
			continue
		}

		vstore.mu.Lock()
		rec, ok = vstore.commands[commandID]
		if !ok {
			vstore.mu.Unlock()
			return
		}
		rec.Downstream = updated
		if anyFailed {
			rec.Status.State = types.CommandFailed
			rec.Status.Error = strings.TrimSpace(firstErr)
			if rec.Status.Error == "" {
				rec.Status.Error = "one or more downstream commands failed"
			}
		} else {
			rec.Status.State = types.CommandSucceeded
			rec.Status.Error = ""
		}
		rec.Status.LastUpdatedAt = time.Now().UTC()
		vstore.commands[commandID] = rec
		storeVirtualCommandStatus(rec.Status)

		if vent, vok := vstore.entities[rec.VirtualKey]; vok {
			if rec.Status.State == types.CommandSucceeded {
				if vent.SourceQuery == "" {
					if src, err := findEntity(vent.SourcePluginID, vent.SourceDeviceID, vent.SourceEntityID); err == nil {
						vent.Entity.Data.Desired = src.Data.Desired
						vent.Entity.Data.Reported = src.Data.Reported
						vent.Entity.Data.Effective = src.Data.Effective
					}
				}
				vent.Entity.Data.SyncStatus = types.SyncStatusSynced
			} else {
				vent.Entity.Data.SyncStatus = types.SyncStatusFailed
			}
			vent.Entity.Data.LastCommandID = commandID
			vent.Entity.Data.UpdatedAt = time.Now().UTC()
			vstore.entities[rec.VirtualKey] = vent
		}
		vstore.persistLocked()
		vstore.mu.Unlock()
		return
	}

	vstore.mu.Lock()
	if rec, ok := vstore.commands[commandID]; ok {
		rec.Status.State = types.CommandFailed
		rec.Status.Error = "timeout waiting for source command"
		rec.Status.LastUpdatedAt = time.Now().UTC()
		vstore.commands[commandID] = rec
		storeVirtualCommandStatus(rec.Status)
		if vent, vok := vstore.entities[rec.VirtualKey]; vok {
			vent.Entity.Data.SyncStatus = types.SyncStatusFailed
			vent.Entity.Data.LastCommandID = commandID
			vent.Entity.Data.UpdatedAt = time.Now().UTC()
			vstore.entities[rec.VirtualKey] = vent
		}
		vstore.persistLocked()
	}
	vstore.mu.Unlock()
}
