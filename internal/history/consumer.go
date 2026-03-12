package history

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

func classifyEventName() string {
	return "entity.statechange"
}

func (h *History) subscribeEntityEvents(nc *nats.Conn) {
	_, _ = nc.Subscribe(types.SubjectEntityEvents, func(m *nats.Msg) {
		var env types.EntityEventEnvelope
		if err := json.Unmarshal(m.Data, &env); err != nil {
			return
		}
		h.broker.broadcast(sseMessage{Type: "entity", PluginID: env.PluginID, DeviceID: env.DeviceID, EntityID: env.EntityID})
	})
}

func (h *History) consumeEvents(ctx context.Context, js nats.JetStreamContext) {
	sub, err := js.PullSubscribe(
		types.SubjectEntityEvents,
		"gateway-history-events",
		nats.BindStream("EVENTS"),
	)
	if err != nil {
		log.Printf("history events consumer init failed: %v", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := sub.Fetch(128, nats.MaxWait(time.Second))
		if err != nil {
			if err == nats.ErrTimeout || ctx.Err() != nil {
				continue
			}
			log.Printf("history events fetch failed: %v", err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		for _, msg := range msgs {
			meta, err := msg.Metadata()
			if err != nil {
				_ = msg.Ack()
				continue
			}
			var env types.EntityEventEnvelope
			if err := json.Unmarshal(msg.Data, &env); err != nil {
				_ = msg.Ack()
				continue
			}
			if err := h.insertEvent(meta.Sequence.Stream, meta.Timestamp.UTC(), env); err != nil {
				log.Printf("history events insert failed (seq=%d): %v", meta.Sequence.Stream, err)
				_ = msg.Nak()
				continue
			}
			h.broker.broadcast(sseMessage{
				Type:      "log",
				Kind:      "event",
				PluginID:  env.PluginID,
				DeviceID:  env.DeviceID,
				EntityID:  env.EntityID,
				Name:      classifyEventName(),
				EventID:   env.EventID,
				CreatedAt: meta.Timestamp.UTC().Format(time.RFC3339Nano),
				Seq:       meta.Sequence.Stream,
			})
			_ = msg.Ack()
		}
	}
}

func (h *History) consumeCommands(ctx context.Context, js nats.JetStreamContext) {
	sub, err := js.PullSubscribe(
		types.SubjectCommandStatus,
		"gateway-history-commands",
		nats.BindStream("COMMANDS"),
	)
	if err != nil {
		log.Printf("history commands consumer init failed: %v", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgs, err := sub.Fetch(128, nats.MaxWait(time.Second))
		if err != nil {
			if err == nats.ErrTimeout || ctx.Err() != nil {
				continue
			}
			log.Printf("history commands fetch failed: %v", err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		for _, msg := range msgs {
			meta, err := msg.Metadata()
			if err != nil {
				_ = msg.Ack()
				continue
			}
			var status types.CommandStatus
			if err := json.Unmarshal(msg.Data, &status); err != nil {
				_ = msg.Ack()
				continue
			}
			if status.CommandID == "" {
				_ = msg.Ack()
				continue
			}
			if err := h.insertCommandStatus(meta.Sequence.Stream, status); err != nil {
				log.Printf("history commands insert failed (seq=%d): %v", meta.Sequence.Stream, err)
				_ = msg.Nak()
				continue
			}
			h.broker.broadcast(sseMessage{
				Type:      "log",
				Kind:      "command",
				PluginID:  status.PluginID,
				DeviceID:  status.DeviceID,
				EntityID:  status.EntityID,
				State:     string(status.State),
				CommandID: status.CommandID,
				CreatedAt: meta.Timestamp.UTC().Format(time.RFC3339Nano),
				Seq:       meta.Sequence.Stream,
			})
			_ = msg.Ack()
		}
	}
}
