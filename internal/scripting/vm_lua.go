package scripting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Lua VM — binds all scripting APIs into the gopher-lua state.
// ---------------------------------------------------------------------------

// LuaVM wraps a VM and adds a live *lua.LState. Created via NewLuaVM.
type LuaVM struct {
	*VM
	L        *lua.LState
	stopOnce sync.Once
}

// NewLuaVM creates a VM with a fully initialised Lua state, injects all
// scripting bindings, and calls OnInit. The caller must call Stop().
func NewLuaVM(entity types.Entity, source string, svc Services) (*LuaVM, error) {
	return newLuaVM(entity, source, svc)
}

// newLuaVM is the real constructor.
func newLuaVM(entity types.Entity, source string, svc Services) (*LuaVM, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmds := newCommandScripting(svc.Commands)
	evts := newEventScriptingWithFinder(svc.Bus, svc.Finder)

	L := lua.NewState()

	lvm := &LuaVM{
		VM: &VM{
			entity:   entity,
			source:   source,
			svc:      svc,
			ctx:      ctx,
			cancel:   cancel,
			work:     make(chan workItem, 64),
			done:     make(chan struct{}),
			This:     newEntityBinding(entity, cmds, evts),
			Query:    newQueryScripting(svc.Finder),
			Commands: cmds,
			Events:   evts,
		},
		L: L,
	}

	// Wire runOnInit to our Lua executor.
	lvm.VM.runOnInit = lvm.execOnInit

	// Inject all Lua bindings.
	lvm.injectBindings()

	// Start the work-queue goroutine.
	go lvm.VM.loop()

	// Execute the source + call OnInit under a deadline.
	errc := make(chan error, 1)
	lvm.VM.work <- workItem{
		fn:   lvm.execOnInit,
		errc: errc,
	}
	select {
	case err := <-errc:
		if err != nil {
			lvm.VM.cancel()
			<-lvm.VM.done
			L.Close()
			return nil, err
		}
	case <-time.After(defaultDeadline):
		lvm.VM.cancel()
		<-lvm.VM.done
		L.Close()
		return nil, fmt.Errorf("scripting: Lua OnInit timeout for entity %s", entity.ID)
	}

	lvm.VM.started = true
	return lvm, nil
}

// Stop cancels the context and closes the Lua state. Idempotent.
func (lvm *LuaVM) Stop() {
	lvm.stopOnce.Do(func() {
		lvm.VM.Stop()
		lvm.L.Close()
	})
}

// execOnInit runs the source and calls OnInit(ctx) if defined.
// Must be called on the work-queue goroutine.
func (lvm *LuaVM) execOnInit() error {
	ctx, cancel := context.WithTimeout(lvm.VM.ctx, defaultDeadline)
	lvm.L.SetContext(ctx)
	// Restore the long-lived context after OnInit so subsequent event callbacks
	// are not silently dropped by a cancelled context.
	defer func() {
		cancel()
		lvm.L.SetContext(lvm.VM.ctx)
	}()

	if err := lvm.L.DoString(lvm.VM.source); err != nil {
		return fmt.Errorf("scripting: Lua parse/exec error: %w", err)
	}

	// Call OnInit(ctx_table) if defined.
	initFn := lvm.L.GetGlobal("OnInit")
	if initFn == lua.LNil {
		return nil
	}
	ctxTable := lvm.newCtxTable()
	if err := lvm.L.CallByParam(lua.P{
		Fn:      initFn,
		NRet:    0,
		Protect: true,
	}, ctxTable); err != nil {
		return fmt.Errorf("scripting: Lua OnInit error: %w", err)
	}
	return nil
}

// ExecLua runs arbitrary Lua source on the VM's goroutine and returns the
// top-of-stack string result. Safe to call from any goroutine.
func (lvm *LuaVM) ExecLua(src string) (string, error) {
	var result string
	err := lvm.VM.Exec(func() error {
		ctx, cancel := context.WithTimeout(lvm.VM.ctx, defaultDeadline)
		lvm.L.SetContext(ctx)
		defer func() {
			cancel()
			lvm.L.SetContext(lvm.VM.ctx)
		}()

		top := lvm.L.GetTop()
		if err := lvm.L.DoString(src); err != nil {
			return fmt.Errorf("scripting: %w", err)
		}
		if lvm.L.GetTop() > top {
			result = lvm.L.ToStringMeta(lvm.L.Get(-1)).String()
			lvm.L.SetTop(top) // pop result
		}
		return nil
	})
	return result, err
}

