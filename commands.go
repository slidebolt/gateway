package main

import (
	"time"

	"github.com/slidebolt/sdk-types"
)

func monitorVirtualCommand(commandID string) {
	for i := 0; i < 100; i++ {
		time.Sleep(100 * time.Millisecond)

		vstore.mu.RLock()
		rec, ok := vstore.commands[commandID]
		vstore.mu.RUnlock()
		if !ok {
			return
		}

		sourceStatus, err := fetchCommandStatus(rec.SourcePluginID, rec.SourceCommand)
		if err != nil {
			continue
		}
		if sourceStatus.State == types.CommandPending {
			continue
		}

		vstore.mu.Lock()
		rec, ok = vstore.commands[commandID]
		if !ok {
			vstore.mu.Unlock()
			return
		}
		rec.Status.State = sourceStatus.State
		rec.Status.Error = sourceStatus.Error
		rec.Status.LastUpdatedAt = time.Now().UTC()
		vstore.commands[commandID] = rec

		if vent, vok := vstore.entities[rec.VirtualKey]; vok {
			if sourceStatus.State == types.CommandSucceeded {
				if src, err := findEntity(vent.SourcePluginID, vent.SourceDeviceID, vent.SourceEntityID); err == nil {
					vent.Entity.Data.Desired = src.Data.Desired
					vent.Entity.Data.Reported = src.Data.Reported
					vent.Entity.Data.Effective = src.Data.Effective
				}
				vent.Entity.Data.SyncStatus = "in_sync"
			} else {
				vent.Entity.Data.SyncStatus = "failed"
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
		if vent, vok := vstore.entities[rec.VirtualKey]; vok {
			vent.Entity.Data.SyncStatus = "failed"
			vent.Entity.Data.LastCommandID = commandID
			vent.Entity.Data.UpdatedAt = time.Now().UTC()
			vstore.entities[rec.VirtualKey] = vent
		}
		vstore.persistLocked()
	}
	vstore.mu.Unlock()
}
