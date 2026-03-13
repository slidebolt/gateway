// Package scripting provides the Lua scripting runtime for the gateway.
//
// Architecture:
//
//   ScriptEngine      - factory: creates, starts and stops VMs
//   VM                - one per script; owns a *lua.LState + work-queue goroutine
//   EntityBinding     - "This" inside every Lua VM; the single bespoke piece
//   QueryScripting    - QueryService.Scripting.* bindings
//   CommandScripting  - CommandService.Scripting.* bindings
//   EventScripting    - EventService.Scripting.* bindings + subject translation
//
// All gateway dependencies are injected via interfaces so this package has no
// concrete imports from the gateway's main package.
package scripting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Dependency interfaces — injected at VM creation time.
// ---------------------------------------------------------------------------

// CommandSubmitter abstracts commandService.Submit.
type CommandSubmitter interface {
	Submit(pluginID, deviceID, entityID string, payload json.RawMessage) (types.CommandStatus, error)
}

// EntityFinder abstracts Registry.FindEntities / FindDevices.
type EntityFinder interface {
	FindEntities(q types.SearchQuery) []types.Entity
	FindDevices(q types.SearchQuery) []types.Device
}

// EventBus abstracts the NATS connection for pub/sub.
type EventBus interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, handler func([]byte)) (Subscription, error)
}

// Subscription is a cancellable NATS subscription handle.
type Subscription interface {
	Unsubscribe() error
}

// ---------------------------------------------------------------------------
// Services — one instance per VM, injected via NewVM.
// ---------------------------------------------------------------------------

// Services bundles all gateway dependencies the scripting layer needs.
type Services struct {
	Commands CommandSubmitter
	Finder   EntityFinder
	Bus      EventBus
}

// ---------------------------------------------------------------------------
// Subject-string translation
// ---------------------------------------------------------------------------

// SubjectFilter is a compiled predicate derived from a subject string.
// Subject string formats:
//
//	"entityID.eventType"  – exact entity + event type match
//	"entityID.*"          – all events for entity
//	"?domain=light"       – events from entities of given domain
//	"?label=Key:Value"    – events from label-matched entities
//	"?pattern=*name*"     – events from pattern-matched entities
//	"*"                   – all entity events
type SubjectFilter struct {
	raw      string
	entityID string            // empty = no filter
	evtType  string            // empty = no filter; "*" = any
	query    *types.SearchQuery // non-nil when "?" prefix
}

// ParseSubject compiles a subject string into a SubjectFilter.
func ParseSubject(s string) (SubjectFilter, error) {
	if s == "" || s == "*" {
		return SubjectFilter{raw: s}, nil
	}

	// Query-string form: "?domain=light&label=Home:Basement"
	if strings.HasPrefix(s, "?") {
		params, err := url.ParseQuery(s[1:])
		if err != nil {
			return SubjectFilter{}, fmt.Errorf("scripting: invalid subject %q: %w", s, err)
		}
		q := &types.SearchQuery{}
		if d := params.Get("domain"); d != "" {
			q.Domain = d
		}
		if p := params.Get("pattern"); p != "" {
			q.Pattern = p
		}
		if pid := params.Get("plugin_id"); pid != "" {
			q.PluginID = pid
		}
		if labels := params["label"]; len(labels) > 0 {
			q.Labels = types.ParseLabels(labels)
		}
		return SubjectFilter{raw: s, query: q}, nil
	}

	// "entityID.eventType" or "entityID.*"
	parts := strings.SplitN(s, ".", 2)
	f := SubjectFilter{raw: s, entityID: parts[0]}
	if len(parts) == 2 {
		f.evtType = parts[1]
	}
	return f, nil
}

// Matches reports whether the envelope satisfies this filter.
func (f SubjectFilter) Matches(env types.EntityEventEnvelope) bool {
	if f.query != nil {
		// Domain filter
		if f.query.Domain != "" && !strings.EqualFold(env.EntityType, f.query.Domain) {
			return false
		}
		// Pattern filter (simple glob: * matches anything)
		if f.query.Pattern != "" && f.query.Pattern != "*" {
			if !matchGlob(f.query.Pattern, env.EntityID) {
				return false
			}
		}
		// PluginID filter
		if f.query.PluginID != "" && env.PluginID != f.query.PluginID {
			return false
		}
		// Label filter requires the entity — we only have the envelope here.
		// Labels on the envelope aren't part of EntityEventEnvelope, so label
		// filtering is best-effort: skip at envelope level (always match) and
		// let callers do a secondary FindEntities check when needed.
		return true
	}

	if f.entityID != "" && f.entityID != "*" {
		if env.EntityID != f.entityID {
			return false
		}
	}
	if f.evtType != "" && f.evtType != "*" {
		var payload map[string]any
		_ = json.Unmarshal(env.Payload, &payload)
		t, _ := payload["type"].(string)
		if t != f.evtType {
			return false
		}
	}
	return true
}

func matchGlob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	parts := strings.SplitN(pattern, "*", 2)
	return strings.HasPrefix(s, parts[0]) && strings.HasSuffix(s, parts[1])
}

