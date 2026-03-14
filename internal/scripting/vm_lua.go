package scripting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	lightdomain "github.com/slidebolt/sdk-entities/light"
	lightstripdomain "github.com/slidebolt/sdk-entities/light_strip"
	"github.com/slidebolt/sdk-types"
	lua "github.com/yuin/gopher-lua"
)

// ---------------------------------------------------------------------------
// Lua VM — binds all scripting APIs into the gopher-lua state.
// ---------------------------------------------------------------------------

// LuaVM wraps a VM and adds a live *lua.LState. Created via NewLuaVM.
type LuaVM struct {
	*VM
	L              *lua.LState
	stopOnce       sync.Once
	definedScripts map[string]*lua.LFunction
	entrypoint     string
}

type ScriptInstance struct {
	Entrypoint string
	ScriptRef  string
	SessionID  string
}

type stripMemberRef struct {
	PluginID string `json:"plugin_id"`
	DeviceID string `json:"device_id"`
	EntityID string `json:"entity_id"`
	Index    int    `json:"index"`
}

// NewLuaVM creates a VM with a fully initialised Lua state, injects all
// scripting bindings, and calls OnInit. The caller must call Stop().
func NewLuaVM(entity types.Entity, source string, svc Services) (*LuaVM, error) {
	return newLuaVM(entity, source, ScriptInstance{}, svc)
}

// NewLuaVMWithEntrypoint creates a VM and invokes the named script entry
// registered via DefineScript after the source is loaded.
func NewLuaVMWithEntrypoint(entity types.Entity, source, entrypoint string, svc Services) (*LuaVM, error) {
	return newLuaVM(entity, source, ScriptInstance{Entrypoint: entrypoint}, svc)
}

// NewLuaVMWithInstance creates a VM with child-script instance metadata.
func NewLuaVMWithInstance(entity types.Entity, source string, inst ScriptInstance, svc Services) (*LuaVM, error) {
	return newLuaVM(entity, source, inst, svc)
}

// newLuaVM is the real constructor.
func newLuaVM(entity types.Entity, source string, inst ScriptInstance, svc Services) (*LuaVM, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmds := newCommandScripting(svc.Commands)
	evts := newEventScriptingWithFinder(svc.Bus, svc.Finder)

	L := lua.NewState()

	lvm := &LuaVM{
		VM: &VM{
			entity:    entity,
			source:    source,
			scriptRef: inst.ScriptRef,
			sessionID: inst.SessionID,
			svc:       svc,
			ctx:       ctx,
			cancel:    cancel,
			work:      make(chan workItem, 64),
			done:      make(chan struct{}),
			This:      newEntityBinding(entity, cmds, evts),
			Query:     newQueryScripting(svc.Finder),
			Commands:  cmds,
			Events:    evts,
		},
		L:              L,
		definedScripts: make(map[string]*lua.LFunction),
		entrypoint:     inst.Entrypoint,
	}
	lvm.VM.Timers = newTimerScripting(lvm.VM, svc.Timers)

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

	chunkName := "script:" + lvm.VM.entity.ID
	fn, err := lvm.L.Load(strings.NewReader(lvm.VM.source), chunkName)
	if err != nil {
		return fmt.Errorf("scripting: Lua parse error in %s: %w", chunkName, err)
	}
	lvm.L.Push(fn)
	if err := lvm.L.PCall(0, lua.MultRet, nil); err != nil {
		return fmt.Errorf("scripting: Lua exec error in %s: %w", chunkName, err)
	}

	if lvm.entrypoint != "" {
		entryFn, ok := lvm.definedScripts[lvm.entrypoint]
		if !ok {
			return fmt.Errorf("scripting: entrypoint %q not defined in %s", lvm.entrypoint, chunkName)
		}
		if err := lvm.L.CallByParam(lua.P{Fn: entryFn, NRet: 0, Protect: true}); err != nil {
			return fmt.Errorf("scripting: Lua entrypoint error: %w", err)
		}
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
		chunkName := "script:" + lvm.VM.entity.ID
		fn, err := lvm.L.Load(strings.NewReader(src), chunkName)
		if err != nil {
			return fmt.Errorf("scripting: %w", err)
		}
		lvm.L.Push(fn)
		if err := lvm.L.PCall(0, lua.MultRet, nil); err != nil {
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

	// Override global print to use slog.
	L.SetGlobal("print", L.NewFunction(lvm.luaPrint))

	// ParseLabels — types.ParseLabels exposed directly.
	L.SetGlobal("ParseLabels", L.NewFunction(lvm.luaParseLabels))

	// DefineScript(name, fn) registers a named entrypoint within this source.
	L.SetGlobal("DefineScript", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		fn := L.CheckFunction(2)
		lvm.definedScripts[name] = fn
		return 0
	}))

	// This — EntityBinding
	lvm.injectThis()

	// Domain helpers — injected based on entity domain.
	lvm.injectDomainHelpers()

	// QueryService.Scripting
	lvm.injectQueryService()

	// CommandService.Scripting
	lvm.injectCommandService()

	// EventService.Scripting
	lvm.injectEventService()

	// LogService.Scripting
	lvm.injectLogService()

	// TimerService.Scripting
	lvm.injectTimerService()
}

