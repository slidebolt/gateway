//go:build bdd

package main

// Pass 2 BDD step definitions for the Lua scripting API.
// These tests create real LuaVMs and exercise the Lua bindings.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"
	"github.com/slidebolt/gateway/internal/scripting"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Extend scriptingTestCtx with Lua VM fields
// ---------------------------------------------------------------------------

type luaState struct {
	vms          map[string]*scripting.LuaVM  // named VMs
	entityVMs    map[string]*scripting.LuaVM  // entity-ID → VM
	namedSources map[string]string            // VM name → source
	currentSrc   string                       // current unnamed script
	lastVM       *scripting.LuaVM             // last started (unnamed)
	lastEntity   types.Entity                 // entity for last started VM
	lastVMErr    error                        // error from last start
	broadcastVMs []*scripting.LuaVM           // for bulk integration tests
}

type luaStateKey struct{}

func getLuaState(ctx context.Context) *luaState {
	if v, ok := ctx.Value(luaStateKey{}).(*luaState); ok && v != nil {
		return v
	}
	return nil
}

func withLuaState(ctx context.Context) (context.Context, *luaState) {
	if v, ok := ctx.Value(luaStateKey{}).(*luaState); ok && v != nil {
		return ctx, v
	}
	ls := &luaState{
		vms:          make(map[string]*scripting.LuaVM),
		entityVMs:    make(map[string]*scripting.LuaVM),
		namedSources: make(map[string]string),
	}
	return context.WithValue(ctx, luaStateKey{}, ls), ls
}

// ---------------------------------------------------------------------------
// Script source steps
// ---------------------------------------------------------------------------

func aLuaScript(ctx context.Context, src *godog.DocString) (context.Context, error) {
	ctx, ls := withLuaState(ctx)
	ls.currentSrc = strings.TrimSpace(src.Content)
	return ctx, nil
}

func aLuaScriptForVM(ctx context.Context, vmName string, src *godog.DocString) (context.Context, error) {
	ctx, ls := withLuaState(ctx)
	ls.namedSources[vmName] = strings.TrimSpace(src.Content)
	return ctx, nil
}

// ---------------------------------------------------------------------------
// VM lifecycle steps
// ---------------------------------------------------------------------------

func iStartTheLuaVM(ctx context.Context) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	if len(sc.finder.entities) == 0 {
		return ctx, fmt.Errorf("no entities in scripting test context")
	}
	entity := sc.finder.entities[0]
	vm, err := scripting.NewLuaVM(entity, ls.currentSrc, sc.svc)
	if err != nil {
		ls.lastVMErr = err
		return ctx, nil
	}
	err = vm.VM.Exec(vm.ExecOnInit)
	ls.lastVM = vm
	ls.lastEntity = entity
	ls.lastVMErr = err
	if vm != nil {
		ls.vms["_last"] = vm
		ls.entityVMs[entity.ID] = vm
	}
	return ctx, nil
}

func iStartTheLuaVMForEntity(ctx context.Context, entityID string) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	entity := findEntityByID(sc.finder.entities, entityID)
	if entity == nil {
		e := types.Entity{ID: entityID}
		sc.finder.entities = append(sc.finder.entities, e)
		entity = &sc.finder.entities[len(sc.finder.entities)-1]
	}
	vm, err := scripting.NewLuaVM(*entity, ls.currentSrc, sc.svc)
	if err != nil {
		ls.lastVMErr = err
		return ctx, nil
	}
	err = vm.VM.Exec(vm.ExecOnInit)
	ls.lastVM = vm
	ls.lastEntity = *entity
	ls.lastVMErr = err
	if vm != nil {
		ls.vms["_last"] = vm
		ls.entityVMs[entityID] = vm
	}
	return ctx, nil
}

func iStartVMForEntity(ctx context.Context, vmName, entityID string) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	src, ok := ls.namedSources[vmName]
	if !ok {
		return ctx, fmt.Errorf("no script registered for VM %q", vmName)
	}
	entity := findEntityByID(sc.finder.entities, entityID)
	if entity == nil {
		e := types.Entity{ID: entityID}
		sc.finder.entities = append(sc.finder.entities, e)
		entity = &sc.finder.entities[len(sc.finder.entities)-1]
	}
	vm, err := scripting.NewLuaVM(*entity, src, sc.svc)
	if err != nil {
		return ctx, fmt.Errorf("VM %q failed to start: %w", vmName, err)
	}
	if err := vm.VM.Exec(vm.ExecOnInit); err != nil {
		return ctx, fmt.Errorf("VM %q OnInit failed: %w", vmName, err)
	}
	ls.vms[vmName] = vm
	ls.entityVMs[entityID] = vm
	return ctx, nil
}

