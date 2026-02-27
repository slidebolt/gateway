package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	runner "github.com/slidebolt/sdk-runner"
)

type runtimeDescriptor struct {
	APIBaseURL   string    `json:"api_base_url"`
	APIHost      string    `json:"api_host"`
	APIPort      string    `json:"api_port"`
	NATSURL      string    `json:"nats_url"`
	GatewayID    string    `json:"gateway_id"`
	StartedAt    time.Time `json:"started_at"`
	PID          int       `json:"pid"`
}

func writeRuntimeDescriptor(apiHost, apiPort, natsURL, gatewayID string) {
	path := os.Getenv(runner.EnvRuntimeFile)
	if path == "" {
		path = ".build/runtime.json"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	d := runtimeDescriptor{
		APIBaseURL: "http://" + apiHost + ":" + apiPort,
		APIHost:    apiHost,
		APIPort:    apiPort,
		NATSURL:    natsURL,
		GatewayID:  gatewayID,
		StartedAt:  time.Now().UTC(),
		PID:        os.Getpid(),
	}
	b, _ := json.MarshalIndent(d, "", "  ")
	_ = os.WriteFile(path, b, 0o644)
}
