package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/nats-io/nats.go"
	gwscripting "github.com/slidebolt/gateway/internal/scripting"
)

type scriptManager struct {
	mu  sync.RWMutex
	svc gwscripting.Services
	vms map[string]*gwscripting.LuaVM
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
		},
		vms: make(map[string]*gwscripting.LuaVM),
	}
}

func (m *scriptManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	vms := m.vms
	m.vms = make(map[string]*gwscripting.LuaVM)
	m.mu.Unlock()
	for _, vm := range vms {
		vm.Stop()
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

	vm, err := gwscripting.NewLuaVM(entity, source, m.svc)
	if err != nil {
		return err
	}

	key := scriptKey(pluginID, deviceID, entityID)
	m.mu.Lock()
	old := m.vms[key]
	m.vms[key] = vm
	m.mu.Unlock()
	if old != nil {
		old.Stop()
	}
	return nil
}

func (m *scriptManager) Remove(pluginID, deviceID, entityID string) {
	if m == nil {
		return
	}
	key := scriptKey(pluginID, deviceID, entityID)
	m.mu.Lock()
	vm := m.vms[key]
	delete(m.vms, key)
	m.mu.Unlock()
	if vm != nil {
		vm.Stop()
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
