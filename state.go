package main

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

var (
	nc          *nats.Conn
	registry    = make(map[string]types.Registration)
	regMu       sync.RWMutex
	vstore      *virtualStore
	gatewayRT   gatewayRuntimeInfo
)

type gatewayRuntimeInfo struct {
	NATSURL string `json:"nats_url"`
	Version string `json:"version,omitempty"`
}

type virtualEntityRecord struct {
	OwnerPluginID  string       `json:"owner_plugin_id"`
	OwnerDeviceID  string       `json:"owner_device_id"`
	SourcePluginID string       `json:"source_plugin_id"`
	SourceDeviceID string       `json:"source_device_id"`
	SourceEntityID string       `json:"source_entity_id"`
	MirrorSource   bool         `json:"mirror_source"`
	Entity         types.Entity `json:"entity"`
}

type virtualCommandRecord struct {
	OwnerPluginID  string              `json:"owner_plugin_id"`
	SourcePluginID string              `json:"source_plugin_id"`
	SourceCommand  string              `json:"source_command"`
	VirtualKey     string              `json:"virtual_key"`
	Status         types.CommandStatus `json:"status"`
}

type observedEvent struct {
	Name      string    `json:"name"`
	PluginID  string    `json:"plugin_id"`
	DeviceID  string    `json:"device_id"`
	EntityID  string    `json:"entity_id"`
	EventID   string    `json:"event_id"`
	CreatedAt time.Time `json:"created_at"`
}

type virtualStore struct {
	mu       sync.RWMutex
	entities map[string]virtualEntityRecord
	commands map[string]virtualCommandRecord
	events   []observedEvent
	dataDir  string
}

func entityKey(pluginID, deviceID, entityID string) string {
	return pluginID + "|" + deviceID + "|" + entityID
}

func virtualFile(dataDir, name string) string {
	return filepath.Join(dataDir, name)
}