func (lvm *LuaVM) injectDomainHelpers() {
	switch lvm.VM.entity.Domain {
	case lightdomain.Type:
		lvm.injectLightHelpers()
	case lightstripdomain.Type:
		lvm.injectStripHelpers()
	}
}

func (lvm *LuaVM) injectLightHelpers() {
	L := lvm.L
	light := L.NewTable()
	this := lvm.VM.This

	send := func(action string, params map[string]any) int {
		cmdID, err := this.SendCommand(action, params)
		if err != nil {
			L.RaiseError("Light.%s: %s", action, err)
			return 0
		}
		L.Push(lua.LString(cmdID))
		return 1
	}

	L.SetField(light, "On", L.NewFunction(func(L *lua.LState) int {
		return send(lightdomain.ActionTurnOn, nil)
	}))
	L.SetField(light, "Off", L.NewFunction(func(L *lua.LState) int {
		return send(lightdomain.ActionTurnOff, nil)
	}))
	L.SetField(light, "SetBrightness", L.NewFunction(func(L *lua.LState) int {
		return send(lightdomain.ActionSetBrightness, map[string]any{"brightness": int(L.CheckNumber(1))})
	}))
	L.SetField(light, "SetRGB", L.NewFunction(func(L *lua.LState) int {
		return send(lightdomain.ActionSetRGB, map[string]any{"rgb": intsFromLuaTable(L, L.CheckTable(1))})
	}))
	L.SetField(light, "SetTemperature", L.NewFunction(func(L *lua.LState) int {
		return send(lightdomain.ActionSetTemperature, map[string]any{"temperature": int(L.CheckNumber(1))})
	}))
	L.SetField(light, "SoftWhite", L.NewFunction(func(L *lua.LState) int {
		brightness := int(L.CheckNumber(1))
		if _, err := this.SendCommand(lightdomain.ActionSetBrightness, map[string]any{"brightness": brightness}); err != nil {
			L.RaiseError("Light.SoftWhite brightness: %s", err)
			return 0
		}
		cmdID, err := this.SendCommand(lightdomain.ActionSetTemperature, map[string]any{"temperature": 3000})
		if err != nil {
			L.RaiseError("Light.SoftWhite temperature: %s", err)
			return 0
		}
		L.Push(lua.LString(cmdID))
		return 1
	}))

	L.SetGlobal("Light", light)
}

