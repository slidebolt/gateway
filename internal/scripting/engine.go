package scripting

import (
	"context"
	"fmt"
	"time"

	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// ScriptEngine — VM factory and lifecycle manager
// ---------------------------------------------------------------------------

// ScriptEngine creates and stops VMs. It has no routing map; each VM
// self-subscribes to whatever NATS subjects it needs. The engine's only job
// is: create → start (call OnInit) → stop.
type ScriptEngine struct {
	svc Services
}

// NewScriptEngine constructs a ScriptEngine with the given gateway dependencies.
func NewScriptEngine(svc Services) *ScriptEngine {
	return &ScriptEngine{svc: svc}
}

// Start creates a VM for entity running source, calls OnInit, and returns the
// running VM. The caller must call VM.Stop() when done.
func (e *ScriptEngine) Start(entity types.Entity, source string) (*VM, error) {
	vm, err := newVM(entity, source, e.svc)
	if err != nil {
		return nil, err
	}
	if err := vm.start(); err != nil {
		vm.Stop()
		return nil, err
	}
	return vm, nil
}

// ---------------------------------------------------------------------------
// VM — one Lua state per script
// ---------------------------------------------------------------------------

const defaultDeadline = 5 * time.Second

// VM wraps a Lua state. All Lua execution happens on the VM's own work-queue
// goroutine, which makes it safe to deliver events from many NATS goroutines.
type VM struct {
	entity  types.Entity
	source  string
	svc     Services
	ctx     context.Context
	cancel  context.CancelFunc
	work    chan workItem
	done    chan struct{}
	started bool

	// runOnInit is called during start(). Defaults to a no-op; LuaVM overrides it.
	runOnInit func() error

	// Scripting sub-services bound to this VM's context.
	This     *EntityBinding
	Query    *QueryScripting
	Commands *CommandScripting
	Events   *EventScripting
}

type workItem struct {
	fn   func() error
	errc chan error
}

// newVM builds a VM but does not start it or run any Lua yet.
func newVM(entity types.Entity, source string, svc Services) (*VM, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmds := newCommandScripting(svc.Commands)
	evts := newEventScriptingWithFinder(svc.Bus, svc.Finder)
	v := &VM{
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
	}
	v.runOnInit = func() error { return nil } // no-op default
	return v, nil
}

// start runs the work-queue goroutine and calls runOnInit with the deadline.
func (v *VM) start() error {
	go v.loop()

	errc := make(chan error, 1)
	v.work <- workItem{
		fn:   v.runOnInit,
		errc: errc,
	}

	select {
	case err := <-errc:
		v.started = true
		return err
	case <-time.After(defaultDeadline + time.Second):
		return fmt.Errorf("scripting: VM start timeout for entity %s", v.entity.ID)
	}
}

// loop is the single goroutine that owns the Lua state.
func (v *VM) loop() {
	defer close(v.done)
	for {
		select {
		case item := <-v.work:
			err := item.fn()
			if item.errc != nil {
				item.errc <- err
			}
		case <-v.ctx.Done():
			return
		}
	}
}

// Stop cancels the VM's context (unregisters all OnEvent subscriptions) and
// waits for the work-queue goroutine to exit.
func (v *VM) Stop() {
	v.cancel()
	<-v.done
}

// Exec runs an arbitrary function on the VM's goroutine and returns its error.
// Safe to call from any goroutine. Blocks until complete or ctx expires.
func (v *VM) Exec(fn func() error) error {
	errc := make(chan error, 1)
	select {
	case v.work <- workItem{fn: fn, errc: errc}:
	case <-v.ctx.Done():
		return fmt.Errorf("scripting: VM stopped")
	}
	select {
	case err := <-errc:
		return err
	case <-v.ctx.Done():
		return fmt.Errorf("scripting: VM stopped while waiting for result")
	}
}

// EnqueueEvent submits an event handler call to the work queue without blocking.
// Used by NATS callbacks — they must not block the subscriber goroutine.
func (v *VM) EnqueueEvent(fn func() error) {
	select {
	case v.work <- workItem{fn: fn}:
	case <-v.ctx.Done():
	}
}

// Source returns the Lua source this VM was created with.
func (v *VM) Source() string { return v.source }

// Entity returns the bound entity.
func (v *VM) Entity() types.Entity { return v.entity }
