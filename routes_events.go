package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

type IngestEventInput struct {
	PluginID  string         `path:"plugin_id" doc:"Plugin ID"`
	DeviceID  string         `path:"device_id" doc:"Device ID"`
	EntityID  string         `path:"entity_id" doc:"Entity ID"`
	CommandID string         `header:"X-Command-ID" doc:"Optional command ID this event is responding to. Marks that command as succeeded."`
	Body      map[string]any `doc:"Domain-specific event payload. Must include a 'type' field (e.g. {\"type\":\"state\",\"on\":true})."`
}
type IngestEventOutput struct{ Body types.Entity }

func registerEventRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "ingest-event",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/events",
		Summary:     "Ingest entity event",
		Description: "Reports a state-change event from a device/entity. Updates entity state (reported, effective). Pass X-Command-ID to link this event to a prior command, which marks that command succeeded.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *IngestEventInput) (*IngestEventOutput, error) {
		pluginID, deviceID, entityID := input.PluginID, input.DeviceID, input.EntityID
		payloadBytes, _ := json.Marshal(input.Body)
		payload := json.RawMessage(payloadBytes)
		commandID := input.CommandID

		params := map[string]any{"device_id": deviceID, "entity_id": entityID, "payload": payload, "command_id": commandID}
		resp := routeRPC(pluginID, "entities/events/ingest", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var ent types.Entity
		json.Unmarshal(resp.Result, &ent)
		return &IngestEventOutput{Body: ent}, nil
	})
}