// ---------------------------------------------------------------------------
// QueryScripting — QueryService.Scripting.*
// ---------------------------------------------------------------------------

// QueryScripting provides the ergonomic Lua-facing query API.
type QueryScripting struct {
	finder EntityFinder
}

func newQueryScripting(f EntityFinder) *QueryScripting { return &QueryScripting{finder: f} }

// NewQueryScripting is the exported constructor for use in tests.
func NewQueryScripting(f EntityFinder) *QueryScripting { return newQueryScripting(f) }

// Find accepts a query string ("?domain=light&label=Home:Basement") or an
// empty string (returns all entities) and returns an EntityList.
func (q *QueryScripting) Find(queryStr string) (EntityList, error) {
	sq, err := parseSearchQuery(queryStr)
	if err != nil {
		return nil, err
	}
	return EntityList(q.finder.FindEntities(sq)), nil
}

// FindOne returns the first matching entity or nil.
func (q *QueryScripting) FindOne(queryStr string) (*types.Entity, error) {
	sq, err := parseSearchQuery(queryStr)
	if err != nil {
		return nil, err
	}
	sq.Limit = 1
	entities := q.finder.FindEntities(sq)
	if len(entities) == 0 {
		return nil, nil
	}
	e := entities[0]
	return &e, nil
}

func parseSearchQuery(s string) (types.SearchQuery, error) {
	if s == "" || s == "*" {
		return types.SearchQuery{}, nil
	}
	if !strings.HasPrefix(s, "?") {
		return types.SearchQuery{}, fmt.Errorf("scripting: query string must start with '?' or be empty, got %q", s)
	}
	params, err := url.ParseQuery(s[1:])
	if err != nil {
		return types.SearchQuery{}, fmt.Errorf("scripting: parse query %q: %w", s, err)
	}
	q := types.SearchQuery{}
	q.Domain = params.Get("domain")
	q.Pattern = params.Get("pattern")
	q.PluginID = params.Get("plugin_id")
	q.DeviceID = params.Get("device_id")
	q.EntityID = params.Get("entity_id")
	if labels := params["label"]; len(labels) > 0 {
		q.Labels = types.ParseLabels(labels)
	}
	return q, nil
}

// EntityList is a slice of entities with scripting conveniences.
type EntityList []types.Entity

func (l EntityList) Count() int { return len(l) }

// Each calls fn for every entity.
func (l EntityList) Each(fn func(types.Entity)) {
	for _, e := range l {
		fn(e)
	}
}

// Where returns a filtered sub-list.
func (l EntityList) Where(fn func(types.Entity) bool) EntityList {
	out := make(EntityList, 0, len(l))
	for _, e := range l {
		if fn(e) {
			out = append(out, e)
		}
	}
	return out
}

// First returns the first entity or nil.
func (l EntityList) First() *types.Entity {
	if len(l) == 0 {
		return nil
	}
	e := l[0]
	return &e
}

// ---------------------------------------------------------------------------
// CommandScripting — CommandService.Scripting.*
// ---------------------------------------------------------------------------

// CommandScripting provides the ergonomic Lua-facing command API.
type CommandScripting struct {
	commands CommandSubmitter
}

func newCommandScripting(c CommandSubmitter) *CommandScripting {
	return &CommandScripting{commands: c}
}

// NewCommandScripting is the exported constructor for use in tests.
func NewCommandScripting(c CommandSubmitter) *CommandScripting { return newCommandScripting(c) }

// Send constructs a payload from action + params, merging {type: action} in,
// then submits the command. Returns the command ID.
func (c *CommandScripting) Send(e types.Entity, action string, params map[string]any) (string, error) {
	if params == nil {
		params = map[string]any{}
	}
	params["type"] = action
	payload, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("scripting: marshal payload: %w", err)
	}
	status, err := c.commands.Submit(e.PluginID, e.DeviceID, e.ID, payload)
	if err != nil {
		return "", err
	}
	return status.CommandID, nil
}

// SendTo is the fully-qualified version when you don't have an Entity object.
func (c *CommandScripting) SendTo(pluginID, deviceID, entityID, action string, params map[string]any) (string, error) {
	e := types.Entity{ID: entityID, PluginID: pluginID, DeviceID: deviceID}
	return c.Send(e, action, params)
}

// ---------------------------------------------------------------------------
// EventScripting — EventService.Scripting.*
// ---------------------------------------------------------------------------

// HandlerFunc is a Go callback invoked when a matching event arrives.
type HandlerFunc func(env types.EntityEventEnvelope)

// EventScripting provides the ergonomic Lua-facing event subscription API.
type EventScripting struct {
	bus    EventBus
	finder EntityFinder // optional: used to resolve entity labels for label-filtered subscriptions
}

func newEventScripting(b EventBus) *EventScripting { return &EventScripting{bus: b} }

func newEventScriptingWithFinder(b EventBus, f EntityFinder) *EventScripting {
	return &EventScripting{bus: b, finder: f}
}

// NewEventScripting is the exported constructor for use in tests.
func NewEventScripting(b EventBus, f EntityFinder) *EventScripting {
	return newEventScriptingWithFinder(b, f)
}

