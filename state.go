package main

import (
	"sync"

	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"

	"github.com/slidebolt/gateway/internal/history"
)

type pluginRecord struct {
	Registration types.Registration
	Valid        bool
}

var (
	nc              *nats.Conn
	js              nats.JetStreamContext
	historyService  *history.History
	diskIO          DiskIO = OSDiskIO{}
	registry               = make(map[string]pluginRecord)
	regMu           sync.RWMutex
	gatewayRT       gatewayRuntimeInfo
	registryService     *regsvc.Registry
	commandService      *Command
	dynamicEventService *DynamicEventService
	gatewayDataDir      string
)

type gatewayRuntimeInfo struct {
	NATSURL string `json:"nats_url"`
	Version string `json:"version,omitempty"`
}