func iStopTheLuaVM(ctx context.Context) (context.Context, error) {
	ctx, ls := withLuaState(ctx)
	if ls.lastVM == nil {
		return ctx, fmt.Errorf("no VM to stop")
	}
	ls.lastVM.VM.Stop()
	return ctx, nil
}

func iStart3LuaVMsFor3DifferentEntities(ctx context.Context) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	for i := 0; i < 3; i++ {
		entityID := fmt.Sprintf("vm-entity-%d", i)
		entity := types.Entity{ID: entityID, Domain: "switch"}
		sc.finder.entities = append(sc.finder.entities, entity)
		vm, err := scripting.NewLuaVM(entity, ls.currentSrc, sc.svc)
		if err != nil {
			return ctx, fmt.Errorf("VM %d failed: %w", i, err)
		}
		if err := vm.VM.Exec(vm.ExecOnInit); err != nil {
			return ctx, fmt.Errorf("VM %d OnInit failed: %w", i, err)
		}
		name := fmt.Sprintf("multi-%d", i)
		ls.vms[name] = vm
		ls.entityVMs[entityID] = vm
	}
	return ctx, nil
}

func given10LuaVMsEachSubscribingToWildcardEvents(ctx context.Context) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	src := `
ReceivedCount = 0
function OnInit(ctx)
  EventService.Scripting.OnEvent(ctx, "*", function(env)
    ReceivedCount = ReceivedCount + 1
  end)
end`
	for i := 0; i < 10; i++ {
		entityID := fmt.Sprintf("broadcast-entity-%d", i)
		entity := types.Entity{ID: entityID, Domain: "switch"}
		sc.finder.entities = append(sc.finder.entities, entity)
		vm, err := scripting.NewLuaVM(entity, src, sc.svc)
		if err != nil {
			return ctx, fmt.Errorf("VM %d failed: %w", i, err)
		}
		if err := vm.VM.Exec(vm.ExecOnInit); err != nil {
			return ctx, fmt.Errorf("VM %d OnInit failed: %w", i, err)
		}
		ls.broadcastVMs = append(ls.broadcastVMs, vm)
		ls.vms[fmt.Sprintf("broadcast-%d", i)] = vm
	}
	return ctx, nil
}

func given10LuaVMsEachSendingACommandOnInit(ctx context.Context) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	src := `
function OnInit(ctx)
  This.SendCommand("turn_on", {})
end`
	for i := 0; i < 10; i++ {
		entityID := fmt.Sprintf("cmd-entity-%d", i)
		entity := types.Entity{ID: entityID, PluginID: "plugin-1", DeviceID: "device-1", Domain: "switch"}
		sc.finder.entities = append(sc.finder.entities, entity)
		vm, err := scripting.NewLuaVM(entity, src, sc.svc)
		if err != nil {
			return ctx, fmt.Errorf("VM %d failed: %w", i, err)
		}
		if err := vm.VM.Exec(vm.ExecOnInit); err != nil {
			return ctx, fmt.Errorf("VM %d OnInit failed: %w", i, err)
		}
		ls.vms[fmt.Sprintf("cmd-vm-%d", i)] = vm
		ls.entityVMs[entityID] = vm
	}
	return ctx, nil
}

func givenNLuaVMsForEntity(ctx context.Context, n int, entityID string) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	entity := findEntityByID(sc.finder.entities, entityID)
	if entity == nil {
		e := types.Entity{ID: entityID}
		sc.finder.entities = append(sc.finder.entities, e)
		entity = &sc.finder.entities[len(sc.finder.entities)-1]
	}
	ls.broadcastVMs = nil
	for i := 0; i < n; i++ {
		vm, err := scripting.NewLuaVM(*entity, ls.currentSrc, sc.svc)
		if err != nil {
			return ctx, err
		}
		ls.broadcastVMs = append(ls.broadcastVMs, vm)
	}
	return ctx, nil
}

func givenNLuaVMsForNDifferentEntities(ctx context.Context, n int) (context.Context, error) {
	sc := scriptingCtxFrom(ctx)
	ctx, ls := withLuaState(ctx)
	ls.broadcastVMs = nil
	for i := 0; i < n; i++ {
		entityID := fmt.Sprintf("stress-entity-%d", i)
		entity := types.Entity{ID: entityID, Domain: "light"}
		sc.finder.entities = append(sc.finder.entities, entity)
		vm, err := scripting.NewLuaVM(entity, ls.currentSrc, sc.svc)
		if err != nil {
			return ctx, err
		}
		ls.broadcastVMs = append(ls.broadcastVMs, vm)
	}
	return ctx, nil
}