func (lvm *LuaVM) injectStripHelpers() {
	L := lvm.L
	strip := L.NewTable()

	sendToMembers := func(action string, params map[string]any) int {
		members, err := lvm.stripMembers()
		if err != nil {
			L.RaiseError("Strip.%s: %s", action, err)
			return 0
		}
		lastID := ""
		for _, member := range members {
			cmdID, err := lvm.VM.Commands.SendTo(member.PluginID, member.DeviceID, member.EntityID, action, params)
			if err != nil {
				L.RaiseError("Strip.%s member %s/%s/%s: %s", action, member.PluginID, member.DeviceID, member.EntityID, err)
				return 0
			}
			lastID = cmdID
		}
		L.Push(lua.LString(lastID))
		return 1
	}

	sendToSegment := func(index int, action string, params map[string]any) int {
		members, err := lvm.stripMembers()
		if err != nil {
			L.RaiseError("Strip.%s: %s", action, err)
			return 0
		}
		for _, member := range members {
			if member.Index != index {
				continue
			}
			cmdID, err := lvm.VM.Commands.SendTo(member.PluginID, member.DeviceID, member.EntityID, action, params)
			if err != nil {
				L.RaiseError("Strip.%s member %s/%s/%s: %s", action, member.PluginID, member.DeviceID, member.EntityID, err)
				return 0
			}
			L.Push(lua.LString(cmdID))
			return 1
		}
		L.RaiseError("Strip.%s: no member at index %d", action, index)
		return 0
	}

	L.SetField(strip, "On", L.NewFunction(func(L *lua.LState) int {
		return sendToMembers(lightdomain.ActionTurnOn, nil)
	}))
	L.SetField(strip, "Off", L.NewFunction(func(L *lua.LState) int {
		return sendToMembers(lightdomain.ActionTurnOff, nil)
	}))
	L.SetField(strip, "SetBrightness", L.NewFunction(func(L *lua.LState) int {
		return sendToMembers(lightdomain.ActionSetBrightness, map[string]any{"brightness": int(L.CheckNumber(1))})
	}))
	L.SetField(strip, "SetRGB", L.NewFunction(func(L *lua.LState) int {
		return sendToMembers(lightdomain.ActionSetRGB, map[string]any{"rgb": intsFromLuaTable(L, L.CheckTable(1))})
	}))
	L.SetField(strip, "Fill", L.NewFunction(func(L *lua.LState) int {
		return sendToMembers(lightdomain.ActionSetRGB, map[string]any{"rgb": intsFromLuaTable(L, L.CheckTable(1))})
	}))
	L.SetField(strip, "SetSegment", L.NewFunction(func(L *lua.LState) int {
		index := int(L.CheckNumber(1))
		rgb := intsFromLuaTable(L, L.CheckTable(2))
		return sendToSegment(index, lightdomain.ActionSetRGB, map[string]any{"rgb": rgb})
	}))
	L.SetField(strip, "Length", L.NewFunction(func(L *lua.LState) int {
		members, err := lvm.stripMembers()
		if err != nil {
			L.Push(lua.LNumber(0))
			return 1
		}
		L.Push(lua.LNumber(len(members)))
		return 1
	}))

	L.SetGlobal("Strip", strip)
}

func (lvm *LuaVM) stripMembers() ([]stripMemberRef, error) {
	raw, ok := lvm.VM.entity.Meta["strip_members"]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("entity %s has no strip_members meta", lvm.VM.entity.ID)
	}
	var members []stripMemberRef
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	return members, nil
}

