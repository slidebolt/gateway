package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

type pluginRecord struct {
	Registration types.Registration
	Valid        bool
}

var (
	nc                   *nats.Conn
	js                   nats.JetStreamContext
	history              *historyStore
	registry             = make(map[string]pluginRecord)
	regMu                sync.RWMutex
	vstore               *virtualStore
	gatewayRT            gatewayRuntimeInfo
	masterRegistry       *regsvc.Service
	hasProjectionDevices atomic.Bool
	virtualStatusSeq     atomic.Uint64
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
	SourceQuery    string       `json:"source_query,omitempty"`
	SourceDomain   string       `json:"source_domain,omitempty"`
	MirrorSource   bool         `json:"mirror_source"`
	Entity         types.Entity `json:"entity"`
}

type virtualDownstreamCommand struct {
	PluginID  string             `json:"plugin_id"`
	DeviceID  string             `json:"device_id"`
	EntityID  string             `json:"entity_id"`
	CommandID string             `json:"command_id"`
	State     types.CommandState `json:"state,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type virtualCommandRecord struct {
	OwnerPluginID  string                     `json:"owner_plugin_id"`
	SourcePluginID string                     `json:"source_plugin_id"`
	SourceCommand  string                     `json:"source_command"`
	VirtualKey     string                     `json:"virtual_key"`
	Status         types.CommandStatus        `json:"status"`
	Downstream     []virtualDownstreamCommand `json:"downstream,omitempty"`
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
	dataDir  string
}

func entityKey(pluginID, deviceID, entityID string) string {
	return pluginID + "|" + deviceID + "|" + entityID
}

func virtualFile(dataDir, name string) string {
	return filepath.Join(dataDir, name)
}
