package main

import (
	"encoding/json"
	"log"
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
	log.Printf("gateway cmd: execute start command_id=%s owner=%s/%s/%s", job.rootStatus.CommandID, job.rootStatus.PluginID, job.rootStatus.DeviceID, job.rootStatus.EntityID)

	resp := routeRPC(t.PluginID, "entities/commands/create", map[string]any{
		"command_id": job.rootStatus.CommandID,
		"device_id":  t.DeviceID,
		"entity_id":  t.EntityID,
		"payload":    job.payload,
	})

	job.rootStatus.LastUpdatedAt = time.Now().UTC()

	if resp.Error != nil {
		log.Printf("gateway cmd: execute rpc failed command_id=%s target=%s/%s/%s duration_ms=%d err=%s", job.rootStatus.CommandID, t.PluginID, t.DeviceID, t.EntityID, time.Since(rpcStart).Milliseconds(), resp.Error.Message)
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = resp.Error.Message
		job.onComplete(job.rootStatus)
		return
	}

	var st types.CommandStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		log.Printf("gateway cmd: execute decode failed command_id=%s duration_ms=%d err=%v", job.rootStatus.CommandID, time.Since(rpcStart).Milliseconds(), err)
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = "invalid command status from plugin"
		job.onComplete(job.rootStatus)
		return
	}

	log.Printf("gateway cmd: execute rpc ok command_id=%s downstream_command_id=%s state=%s duration_ms=%d", job.rootStatus.CommandID, st.CommandID, st.State, time.Since(rpcStart).Milliseconds())

	if st.State == types.CommandFailed {
		job.rootStatus.State = types.CommandFailed
		job.rootStatus.Error = st.Error
	} else {
		job.rootStatus.State = types.CommandSucceeded
	}
	job.onComplete(job.rootStatus)
	log.Printf("gateway cmd: execute complete command_id=%s state=%s err=%q", job.rootStatus.CommandID, job.rootStatus.State, job.rootStatus.Error)
}