// OnEvent subscribes to entity events matching subject. The subscription is
// automatically cancelled when ctx is done. Returns a Subscription that can
// be cancelled earlier if needed.
func (e *EventScripting) OnEvent(ctx context.Context, subject string, fn HandlerFunc) (Subscription, error) {
	filter, err := ParseSubject(subject)
	if err != nil {
		return nil, err
	}

	hasLabelFilter := filter.query != nil && len(filter.query.Labels) > 0

	sub, err := e.bus.Subscribe(types.SubjectEntityEvents, func(data []byte) {
		var env types.EntityEventEnvelope
		if json.Unmarshal(data, &env) != nil {
			return
		}
		if !filter.Matches(env) {
			return
		}
		// Label filters require a secondary entity lookup since envelopes
		// do not carry label data.
		if hasLabelFilter && e.finder != nil {
			entities := e.finder.FindEntities(types.SearchQuery{
				EntityID: env.EntityID,
				Labels:   filter.query.Labels,
			})
			if len(entities) == 0 {
				return
			}
		}
		fn(env)
	})
	if err != nil {
		return nil, err
	}

	// Auto-unsubscribe when context is cancelled.
	go func() {
		<-ctx.Done()
		_ = sub.Unsubscribe()
	}()

	return sub, nil
}

// Publish emits an EntityEventEnvelope onto SubjectEntityEvents.
// Used by EntityBinding.SendEvent.
func (e *EventScripting) Publish(env types.EntityEventEnvelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return e.bus.Publish(types.SubjectEntityEvents, data)
}

// ---------------------------------------------------------------------------
// EntityBinding — "This" inside every Lua VM
// ---------------------------------------------------------------------------

// IncomingCommand represents a command arriving at a scripting entity.
type IncomingCommand struct {
	Name   string
	Params map[string]any
}

// EntityBinding is the bespoke scripting context bound to a specific entity.
// It is the single piece that has no analog in the existing Go services.
type EntityBinding struct {
	Entity types.Entity

	commands *CommandScripting
	events   *EventScripting

	// Per-command-name callbacks registered by This.OnCommand(name, fn).
	commandHandlers map[string]func(IncomingCommand)

	// OnEvent subscription managed by This.OnEvent.
	onEventSub Subscription
}

// newEntityBinding constructs a This for the given entity and services.
func newEntityBinding(entity types.Entity, cmds *CommandScripting, evts *EventScripting) *EntityBinding {
	return &EntityBinding{
		Entity:   entity,
		commands: cmds,
		events:   evts,
	}
}

// NewEntityBinding is the exported constructor for use in tests.
func NewEntityBinding(entity types.Entity, cmds *CommandScripting, evts *EventScripting) *EntityBinding {
	return newEntityBinding(entity, cmds, evts)
}

// SendCommand dispatches a command to this entity.
func (b *EntityBinding) SendCommand(action string, params map[string]any) (string, error) {
	return b.commands.Send(b.Entity, action, params)
}

// SendEvent publishes a state-change event for this entity.
func (b *EntityBinding) SendEvent(payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env := types.EntityEventEnvelope{
		PluginID:  b.Entity.PluginID,
		DeviceID:  b.Entity.DeviceID,
		EntityID:  b.Entity.ID,
		EntityType: b.Entity.Domain,
		Payload:   raw,
		CreatedAt: time.Now().UTC(),
	}
	return b.events.Publish(env)
}

// OnEvent subscribes to events for this entity (subject defaults to "entityID.*").
// Cancels when ctx is done.
func (b *EntityBinding) OnEvent(ctx context.Context, fn HandlerFunc) (Subscription, error) {
	subject := b.Entity.ID + ".*"
	return b.events.OnEvent(ctx, subject, fn)
}

// OnCommand registers a callback for the named command. Overwrites any prior
// handler registered for the same name.
func (b *EntityBinding) OnCommand(commandName string, fn func(IncomingCommand)) {
	if b.commandHandlers == nil {
		b.commandHandlers = make(map[string]func(IncomingCommand))
	}
	b.commandHandlers[commandName] = fn
}

// HandleCommand delivers an incoming command to the registered per-name callback.
// Returns ErrNoCommandHandler if no callback is registered for that command name.
func (b *EntityBinding) HandleCommand(name string, params map[string]any) error {
	if fn, ok := b.commandHandlers[name]; ok {
		fn(IncomingCommand{Name: name, Params: params})
		return nil
	}
	return ErrNoCommandHandler
}

// GetField returns a value from the entity's effective state by key.
func (b *EntityBinding) GetField(key string) (any, bool) {
	if len(b.Entity.Data.Effective) == 0 {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(b.Entity.Data.Effective, &m) != nil {
		return nil, false
	}
	v, ok := m[key]
	return v, ok
}

// GetState returns the entire effective state as a map.
func (b *EntityBinding) GetState() map[string]any {
	if len(b.Entity.Data.Effective) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b.Entity.Data.Effective, &m)
	return m
}

// Sentinel errors.
var ErrNoCommandHandler = fmt.Errorf("scripting: no OnCommand handler registered")
