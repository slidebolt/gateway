package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
	gwscripting "github.com/slidebolt/gateway/internal/scripting"
	automationsnippets "github.com/slidebolt/plugin-automation/snippets"
	"github.com/slidebolt/sdk-types"
)

type scriptManager struct {
	mu           sync.RWMutex
	svc          gwscripting.Services
	vms          map[string]*gwscripting.LuaVM
	namedSources map[string]string
	children     map[string]childScript
	parentToKids map[string]map[string]struct{}
}

type childScript struct {
	vm        *gwscripting.LuaVM
	parentKey string
}

func ensureScriptRuntime() {
	if scriptRuntime != nil || nc == nil || registryService == nil || commandService == nil {
		return
	}
	scriptRuntime = &scriptManager{
		svc: gwscripting.Services{
			Commands: commandService,
			Finder:   registryService,
			Bus:      natsEventBus{nc: nc},
			Logger:   slog.Default(),
			Timers:   gwscripting.NewOSTimerService(),
			Sessions: registryService,
			StartLua: gwscripting.NewLuaVM,
		},
		vms:          make(map[string]*gwscripting.LuaVM),
		namedSources: make(map[string]string),
		children:     make(map[string]childScript),
		parentToKids: make(map[string]map[string]struct{}),
	}
	for name, source := range automationsnippets.Builtins() {
		scriptRuntime.namedSources[name] = source
	}
	scriptRuntime.svc.Scripts = scriptRuntime
}

func (m *scriptManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	vms := m.vms
	children := m.children
	m.vms = make(map[string]*gwscripting.LuaVM)
	m.children = make(map[string]childScript)
	m.parentToKids = make(map[string]map[string]struct{})
	m.mu.Unlock()
	for _, vm := range vms {
		vm.Stop()
	}
	for _, child := range children {
		child.vm.Stop()
	}
}

func (m *scriptManager) Install(pluginID, deviceID, entityID, source string) error {
	if m == nil {
		return nil
	}
	entity, err := findEntity(pluginID, deviceID, entityID)
	if err != nil {
		return err
	}
	entity.PluginID = pluginID
	_, err = m.InstallVM(entity, source)
	return err
}

// InstallVM starts a LuaVM for an already-resolved entity and registers it.
// Used by tests and internal code that has an entity object already.
func (m *scriptManager) InstallVM(entity types.Entity, source string) (*gwscripting.LuaVM, error) {
	if m == nil {
		return nil, nil
	}
	vm, err := gwscripting.NewLuaVM(entity, source, m.svc)
	if err != nil {
		return nil, err
	}
	key := scriptKey(entity.PluginID, entity.DeviceID, entity.ID)
	m.mu.Lock()
	old := m.vms[key]
	m.vms[key] = vm
	childIDs := m.parentToKids[key]
	children := make([]childScript, 0, len(childIDs))
	for childID := range childIDs {
		children = append(children, m.children[childID])
		delete(m.children, childID)
	}
	delete(m.parentToKids, key)
	m.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	for _, child := range children {
		child.vm.Stop()
	}
	return vm, nil
}

func (m *scriptManager) Remove(pluginID, deviceID, entityID string) {
	if m == nil {
		return
	}
	key := scriptKey(pluginID, deviceID, entityID)
	m.mu.Lock()
	vm := m.vms[key]
	delete(m.vms, key)
	childIDs := m.parentToKids[key]
	children := make([]childScript, 0, len(childIDs))
	for childID := range childIDs {
		children = append(children, m.children[childID])
		delete(m.children, childID)
	}
	delete(m.parentToKids, key)
	m.mu.Unlock()
	if vm != nil {
		vm.Stop()
	}
	for _, child := range children {
		child.vm.Stop()
	}
}