// GetGlobalNumber reads a Lua global as float64. Treats nil as 0. Thread-safe.
func (lvm *LuaVM) GetGlobalNumber(name string) (float64, error) {
	var result float64
	err := lvm.VM.Exec(func() error {
		v := lvm.L.GetGlobal(name)
		switch n := v.(type) {
		case lua.LNumber:
			result = float64(n)
		case *lua.LNilType:
			result = 0
		default:
			return fmt.Errorf("scripting: global %q is %T, expected number", name, v)
		}
		return nil
	})
	return result, err
}

// GetGlobalString reads a Lua global as a string. Treats nil as "". Thread-safe.
func (lvm *LuaVM) GetGlobalString(name string) (string, error) {
	var result string
	err := lvm.VM.Exec(func() error {
		v := lvm.L.GetGlobal(name)
		switch s := v.(type) {
		case lua.LString:
			result = string(s)
		case *lua.LNilType:
			result = ""
		default:
			return fmt.Errorf("scripting: global %q is %T, expected string", name, v)
		}
		return nil
	})
	return result, err
}

// GetGlobalBool reads a Lua global as a bool. Treats nil as false. Thread-safe.
func (lvm *LuaVM) GetGlobalBool(name string) (bool, error) {
	var result bool
	err := lvm.VM.Exec(func() error {
		v := lvm.L.GetGlobal(name)
		switch b := v.(type) {
		case lua.LBool:
			result = bool(b)
		case *lua.LNilType:
			result = false
		default:
			return fmt.Errorf("scripting: global %q is %T, expected bool", name, v)
		}
		return nil
	})
	return result, err
}

// GetGlobalTableLen reads the length of a Lua table global. Thread-safe.
func (lvm *LuaVM) GetGlobalTableLen(name string) (int, error) {
	var result int
	err := lvm.VM.Exec(func() error {
		v := lvm.L.GetGlobal(name)
		t, ok := v.(*lua.LTable)
		if !ok {
			return fmt.Errorf("scripting: global %q is %T, expected table", name, v)
		}
		result = t.Len()
		return nil
	})
	return result, err
}

// HandleCommand delivers a command to this VM's EntityBinding on the work queue.
func (lvm *LuaVM) HandleCommand(name string, params map[string]any) error {
	return lvm.VM.Exec(func() error {
		return lvm.VM.This.HandleCommand(name, params)
	})
}

func (lvm *LuaVM) injectBindings() {
	L := lvm.L

	// ParseLabels — types.ParseLabels exposed directly.
	L.SetGlobal("ParseLabels", L.NewFunction(lvm.luaParseLabels))

	// This — EntityBinding
	lvm.injectThis()

	// QueryService.Scripting
	lvm.injectQueryService()

	// CommandService.Scripting
	lvm.injectCommandService()

	// EventService.Scripting
	lvm.injectEventService()
}

// ---------------------------------------------------------------------------
// This binding
// ---------------------------------------------------------------------------

