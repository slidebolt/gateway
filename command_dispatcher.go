package main

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/slidebolt/sdk-types"
)

type Dispatcher struct{}

func CommandDispatcher() *Dispatcher {
	return &Dispatcher{}
}

func (d *Dispatcher) Execute(job commandJob) {
	t := job.target
	rpcStart := time.Now()
	slog.Info("execute start", "command_id", job.rootStatus.CommandID, "plugin_id", job.rootStatus.PluginID, "device_id", job.rootStatus.DeviceID, "entity_id", job.rootStatus.EntityID)

	resp := routeRPC(t.PluginID, "entities/commands/create", map[string]any{
		"command_id": job.rootStatus.CommandID,
		"device_id":  t.DeviceID,
		"entity_id":  t.EntityID,
		"payload":    job.payload,
	})

	job.rootStatus.LastUpdatedAt = time.Now().UTC()

	if resp.Error != nil {
		slog.Warn("execute rpc failed", "command_id", job.rootStatus.CommandID, "plugin_id", t.PluginID, "device_id", t.DeviceID, "entity_id", t.EntityID, "duration_ms", time.Since(rpcStart).Milliseconds(), "error", resp.Error.Message)
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = resp.Error.Message
		job.onComplete(job.rootStatus)
		return
	}

	var st types.CommandStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		slog.Warn("execute decode failed", "command_id", job.rootStatus.CommandID, "duration_ms", time.Since(rpcStart).Milliseconds(), "error", err)
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = "invalid command status from plugin"
		job.onComplete(job.rootStatus)
		return
	}

	slog.Info("execute rpc ok", "command_id", job.rootStatus.CommandID, "downstream_command_id", st.CommandID, "state", st.State, "duration_ms", time.Since(rpcStart).Milliseconds())

	if st.State == types.CommandFailed {
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = st.Error
	} else {
		job.rootStatus.State = types.CommandSucceeded
	}
	job.onComplete(job.rootStatus)
	slog.Info("execute complete", "command_id", job.rootStatus.CommandID, "state", job.rootStatus.State, "error", job.rootStatus.Error)
}