func intsFromLuaTable(L *lua.LState, t *lua.LTable) []int {
	out := make([]int, 0, t.Len())
	t.ForEach(func(_ lua.LValue, v lua.LValue) {
		out = append(out, int(lua.LVAsNumber(v)))
	})
	return out
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

	// This.Log(msg, [params_table])
	L.SetField(t, "Log", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		params := tableToMap(L, L.OptTable(2, L.NewTable()))
		lvm.log(slog.LevelInfo, msg, params)
		return 0
	}))

	// This.SendCommand(action, params_table, opts_table)
	// opts: {failOnError=false} — when false (default), unsupported actions are silently skipped
	L.SetField(t, "SendCommand", L.NewFunction(func(L *lua.LState) int {
		action := L.CheckString(1)
		params := tableToMap(L, L.OptTable(2, L.NewTable()))
		opts := L.OptTable(3, L.NewTable())
		failOnError := opts.RawGetString("failOnError") == lua.LTrue
		if !failOnError && !entitySupportsActionLua(this.Entity.Actions, action) {
			return 0 // silently skip
		}
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

	// This.OnEvent(subject, fn) or This.OnEvent(fn) for entity's own events
	L.SetField(t, "OnEvent", L.NewFunction(func(L *lua.LState) int {
		top := L.GetTop()
		var subject string
		var fn *lua.LFunction

		// Support colon call (self is arg 1)
		argOffset := 0
		if top > 0 && L.Get(1).Type() == lua.LTTable {
			argOffset = 1
		}

		if top-argOffset == 1 {
			// OnEvent(fn)
			subject = this.Entity.ID + ".*"
			fn = L.CheckFunction(argOffset + 1)
		} else {
			// OnEvent(subject, fn)
			subject = L.CheckString(argOffset + 1)
			fn = L.CheckFunction(argOffset + 2)
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

	L.SetField(t, "SessionID", L.NewFunction(func(L *lua.LState) int {
		if lvm.VM.sessionID == "" {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(lua.LString(lvm.VM.sessionID))
		return 1
	}))

	L.SetField(t, "LoadSession", L.NewFunction(func(L *lua.LState) int {
		if lvm.VM.sessionID == "" || lvm.VM.svc.Sessions == nil {
			L.Push(lua.LNil)
			return 1
		}
		state, ok := lvm.VM.svc.Sessions.LoadSession(lvm.VM.sessionID)
		if !ok || state == nil {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(mapToTable(L, state))
		return 1
	}))

	L.SetField(t, "SaveSession", L.NewFunction(func(L *lua.LState) int {
		if lvm.VM.sessionID == "" || lvm.VM.svc.Sessions == nil {
			L.RaiseError("This.SaveSession: no active session")
			return 0
		}
		payload := tableToMap(L, L.CheckTable(1))
		if err := lvm.VM.svc.Sessions.SaveSession(lvm.VM.sessionID, payload); err != nil {
			L.RaiseError("This.SaveSession: %s", err)
			return 0
		}
		return 0
	}))

	// This.RunScript(name) -> instanceID
	L.SetField(t, "RunScript", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if lvm.VM.svc.Scripts == nil {
			L.RaiseError("This.RunScript: script controller unavailable")
			return 0
		}
		instanceID, err := lvm.VM.svc.Scripts.Run(this.Entity, name)
		if err != nil {
			L.RaiseError("This.RunScript: %s", err)
			return 0
		}
		L.Push(lua.LString(instanceID))
		return 1
	}))

	// This.StopScript(instanceID)
	L.SetField(t, "StopScript", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		if lvm.VM.svc.Scripts == nil {
			L.RaiseError("This.StopScript: script controller unavailable")
			return 0
		}
		if err := lvm.VM.svc.Scripts.StopScript(this.Entity, instanceID); err != nil {
			L.RaiseError("This.StopScript: %s", err)
			return 0
		}
		return 0
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

	// CommandService.Scripting.Send(entity_table, action, params_table, opts_table) → cmdID
	// opts: {failOnError=false} — when false (default), unsupported actions are silently skipped
	L.SetField(scripting, "Send", L.NewFunction(func(L *lua.LState) int {
		eTable := L.CheckTable(1)
		action := L.CheckString(2)
		params := tableToMap(L, L.OptTable(3, L.NewTable()))
		opts := L.OptTable(4, L.NewTable())
		failOnError := opts.RawGetString("failOnError") == lua.LTrue

		// Check action support via Actions table on the entity table
		if !failOnError {
			actionsVal := eTable.RawGetString("Actions")
			if at, ok := actionsVal.(*lua.LTable); ok {
				supported := false
				at.ForEach(func(_, v lua.LValue) {
					if lua.LString(action) == v {
						supported = true
					}
				})
				if !supported {
					return 0 // silently skip
				}
			}
		}

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
		lvm.log(slog.LevelDebug, "NATS event received by scripting bridge", map[string]any{
			"subject":   subject,
			"entity_id": env.EntityID,
		})
		lvm.VM.EnqueueEvent(func() error {
			lvm.log(slog.LevelDebug, "Delivering event to Lua callback", map[string]any{
				"entity_id": env.EntityID,
			})
			envTable := envelopeToTable(lvm.L, env)
			return lvm.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, envTable)
		})
	})
	if err != nil {
		lvm.log(slog.LevelError, "Failed to subscribe to events in Lua", map[string]any{"err": err, "subject": subject})
	}
}

// newCtxTable returns a Lua table representing the VM's lifetime context.
func (lvm *LuaVM) newCtxTable() *lua.LTable {
	t := lvm.L.NewTable()
	lvm.L.SetField(t, "_vm", lua.LTrue) // marker
	return t
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
// LogService.Scripting
// ---------------------------------------------------------------------------

func (lvm *LuaVM) injectLogService() {
	L := lvm.L
	scripting := L.NewTable()

	L.SetField(scripting, "Info", L.NewFunction(func(L *lua.LState) int {
		lvm.log(slog.LevelInfo, L.CheckString(1), tableToMap(L, L.OptTable(2, L.NewTable())))
		return 0
	}))
	L.SetField(scripting, "Warn", L.NewFunction(func(L *lua.LState) int {
		lvm.log(slog.LevelWarn, L.CheckString(1), tableToMap(L, L.OptTable(2, L.NewTable())))
		return 0
	}))
	L.SetField(scripting, "Error", L.NewFunction(func(L *lua.LState) int {
		lvm.log(slog.LevelError, L.CheckString(1), tableToMap(L, L.OptTable(2, L.NewTable())))
		return 0
	}))
	L.SetField(scripting, "Debug", L.NewFunction(func(L *lua.LState) int {
		lvm.log(slog.LevelDebug, L.CheckString(1), tableToMap(L, L.OptTable(2, L.NewTable())))
		return 0
	}))

	scriptingParent := L.NewTable()
	L.SetField(scriptingParent, "Scripting", scripting)
	L.SetGlobal("LogService", scriptingParent)
}

func (lvm *LuaVM) luaPrint(L *lua.LState) int {
	top := L.GetTop()
	parts := make([]string, 0, top)
	for i := 1; i <= top; i++ {
		parts = append(parts, L.ToStringMeta(L.Get(i)).String())
	}
	lvm.log(slog.LevelInfo, strings.Join(parts, "\t"), nil)
	return 0
}

func (lvm *LuaVM) log(level slog.Level, msg string, params map[string]any) {
	if lvm.VM.svc.Logger == nil {
		return
	}
	attrs := []any{
		slog.String("plugin_id", lvm.VM.entity.PluginID),
		slog.String("device_id", lvm.VM.entity.DeviceID),
		slog.String("entity_id", lvm.VM.entity.ID),
		slog.String("source", "lua"),
	}
	for k, v := range params {
		attrs = append(attrs, slog.Any(k, v))
	}
	lvm.VM.svc.Logger.Log(context.Background(), level, msg, attrs...)
}

// ---------------------------------------------------------------------------
// TimerService.Scripting
// ---------------------------------------------------------------------------

// luaErrMsg formats a gopher-lua error for logging. It strips duplicate stack
// tracebacks that appear when errors propagate through multiple Lua call frames.
func luaErrMsg(err error) string {
	s := err.Error()
	// gopher-lua can produce two "stack traceback:" sections; keep only the first.
	const marker = "\nstack traceback:"
	first := strings.Index(s, marker)
	if first != -1 {
		second := strings.Index(s[first+1:], marker)
		if second != -1 {
			s = s[:first+1+second]
		}
	}
	return s
}

func (lvm *LuaVM) injectTimerService() {
	L := lvm.L
	ts := lvm.VM.Timers

	scripting := L.NewTable()

	// TimerService.Scripting.After(seconds, fn) -> id
	L.SetField(scripting, "After", L.NewFunction(func(L *lua.LState) int {
		delay := time.Duration(float64(L.CheckNumber(1)) * float64(time.Second))
		fn := L.CheckFunction(2)
		id := ts.After(delay, func() {
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
				lvm.log(slog.LevelError, "TimerService.After callback failed", map[string]any{"error": luaErrMsg(err)})
			}
		})
		L.Push(lua.LNumber(id))
		return 1
	}))

	// TimerService.Scripting.Every(seconds, fn) -> id
	L.SetField(scripting, "Every", L.NewFunction(func(L *lua.LState) int {
		interval := time.Duration(float64(L.CheckNumber(1)) * float64(time.Second))
		fn := L.CheckFunction(2)
		id := ts.Every(interval, func() {
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
				lvm.log(slog.LevelError, "TimerService.Every callback failed", map[string]any{"error": luaErrMsg(err)})
			}
		})
		L.Push(lua.LNumber(id))
		return 1
	}))

	// TimerService.Scripting.Cancel(id)
	L.SetField(scripting, "Cancel", L.NewFunction(func(L *lua.LState) int {
		id := TimerID(L.CheckNumber(1))
		ts.Cancel(id)
		return 0
	}))

	scriptingParent := L.NewTable()
	L.SetField(scriptingParent, "Scripting", scripting)
	L.SetGlobal("TimerService", scriptingParent)
}

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
		if isArrayTable(x) {
			out := make([]any, 0, x.Len())
			for i := 1; i <= x.Len(); i++ {
				out = append(out, luaToGo(x.RawGetInt(i)))
			}
			return out
		}
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
	case []any:
		return sliceToTable(L, x)
	case []int:
		out := make([]any, 0, len(x))
		for _, v := range x {
			out = append(out, v)
		}
		return sliceToTable(L, out)
	case []string:
		out := make([]any, 0, len(x))
		for _, v := range x {
			out = append(out, v)
		}
		return sliceToTable(L, out)
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

func sliceToTable(L *lua.LState, values []any) *lua.LTable {
	t := L.NewTable()
	for i, v := range values {
		L.RawSetInt(t, i+1, goToLua(L, v))
	}
	return t
}

func isArrayTable(t *lua.LTable) bool {
	if t.Len() == 0 {
		return false
	}
	count := 0
	arrayLike := true
	t.ForEach(func(k, _ lua.LValue) {
		count++
		if !arrayLike {
			return
		}
		n, ok := k.(lua.LNumber)
		if !ok {
			arrayLike = false
			return
		}
		i := int(n)
		if float64(n) != float64(i) || i < 1 || i > t.Len() {
			arrayLike = false
		}
	})
	return arrayLike && count == t.Len()
}

// entitySupportsActionLua checks if action is in the actions slice.
// Returns true if actions is empty (no capability data).
func entitySupportsActionLua(actions []string, action string) bool {
	if len(actions) == 0 {
		return true
	}
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}

func entityToTable(L *lua.LState, e types.Entity) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "ID", lua.LString(e.ID))
	L.SetField(t, "PluginID", lua.LString(e.PluginID))
	L.SetField(t, "DeviceID", lua.LString(e.DeviceID))
	L.SetField(t, "Domain", lua.LString(e.Domain))

	if len(e.Actions) > 0 {
		at := L.NewTable()
		for i, a := range e.Actions {
			L.RawSetInt(at, i+1, lua.LString(a))
		}
		L.SetField(t, "Actions", at)
	}

	// e:Supports("action") — checks if action is in e.Actions
	actions := e.Actions
	L.SetField(t, "Supports", L.NewFunction(func(L *lua.LState) int {
		// Supports colon syntax e:Supports("x") where arg1=self, arg2=action
		// and dot syntax e.Supports("x") where arg1=action
		var action string
		if s, ok := L.Get(1).(lua.LString); ok {
			action = string(s)
		} else {
			action = L.CheckString(2)
		}
		if len(actions) == 0 {
			L.Push(lua.LTrue) // no capability data — assume supported
			return 1
		}
		for _, a := range actions {
			if a == action {
				L.Push(lua.LTrue)
				return 1
			}
		}
		L.Push(lua.LFalse)
		return 1
	}))

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
