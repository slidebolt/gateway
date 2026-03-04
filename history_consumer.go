package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

func startHistoryConsumers(ctx context.Context) {
	if history == nil || js == nil {
		return
	}
	go consumeEventHistory(ctx)
	go consumeCommandHistory(ctx)
}

func consumeEventHistory(ctx context.Context) {
	sub, err := js.PullSubscribe(
		runner.SubjectEntityEvents,
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
			if err := history.InsertEvent(meta.Sequence.Stream, meta.Timestamp.UTC(), env); err != nil {
				log.Printf("history events insert failed (seq=%d): %v", meta.Sequence.Stream, err)
				_ = msg.Nak()
				continue
			}
			broker.broadcast(sseMessage{
				Type:      "log",
				Kind:      "event",
				PluginID:  env.PluginID,
				DeviceID:  env.DeviceID,
				EntityID:  env.EntityID,
				Name:      classifyEventName(env.EntityType, env.Payload, false),
				EventID:   env.EventID,
				CreatedAt: meta.Timestamp.UTC().Format(time.RFC3339Nano),
				Seq:       meta.Sequence.Stream,
			})
			_ = msg.Ack()
		}
	}
}

func consumeCommandHistory(ctx context.Context) {
	sub, err := js.PullSubscribe(
		runner.SubjectCommandStatus,
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
			if err := history.InsertCommandStatus(meta.Sequence.Stream, status); err != nil {
				log.Printf("history commands insert failed (seq=%d): %v", meta.Sequence.Stream, err)
				_ = msg.Nak()
				continue
			}
			broker.broadcast(sseMessage{
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