func (lvm *LuaVM) injectThis() {
	L := lvm.L
	this := lvm.VM.This

	t := L.NewTable()

	// Fields
	L.SetField(t, "ID", lua.LString(this.Entity.ID))
	L.SetField(t, "PluginID", lua.LString(this.Entity.PluginID))
	L.SetField(t, "DeviceID", lua.LString(this.Entity.DeviceID))
	L.SetField(t, "Domain", lua.LString(this.Entity.Domain))

	// This.SendCommand(action, params_table)
	L.SetField(t, "SendCommand", L.NewFunction(func(L *lua.LState) int {
		action := L.CheckString(1)
		params := tableToMap(L, L.OptTable(2, L.NewTable()))
		cmdID, err := this.SendCommand(action, params)
		if err != nil {
			L.RaiseError("This.SendCommand: %s", err)
			return 0
		}
		L.Push(lua.LString(cmdID))
		return 1
	}))

	// This.SendEvent(payload_table)
	L.SetField(t, "SendEvent", L.NewFunction(func(L *lua.LState) int {
		payload := tableToMap(L, L.CheckTable(1))
		if err := this.SendEvent(payload); err != nil {
			L.RaiseError("This.SendEvent: %s", err)
		}
		return 0
	}))

	// This.OnEvent(ctx, subject, fn) or This.OnEvent(ctx, fn) for entity's own events
	L.SetField(t, "OnEvent", L.NewFunction(func(L *lua.LState) int {
		subject := this.Entity.ID + ".*"
		var fn *lua.LFunction
		if L.GetTop() == 2 {
			// OnEvent(ctx, fn) — own entity events
			fn = L.CheckFunction(2)
		} else {
			// OnEvent(ctx, subject, fn)
			subject = L.CheckString(2)
			fn = L.CheckFunction(3)
		}
		lvm.subscribeEventHandler(subject, fn)
		return 0
	}))

	// This.OnCommand(commandName, fn) — register per-command handler
	L.SetField(t, "OnCommand", L.NewFunction(func(L *lua.LState) int {
		cmdName := L.CheckString(1)
		fn := L.CheckFunction(2)
		this.OnCommand(cmdName, func(cmd IncomingCommand) {
			lvm.VM.EnqueueEvent(func() error {
				cmdTable := incomingCommandToTable(L, cmd)
				return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, cmdTable)
			})
		})
		return 0
	}))

	// This.GetState() → table
	L.SetField(t, "GetState", L.NewFunction(func(L *lua.LState) int {
		m := this.GetState()
		if m == nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(mapToTable(L, m))
		return 1
	}))

	// This.GetField(key) → value
	L.SetField(t, "GetField", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		v, ok := this.GetField(key)
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(goToLua(L, v))
		return 1
	}))

	L.SetGlobal("This", t)
}

// ---------------------------------------------------------------------------
// QueryService.Scripting
// ---------------------------------------------------------------------------

func (lvm *LuaVM) injectQueryService() {
	L := lvm.L
	qs := lvm.VM.Query

	scripting := L.NewTable()

	// QueryService.Scripting.Find(queryStr) → EntityList (table with :each, :first, :count, :where)
	L.SetField(scripting, "Find", L.NewFunction(func(L *lua.LState) int {
		q := L.CheckString(1)
		list, err := qs.Find(q)
		if err != nil {
			L.RaiseError("QueryService.Scripting.Find: %s", err)
			return 0
		}
		L.Push(lvm.entityListToTable(list))
		return 1
	}))

	// QueryService.Scripting.FindOne(queryStr) → entity table or nil
	L.SetField(scripting, "FindOne", L.NewFunction(func(L *lua.LState) int {
		q := L.CheckString(1)
		e, err := qs.FindOne(q)
		if err != nil {
			L.RaiseError("QueryService.Scripting.FindOne: %s", err)
			return 0
		}
		if e == nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(entityToTable(L, *e))
		return 1
	}))

	scriptingParent := L.NewTable()
	L.SetField(scriptingParent, "Scripting", scripting)
	L.SetGlobal("QueryService", scriptingParent)
}

// ---------------------------------------------------------------------------
// CommandService.Scripting
// ---------------------------------------------------------------------------

func (lvm *LuaVM) injectCommandService() {
	L := lvm.L
	cs := lvm.VM.Commands

	scripting := L.NewTable()

	// CommandService.Scripting.Send(entity_table, action, params_table) → cmdID
	L.SetField(scripting, "Send", L.NewFunction(func(L *lua.LState) int {
		eTable := L.CheckTable(1)
		action := L.CheckString(2)
		params := tableToMap(L, L.OptTable(3, L.NewTable()))

		e := tableToEntity(L, eTable)
		cmdID, err := cs.Send(e, action, params)
		if err != nil {
			L.RaiseError("CommandService.Scripting.Send: %s", err)
			return 0
		}
		L.Push(lua.LString(cmdID))
		return 1
	}))

	// CommandService.Scripting.SendTo(pluginID, deviceID, entityID, action, params) → cmdID
	L.SetField(scripting, "SendTo", L.NewFunction(func(L *lua.LState) int {
		pluginID := L.CheckString(1)
		deviceID := L.CheckString(2)
		entityID := L.CheckString(3)
		action := L.CheckString(4)
		params := tableToMap(L, L.OptTable(5, L.NewTable()))

		cmdID, err := cs.SendTo(pluginID, deviceID, entityID, action, params)
		if err != nil {
			L.RaiseError("CommandService.Scripting.SendTo: %s", err)
			return 0
		}
		L.Push(lua.LString(cmdID))
		return 1
	}))

	scriptingParent := L.NewTable()
	L.SetField(scriptingParent, "Scripting", scripting)
	L.SetGlobal("CommandService", scriptingParent)
}