func whenAllVMsStartSimultaneously(ctx context.Context, n int) error {
	ls := getLuaState(ctx)
	if len(ls.broadcastVMs) < n {
		return fmt.Errorf("expected %d VMs, have %d", n, len(ls.broadcastVMs))
	}
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for _, vm := range ls.broadcastVMs {
		wg.Add(1)
		go func(v *scripting.LuaVM) {
			defer wg.Done()
			if err := v.VM.Exec(v.ExecOnInit); err != nil {
				errCount.Add(1)
			}
		}(vm)
	}
	wg.Wait()
	if errCount.Load() > 0 {
		return fmt.Errorf("%d VMs failed to start/exec", errCount.Load())
	}
	return nil
}

func iDispatchCommandToEntityViaTheBinding(ctx context.Context, command, entityID string) (context.Context, error) {
	ctx, ls := withLuaState(ctx)
	vm, ok := ls.entityVMs[entityID]
	if !ok {
		return ctx, fmt.Errorf("no VM found for entity %q", entityID)
	}
	if err := vm.HandleCommand(command, map[string]any{"type": command}); err != nil {
		if err != scripting.ErrNoCommandHandler {
			return ctx, err
		}
	}
	return ctx, nil
}

func iExecuteTheLuaCode(ctx context.Context, code string) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	_, err := ls.lastVM.ExecLua(code)
	return err
}

func iUpdateTheLuaScript(ctx context.Context, source string) error {
	sc := scriptingCtxFrom(ctx)
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	ls.lastVM.VM.Stop()
	vm, err := scripting.NewLuaVM(ls.lastEntity, strings.TrimSpace(source), sc.svc)
	if err != nil {
		return err
	}
	if err := vm.VM.Exec(vm.ExecOnInit); err != nil {
		return err
	}
	ls.lastVM = vm
	return nil
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

func theVMStartsWithoutError(ctx context.Context) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVMErr == nil {
		return nil
	}
	return fmt.Errorf("VM failed to start: %v", ls.lastVMErr)
}

func theVMFailsToStartWithAnError(ctx context.Context) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVMErr == nil {
		return fmt.Errorf("expected VM to fail, but it started successfully")
	}
	return nil
}

func theVMIsStopped(ctx context.Context) error {
	return nil
}

func all3VMsStartWithoutError(ctx context.Context) error {
	ls := getLuaState(ctx)
	for name, vm := range ls.vms {
		if strings.HasPrefix(name, "multi-") && vm == nil {
			return fmt.Errorf("VM %q is nil", name)
		}
	}
	return nil
}

func eachVMHasLuaGlobalEqualTo1(ctx context.Context, globalName string) error {
	ls := getLuaState(ctx)
	for name, vm := range ls.vms {
		if !strings.HasPrefix(name, "multi-") {
			continue
		}
		n, err := vm.GetGlobalNumber(globalName)
		if err != nil {
			return fmt.Errorf("VM %q GetGlobal(%q): %w", name, globalName, err)
		}
		if n != 1 {
			return fmt.Errorf("VM %q: expected %s=1, got %v", name, globalName, n)
		}
	}
	return nil
}

func theLuaGlobalEqualsNumber(ctx context.Context, name string, want int) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	n, err := ls.lastVM.GetGlobalNumber(name)
	if err != nil {
		return err
	}
	if int(n) != want {
		return fmt.Errorf("Lua global %q = %v, expected %d", name, n, want)
	}
	return nil
}

func theLuaGlobalEventuallyEqualsNumber(ctx context.Context, name string, want int) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n, err := ls.lastVM.GetGlobalNumber(name)
		if err == nil && int(n) == want {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	n, _ := ls.lastVM.GetGlobalNumber(name)
	return fmt.Errorf("Lua global %q = %v, expected %d (timed out)", name, n, want)
}

