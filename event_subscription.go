package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

// EventFilter defines which events a dynamic subscription should receive.
// It is the core, language-agnostic subscription descriptor.
type EventFilter struct {
	// EntityQuery selects which entities' events are eligible.
	// All populated fields must match (AND semantics).
	EntityQuery types.SearchQuery `json:"entity_query"`

	// Action filters on the event's "type" field inside the payload.
	// Empty string or "*" matches all actions. Glob patterns (e.g. "state.*") are supported.
	Action string `json:"action,omitempty"`
}

// dynamicSub is one active dynamic subscription.
type dynamicSub struct {
	id        string
	filter    EventFilter
	ch        chan types.EntityEventEnvelope
	closed    atomic.Bool
	once      sync.Once
	createdAt time.Time
}

func (s *dynamicSub) close() {
	s.once.Do(func() {
		s.closed.Store(true)
		close(s.ch)
	})
}

// trySend delivers env to the sub's channel. Returns false if the sub is
// closed or the buffer is full (drop).
func (s *dynamicSub) trySend(env types.EntityEventEnvelope) bool {
	if s.closed.Load() {
		return false
	}
	select {
	case s.ch <- env:
		return true
	default:
		return false // buffer full, drop
	}
}

// DynamicEventService maintains dynamic event subscriptions and fans matching
// events out to each subscriber's channel. A single NATS subscription is used
// for all active subscriptions; filtering is done in the handler.
type DynamicEventService struct {
	mu      sync.RWMutex
	subs    map[string]*dynamicSub
	natsSub *nats.Subscription
}

func newDynamicEventService() *DynamicEventService {
	return &DynamicEventService{
		subs: make(map[string]*dynamicSub),
	}
}

// Start subscribes to the entity-events NATS subject.
// Must be called once after the NATS connection is established.
func (s *DynamicEventService) Start(nc *nats.Conn) error {
	sub, err := nc.Subscribe(types.SubjectEntityEvents, s.handle)
	if err != nil {
		return err
	}
	s.natsSub = sub
	return nil
}

// Stop unsubscribes from NATS and closes all active subscription channels.
func (s *DynamicEventService) Stop() {
	if s.natsSub != nil {
		_ = s.natsSub.Unsubscribe()
	}
	s.mu.Lock()
	for _, sub := range s.subs {
		sub.close()
	}
	s.subs = make(map[string]*dynamicSub)
	s.mu.Unlock()
}

// Subscribe registers a new dynamic subscription and returns its ID and a
// channel on which matching events will be delivered. The channel is buffered
// (256 items); slow consumers drop events silently. The subscription lives
// until Unsubscribe is called.
func (s *DynamicEventService) Subscribe(filter EventFilter) (id string, ch <-chan types.EntityEventEnvelope) {
	sub := &dynamicSub{
		id:        nextID("dsub"),
		filter:    filter,
		ch:        make(chan types.EntityEventEnvelope, 256),
		createdAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.subs[sub.id] = sub
	s.mu.Unlock()
	return sub.id, sub.ch
}

// Unsubscribe cancels a subscription by ID and closes its channel.
// It is a no-op if the ID is unknown.
func (s *DynamicEventService) Unsubscribe(id string) {
	s.mu.Lock()
	sub, ok := s.subs[id]
	if ok {
		delete(s.subs, id)
	}
	s.mu.Unlock()
	if ok {
		sub.close()
	}
}

// Drain returns all events currently buffered for id without blocking.
// Used for polling-style consumers (tests, non-streaming clients).
// Returns nil if the subscription does not exist.
func (s *DynamicEventService) Drain(id string) []types.EntityEventEnvelope {
	s.mu.RLock()
	sub, ok := s.subs[id]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	var out []types.EntityEventEnvelope
	for {
		select {
		case env, open := <-sub.ch:
			if !open {
				return out
			}
			out = append(out, env)
		default:
			return out
		}
	}
}

// handle is the NATS message handler. It decodes the envelope and fans it out
// to all subscriptions whose filter matches.
func (s *DynamicEventService) handle(msg *nats.Msg) {
	var env types.EntityEventEnvelope
	if json.Unmarshal(msg.Data, &env) != nil {
		return
	}

	s.mu.RLock()
	subs := make([]*dynamicSub, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.RUnlock()

	for _, sub := range subs {
		if s.matchesFilter(sub.filter, env) {
			sub.trySend(env)
		}
	}
}

// matchesFilter returns true if env satisfies all criteria in f.
func (s *DynamicEventService) matchesFilter(f EventFilter, env types.EntityEventEnvelope) bool {
	q := f.EntityQuery

	if q.PluginID != "" && env.PluginID != q.PluginID {
		return false
	}
	if q.Domain != "" && !strings.EqualFold(env.EntityType, q.Domain) {
		return false
	}
	if q.EntityID != "" && env.EntityID != q.EntityID {
		return false
	}
	if q.Pattern != "" && q.Pattern != "*" {
		if !matchDynEventPattern(q.Pattern, env.EntityID) {
			return false
		}
	}

	// Label filter: requires a secondary registry lookup because labels are not
	// carried in the envelope. Only performed after cheaper checks pass.
	if len(q.Labels) > 0 {
		hits := performEntitySearch(types.SearchQuery{
			EntityID: env.EntityID,
			Labels:   q.Labels,
		})
		if len(hits) == 0 {
			return false
		}
	}

	// Action filter: decode the payload's "type" field.
	if f.Action != "" && f.Action != "*" {
		var payload map[string]any
		_ = json.Unmarshal(env.Payload, &payload)
		t, _ := payload["type"].(string)
		if !matchDynEventPattern(f.Action, t) {
			return false
		}
	}

	return true
}

// matchDynEventPattern returns true if pattern matches s.
// Supports exact match (case-insensitive) and glob (filepath.Match semantics).
func matchDynEventPattern(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	pl, sl := strings.ToLower(pattern), strings.ToLower(s)
	if !strings.Contains(pl, "*") {
		return pl == sl
	}
	ok, _ := filepath.Match(pl, sl)
	return ok
}