// ---------------------------------------------------------------------------
// EventService.Scripting
// ---------------------------------------------------------------------------

func (lvm *LuaVM) injectEventService() {
	L := lvm.L

	scripting := L.NewTable()

	// EventService.Scripting.OnEvent(ctx, subject, fn)
	L.SetField(scripting, "OnEvent", L.NewFunction(func(L *lua.LState) int {
		// ctx is arg 1 (the Lua ctx table, currently ignored — VM lifetime handles cleanup)
		subject := L.CheckString(2)
		fn := L.CheckFunction(3)
		lvm.subscribeEventHandler(subject, fn)
		return 0
	}))

	// EventService.Scripting.Publish(ctx, entityID, eventType, params_table)
	L.SetField(scripting, "Publish", L.NewFunction(func(L *lua.LState) int {
		// arg 1: ctx (ignored — VM lifetime handles cleanup)
		entityID := L.CheckString(2)
		eventType := L.CheckString(3)
		params := tableToMap(L, L.OptTable(4, L.NewTable()))
		params["type"] = eventType

		payload, err := json.Marshal(params)
		if err != nil {
			L.RaiseError("EventService.Scripting.Publish: %s", err)
			return 0
		}
		env := types.EntityEventEnvelope{
			EntityID:  entityID,
			Payload:   payload,
			CreatedAt: time.Now().UTC(),
		}
		if err := lvm.VM.Events.Publish(env); err != nil {
			L.RaiseError("EventService.Scripting.Publish: %s", err)
		}
		return 0
	}))

	scriptingParent := L.NewTable()
	L.SetField(scriptingParent, "Scripting", scripting)
	L.SetGlobal("EventService", scriptingParent)
}

// subscribeEventHandler registers a NATS subscription that calls fn on the
// VM's work queue. The subscription lives until the VM's ctx is cancelled.
func (lvm *LuaVM) subscribeEventHandler(subject string, fn *lua.LFunction) {
	_, err := lvm.VM.Events.OnEvent(lvm.VM.ctx, subject, func(env types.EntityEventEnvelope) {
		lvm.VM.EnqueueEvent(func() error {
			envTable := envelopeToTable(lvm.L, env)
			return lvm.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, envTable)
		})
	})
	if err != nil {
		// Non-fatal: log or surface via Lua error on next call.
		_ = err
	}
}

// newCtxTable returns a Lua table representing the VM's lifetime context.
func (lvm *LuaVM) newCtxTable() *lua.LTable {
	t := lvm.L.NewTable()
	lvm.L.SetField(t, "_vm", lua.LTrue) // marker
	return t
}

// ---------------------------------------------------------------------------
// Lua ↔ Go conversion helpers
// ---------------------------------------------------------------------------

func tableToMap(L *lua.LState, t *lua.LTable) map[string]any {
	m := map[string]any{}
	t.ForEach(func(k, v lua.LValue) {
		key := k.String()
		m[key] = luaToGo(v)
	})
	return m
}

func luaToGo(v lua.LValue) any {
	switch x := v.(type) {
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		f := float64(x)
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	case lua.LString:
		return string(x)
	case *lua.LTable:
		m := map[string]any{}
		x.ForEach(func(k, val lua.LValue) {
			m[k.String()] = luaToGo(val)
		})
		return m
	default:
		return nil
	}
}

func goToLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case int:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case map[string]any:
		return mapToTable(L, x)
	default:
		return lua.LNil
	}
}

func mapToTable(L *lua.LState, m map[string]any) *lua.LTable {
	t := L.NewTable()
	for k, v := range m {
		L.SetField(t, k, goToLua(L, v))
	}
	return t
}

func entityToTable(L *lua.LState, e types.Entity) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "ID", lua.LString(e.ID))
	L.SetField(t, "PluginID", lua.LString(e.PluginID))
	L.SetField(t, "DeviceID", lua.LString(e.DeviceID))
	L.SetField(t, "Domain", lua.LString(e.Domain))
	if len(e.Data.Effective) > 0 {
		var m map[string]any
		if json.Unmarshal(e.Data.Effective, &m) == nil {
			L.SetField(t, "State", mapToTable(L, m))
		}
	}
	return t
}