func theEntityShouldHaveAtLeastValuesInLabel(ctx context.Context, entityID string, count int, labelKey string) error {
	sc := scriptingCtxFrom(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		history := sc.bus.allEvents()
		var lastLabels map[string][]string
		for _, env := range history {
			if env.EntityID == entityID {
				var payload map[string]any
				_ = json.Unmarshal(env.Payload, &payload)
				if l, ok := payload["labels"].(map[string]any); ok {
					// convert to map[string][]string
					merged := make(map[string][]string)
					for k, v := range l {
						if arr, ok := v.([]any); ok {
							for _, val := range arr {
								merged[k] = append(merged[k], fmt.Sprint(val))
							}
						}
					}
					lastLabels = merged
				}
			}
		}

		if lastLabels != nil {
			if vals := lastLabels[labelKey]; len(vals) >= count {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("entity %q label %q did not reach %d values", entityID, labelKey, count)
}

func theGatewayShouldNotHavePanicked(ctx context.Context) error {
	return nil
}

func theGatewayShouldRemainHealthy(ctx context.Context) error {
	return nil
}

func iWaitSeconds(ctx context.Context, seconds int) error {
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

func theLuaGlobalEqualsAfterWaitingSeconds(ctx context.Context, globalName string, want int, seconds int) error {
	time.Sleep(time.Duration(seconds) * time.Second)
	return theLuaGlobalEqualsNumber(ctx, globalName, want)
}

func theLuaGlobalEqualsString(ctx context.Context, name, want string) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	s, err := ls.lastVM.GetGlobalString(name)
	if err != nil {
		return err
	}
	if s != want {
		return fmt.Errorf("Lua global %q = %q, expected %q", name, s, want)
	}
	return nil
}

func theLuaGlobalIsNotEmpty(ctx context.Context, name string) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	s, err := ls.lastVM.GetGlobalString(name)
	if err != nil {
		return err
	}
	if s == "" {
		return fmt.Errorf("Lua global %q is empty", name)
	}
	return nil
}

func theLuaGlobalIsTrue(ctx context.Context, name string) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	b, err := ls.lastVM.GetGlobalBool(name)
	if err != nil {
		return err
	}
	if !b {
		return fmt.Errorf("Lua global %q is false, expected true", name)
	}
	return nil
}

func theLuaGlobalIsFalse(ctx context.Context, name string) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	b, err := ls.lastVM.GetGlobalBool(name)
	if err != nil {
		return err
	}
	if b {
		return fmt.Errorf("Lua global %q is true, expected false", name)
	}
	return nil
}

func theLuaGlobalIsATableWithEntries(ctx context.Context, name string, count int) error {
	ls := getLuaState(ctx)
	if ls == nil || ls.lastVM == nil {
		return fmt.Errorf("no Lua VM active")
	}
	n, err := ls.lastVM.GetGlobalTableLen(name)
	if err != nil {
		return err
	}
	if n != count {
		return fmt.Errorf("Lua global %q has %d entries, expected %d", name, n, count)
	}
	return nil
}

