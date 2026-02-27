package main

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-entities/light"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

func classifyEventName(entityType string, payload json.RawMessage, isVirtual bool) string {
	prefix := "entity.original"
	if isVirtual {
		prefix = "entity.virtual"
	}
	if entityType == light.Type {
		var p struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(payload, &p) == nil && p.Type == light.ActionSetRGB {
			return prefix + ".lightchange"
		}
	}
	return prefix + ".statechange"
}

func subscribeEntityEvents() {
	_, _ = nc.Subscribe(runner.SubjectEntityEvents, func(m *nats.Msg) {
		var env types.EntityEventEnvelope
		if err := json.Unmarshal(m.Data, &env); err != nil {
			return
		}

		vstore.mu.Lock()
		vstore.appendEventLocked(observedEvent{
			Name:      classifyEventName(env.EntityType, env.Payload, false),
			PluginID:  env.PluginID,
			DeviceID:  env.DeviceID,
			EntityID:  env.EntityID,
			EventID:   env.EventID,
			CreatedAt: time.Now().UTC(),
		})

		for key, rec := range vstore.entities {
			if !rec.MirrorSource {
				continue
			}
			if rec.SourcePluginID != env.PluginID || rec.SourceDeviceID != env.DeviceID || rec.SourceEntityID != env.EntityID {
				continue
			}
			src, err := findEntity(rec.SourcePluginID, rec.SourceDeviceID, rec.SourceEntityID)
			if err != nil {
				continue
			}
			rec.Entity.Data.Desired = src.Data.Desired
			rec.Entity.Data.Reported = src.Data.Reported
			rec.Entity.Data.Effective = src.Data.Effective
			rec.Entity.Data.SyncStatus = "in_sync"
			rec.Entity.Data.LastEventID = nextID("vevt")
			rec.Entity.Data.UpdatedAt = time.Now().UTC()
			vstore.entities[key] = rec
			vstore.appendEventLocked(observedEvent{
				Name:      classifyEventName(env.EntityType, env.Payload, true),
				PluginID:  rec.OwnerPluginID,
				DeviceID:  rec.OwnerDeviceID,
				EntityID:  rec.Entity.ID,
				EventID:   rec.Entity.Data.LastEventID,
				CreatedAt: time.Now().UTC(),
			})
		}
		vstore.persistLocked()
		vstore.mu.Unlock()
	})
}