func tableToEntity(_ *lua.LState, t *lua.LTable) types.Entity {
	return types.Entity{
		ID:       luaFieldStr(t, "ID"),
		PluginID: luaFieldStr(t, "PluginID"),
		DeviceID: luaFieldStr(t, "DeviceID"),
		Domain:   luaFieldStr(t, "Domain"),
	}
}

func luaFieldStr(t *lua.LTable, key string) string {
	v := t.RawGetString(key)
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}
	return ""
}

func envelopeToTable(L *lua.LState, env types.EntityEventEnvelope) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "EventID", lua.LString(env.EventID))
	L.SetField(t, "PluginID", lua.LString(env.PluginID))
	L.SetField(t, "DeviceID", lua.LString(env.DeviceID))
	L.SetField(t, "EntityID", lua.LString(env.EntityID))
	L.SetField(t, "EntityType", lua.LString(env.EntityType))
	L.SetField(t, "CorrelationID", lua.LString(env.CorrelationID))
	if len(env.Payload) > 0 {
		var m map[string]any
		if json.Unmarshal(env.Payload, &m) == nil {
			L.SetField(t, "Payload", mapToTable(L, m))
		}
	}
	return t
}

func incomingCommandToTable(L *lua.LState, cmd IncomingCommand) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "Name", lua.LString(cmd.Name))
	if cmd.Params != nil {
		L.SetField(t, "Params", mapToTable(L, cmd.Params))
	} else {
		L.SetField(t, "Params", L.NewTable())
	}
	return t
}

func (lvm *LuaVM) entityListToTable(list EntityList) *lua.LTable {
	L := lvm.L
	t := L.NewTable()

	// Populate array part.
	for i, e := range list {
		L.RawSetInt(t, i+1, entityToTable(L, e))
	}

	mt := L.NewTable()
	L.SetField(mt, "__index", mt)

	// :each(fn)
	L.SetField(mt, "each", L.NewFunction(func(L *lua.LState) int {
		self := L.CheckTable(1)
		fn := L.CheckFunction(2)
		n := self.MaxN()
		for i := 1; i <= n; i++ {
			e := self.RawGetInt(i)
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, e); err != nil {
				L.RaiseError("each: %s", err)
			}
		}
		return 0
	}))

	// :count()
	L.SetField(mt, "count", L.NewFunction(func(L *lua.LState) int {
		self := L.CheckTable(1)
		L.Push(lua.LNumber(self.MaxN()))
		return 1
	}))

	// :first()
	L.SetField(mt, "first", L.NewFunction(func(L *lua.LState) int {
		self := L.CheckTable(1)
		v := self.RawGetInt(1)
		L.Push(v)
		return 1
	}))

	// :where(fn)
	L.SetField(mt, "where", L.NewFunction(func(L *lua.LState) int {
		self := L.CheckTable(1)
		fn := L.CheckFunction(2)
		out := L.NewTable()
		n := self.MaxN()
		idx := 1
		for i := 1; i <= n; i++ {
			e := self.RawGetInt(i)
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, e); err != nil {
				L.RaiseError("where: %s", err)
			}
			if L.ToBool(-1) {
				L.RawSetInt(out, idx, e)
				idx++
			}
			L.Pop(1)
		}
		L.SetMetatable(out, mt)
		L.Push(out)
		return 1
	}))

	L.SetMetatable(t, mt)
	return t
}

// luaParseLabels exposes types.ParseLabels to Lua.
// Accepts a table of "Key:Value" strings, returns a table of {Key: {Value}}.
func (lvm *LuaVM) luaParseLabels(L *lua.LState) int {
	t := L.CheckTable(1)
	pairs := []string{}
	t.ForEach(func(_, v lua.LValue) {
		if s, ok := v.(lua.LString); ok {
			pairs = append(pairs, string(s))
		}
	})
	m := types.ParseLabels(pairs)
	out := L.NewTable()
	for k, vs := range m {
		vt := L.NewTable()
		for i, v := range vs {
			L.RawSetInt(vt, i+1, lua.LString(v))
		}
		L.SetField(out, k, vt)
	}
	L.Push(out)
	return 1
}

// keep strings import referenced
var _ = strings.Contains