func (m *scriptManager) NotifyCommand(pluginID, deviceID, entityID string, payload json.RawMessage) {
	if m == nil {
		return
	}
	key := scriptKey(pluginID, deviceID, entityID)
	m.mu.RLock()
	vm := m.vms[key]
	m.mu.RUnlock()
	if vm == nil {
		return
	}

	var params map[string]any
	if err := json.Unmarshal(payload, &params); err != nil {
		slog.Warn("script command payload decode failed", "plugin_id", pluginID, "device_id", deviceID, "entity_id", entityID, "error", err)
		return
	}
	action, _ := params["type"].(string)
	delete(params, "type")
	if action == "" {
		return
	}

	if err := vm.This.HandleCommand(action, params); err != nil && !errors.Is(err, gwscripting.ErrNoCommandHandler) {
		slog.Warn("script OnCommand failed", "plugin_id", pluginID, "device_id", deviceID, "entity_id", entityID, "action", action, "error", err)
	}
}

func scriptKey(pluginID, deviceID, entityID string) string {
	return pluginID + "\x00" + deviceID + "\x00" + entityID
}

func (m *scriptManager) RegisterNamedSource(name, source string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.namedSources[name] = source
}

func (m *scriptManager) Run(entity types.Entity, name string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("script runtime unavailable")
	}

	moduleName, entrypoint := parseNamedScriptRef(name)

	m.mu.RLock()
	source, ok := m.namedSources[moduleName]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("named script %q not found", moduleName)
	}

	instanceID := nextID("script-child")
	vm, err := gwscripting.NewLuaVMWithInstance(entity, source, gwscripting.ScriptInstance{
		Entrypoint: entrypoint,
		ScriptRef:  name,
		SessionID:  instanceID,
	}, m.svc)
	if err != nil {
		return "", err
	}

	parentKey := scriptKey(entity.PluginID, entity.DeviceID, entity.ID)
	m.mu.Lock()
	if m.parentToKids[parentKey] == nil {
		m.parentToKids[parentKey] = make(map[string]struct{})
	}
	m.parentToKids[parentKey][instanceID] = struct{}{}
	m.children[instanceID] = childScript{vm: vm, parentKey: parentKey}
	m.mu.Unlock()
	return instanceID, nil
}

func (m *scriptManager) StopScript(entity types.Entity, instanceID string) error {
	if m == nil {
		return fmt.Errorf("script runtime unavailable")
	}
	parentKey := scriptKey(entity.PluginID, entity.DeviceID, entity.ID)
	m.mu.Lock()
	child, ok := m.children[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("script instance %q not found", instanceID)
	}
	if child.parentKey != parentKey {
		m.mu.Unlock()
		return fmt.Errorf("script instance %q is not owned by entity %s/%s/%s", instanceID, entity.PluginID, entity.DeviceID, entity.ID)
	}
	delete(m.children, instanceID)
	if kids := m.parentToKids[parentKey]; kids != nil {
		delete(kids, instanceID)
		if len(kids) == 0 {
			delete(m.parentToKids, parentKey)
		}
	}
	m.mu.Unlock()
	if m.svc.Sessions != nil {
		_ = m.svc.Sessions.DeleteSession(instanceID)
	}
	child.vm.Stop()
	return nil
}

func parseNamedScriptRef(name string) (moduleName, entrypoint string) {
	moduleName = name
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		moduleName = name[:dot]
		if dot+1 < len(name) {
			entrypoint = name[dot+1:]
		}
	}
	return moduleName, entrypoint
}

type natsEventBus struct {
	nc *nats.Conn
}

func (b natsEventBus) Publish(subject string, data []byte) error {
	return b.nc.Publish(subject, data)
}

func (b natsEventBus) Subscribe(subject string, handler func([]byte)) (gwscripting.Subscription, error) {
	sub, err := b.nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
	if err != nil {
		return nil, err
	}
	return natsSub{sub: sub}, nil
}

type natsSub struct {
	sub *nats.Subscription
}

func (s natsSub) Unsubscribe() error {
	return s.sub.Unsubscribe()
}
