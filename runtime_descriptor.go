package main

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/slidebolt/sdk-types"
)

type runtimeDescriptor struct {
	APIBaseURL string    `json:"api_base_url"`
	APIHost    string    `json:"api_host"`
	APIPort    string    `json:"api_port"`
	NATSURL    string    `json:"nats_url"`
	GatewayID  string    `json:"gateway_id"`
	StartedAt  time.Time `json:"started_at"`
	PID        int       `json:"pid"`
}

func writeRuntimeDescriptor(apiHost, apiPort, natsURL, gatewayID string) {
	path := getenv(types.EnvRuntimeFile)
	if path == "" {
		path = ".build/runtime.json"
	}
	_ = diskIO.MkdirAll(filepath.Dir(path), 0o755)
	d := runtimeDescriptor{
		APIBaseURL: "http://" + apiHost + ":" + apiPort,
		APIHost:    apiHost,
		APIPort:    apiPort,
		NATSURL:    natsURL,
		GatewayID:  gatewayID,
		StartedAt:  time.Now().UTC(),
		PID:        pid(),
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	_ = diskIO.WriteFile(path, b, 0o644)
}