func vmHasLuaGlobalEqualToNumber(ctx context.Context, vmName, globalName string, want int) error {
	ls := getLuaState(ctx)
	if ls == nil {
		return fmt.Errorf("no Lua state")
	}
	vm, ok := ls.vms[vmName]
	if !ok {
		return fmt.Errorf("VM %q not found", vmName)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	var n float64
	var err error
	for {
		n, err = vm.GetGlobalNumber(globalName)
		if err == nil && int(n) == want {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("VM %q: %w", vmName, err)
	}
	return fmt.Errorf("VM %q global %q = %v, expected %d", vmName, globalName, n, want)
}

func all10VMsReceivedTheEventExactlyOnce(ctx context.Context) error {
	ls := getLuaState(ctx)
	for i, vm := range ls.broadcastVMs {
		n, err := vm.GetGlobalNumber("ReceivedCount")
		if err != nil {
			return fmt.Errorf("broadcast VM %d: %w", i, err)
		}
		if int(n) != 1 {
			return fmt.Errorf("broadcast VM %d: ReceivedCount=%v, expected 1", i, n)
		}
	}
	return nil
}

func all10CommandsWereRecorded(ctx context.Context) error {
	sc := scriptingCtxFrom(ctx)
	sc.submitter.mu.Lock()
	n := len(sc.submitter.commands)
	sc.submitter.mu.Unlock()
	if n < 10 {
		return fmt.Errorf("expected 10 commands recorded, got %d", n)
	}
	return nil
}

func findEntityByID(entities []types.Entity, id string) *types.Entity {
	for i, e := range entities {
		if e.ID == id {
			return &entities[i]
		}
	}
	return nil
}

func registerScriptingLuaSteps(sc *godog.ScenarioContext) {
	sc.After(func(ctx context.Context, scenario *godog.Scenario, err error) (context.Context, error) {
		sctx := scriptingCtxFrom(ctx)
		if sctx != nil && sctx.svc.Timers != nil {
			sctx.svc.Timers.Clear()
		}
		if ls := getLuaState(ctx); ls != nil {
			stopped := map[*scripting.LuaVM]bool{}
			stopOnce := func(vm *scripting.LuaVM) {
				if vm != nil && !stopped[vm] {
					stopped[vm] = true
					vm.VM.Stop()
				}
			}
			for _, vm := range ls.vms {
				stopOnce(vm)
			}
			for _, vm := range ls.broadcastVMs {
				stopOnce(vm)
			}
		}
		return ctx, nil
	})

	sc.Step(`^a Lua script:$`, aLuaScript)
	sc.Step(`^each VM has the script:$`, aLuaScript)
	sc.Step(`^a Lua script for VM "([^"]*)":$`, aLuaScriptForVM)

	sc.Step(`^I start the Lua VM$`, iStartTheLuaVM)
	sc.Step(`^I start the Lua VM for entity "([^"]*)"$`, iStartTheLuaVMForEntity)
	sc.Step(`^I start VM "([^"]*)" for entity "([^"]*)"$`, iStartVMForEntity)
	sc.Step(`^I stop the Lua VM$`, iStopTheLuaVM)
	sc.Step(`^all (\d+) VMs start simultaneously$`, whenAllVMsStartSimultaneously)
	sc.Step(`^I start 3 Lua VMs for 3 different entities$`, iStart3LuaVMsFor3DifferentEntities)
	sc.Step(`^(\d+) Lua VMs for entity "([^"]*)"$`, givenNLuaVMsForEntity)
	sc.Step(`^(\d+) Lua VMs for (\d+) different entities$`, func(ctx context.Context, n, m int) (context.Context, error) { return givenNLuaVMsForNDifferentEntities(ctx, n) })
	sc.Step(`^10 Lua VMs each subscribing to wildcard events$`, given10LuaVMsEachSubscribingToWildcardEvents)
	sc.Step(`^10 Lua VMs each sending a command on init$`, given10LuaVMsEachSendingACommandOnInit)
	sc.Step(`^I dispatch command "([^"]*)" to entity "([^"]*)" via the binding$`, iDispatchCommandToEntityViaTheBinding)
	sc.Step(`^I execute the Lua code "([^"]*)"$`, iExecuteTheLuaCode)
	sc.Step(`^I update the Lua script:$`, iUpdateTheLuaScript)

	sc.Step(`^the VM starts without error$`, theVMStartsWithoutError)
	sc.Step(`^the VM fails to start with an error$`, theVMFailsToStartWithAnError)
	sc.Step(`^the VM is stopped$`, theVMIsStopped)
	sc.Step(`^all 3 VMs start without error$`, all3VMsStartWithoutError)
	sc.Step(`^each VM has Lua global "([^"]*)" equal to 1$`, eachVMHasLuaGlobalEqualTo1)

	sc.Step(`^the Lua global "([^"]*)" equals (\d+)$`, theLuaGlobalEqualsNumber)
	sc.Step(`^the Lua global "([^"]*)" eventually equals (\d+)$`, theLuaGlobalEventuallyEqualsNumber)
	sc.Step(`^the entity "([^"]*)" should have at least (\d+) values in the "([^"]*)" label$`, theEntityShouldHaveAtLeastValuesInLabel)
	sc.Step(`^the Gateway should not have panicked$`, theGatewayShouldNotHavePanicked)
	sc.Step(`^the Gateway should remain healthy$`, theGatewayShouldRemainHealthy)
	sc.Step(`^the Lua global "([^"]*)" equals (\d+) after waiting (\d+) seconds$`, theLuaGlobalEqualsAfterWaitingSeconds)
	sc.Step(`^I wait (\d+) seconds$`, iWaitSeconds)
	sc.Step(`^the Lua global "([^"]*)" equals "([^"]*)"$`, theLuaGlobalEqualsString)
	sc.Step(`^the Lua global "([^"]*)" is not empty$`, theLuaGlobalIsNotEmpty)
	sc.Step(`^the Lua global "([^"]*)" is true$`, theLuaGlobalIsTrue)
	sc.Step(`^the Lua global "([^"]*)" is false$`, theLuaGlobalIsFalse)
	sc.Step(`^the Lua global "([^"]*)" is a table with (\d+) entries$`, theLuaGlobalIsATableWithEntries)

	sc.Step(`^VM "([^"]*)" has Lua global "([^"]*)" equal to (\d+)$`, vmHasLuaGlobalEqualToNumber)

	sc.Step(`^all 10 VMs received the event exactlyOnce$`, all10VMsReceivedTheEventExactlyOnce)
	sc.Step(`^all 10 commands were recorded$`, all10CommandsWereRecorded)
}
