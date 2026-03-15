package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- Command types ---

type SendCommandInput struct {
	PluginID string         `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string         `path:"device_id" doc:"Device ID"`
	EntityID string         `path:"entity_id" doc:"Entity ID"`
	Body     map[string]any `doc:"Domain-specific command payload. Must include a 'type' field (e.g. {\"type\":\"turn_on\"})."`
}
type CommandStatusOutput struct{ Body types.CommandStatus }

type GetCommandStatusInput struct {
	PluginID  string `path:"plugin_id" doc:"Plugin ID"`
	CommandID string `path:"command_id" doc:"Command ID returned by the send-command endpoint"`
}

func registerCommandRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "send-command",
		Method:        http.MethodPost,
		Path:          "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/commands",
		Summary:       "Send command",
		Description:   "Sends a domain-specific command to an entity. Returns CommandStatus with state=pending; poll get-command-status for completion.",
		Tags:          []string{"commands"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *SendCommandInput) (*CommandStatusOutput, error) {
		pluginID, deviceID, entityID := input.PluginID, input.DeviceID, input.EntityID
		payloadBytes, _ := json.Marshal(input.Body)
		payload := json.RawMessage(payloadBytes)
		status, err := commandService.Submit(pluginID, deviceID, entityID, payload)
		if err == nil {
			return &CommandStatusOutput{Body: status}, nil
		}
		if code, ok := commandErrCode(err); ok {
			switch code {
			case CommandErrNotFound:
				return nil, notFoundErr("entity not found")
			case CommandErrInvalidPayload, CommandErrUnsupportedAction:
				return nil, badReqErr(err.Error())
			case CommandErrTimeout:
				return nil, timeoutErr(err.Error())
			case CommandErrProjectionUnavailable:
				return nil, upstreamErr(err.Error())
			}
		}
		if errors.Is(err, errCommandTargetNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, notFoundErr("entity not found")
		}
		if strings.Contains(err.Error(), "payload.type is required") {
			return nil, badReqErr(err.Error())
		}
		return nil, pluginErr(err.Error())
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-command-status",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/commands/{command_id}",
		Summary:     "Get command status",
		Description: "Polls the status of a previously issued command. State transitions: pending → succeeded | failed.",
		Tags:        []string{"commands"},
	}, func(ctx context.Context, input *GetCommandStatusInput) (*CommandStatusOutput, error) {
		if commandService != nil && commandService.IsGatewayCommand(input.CommandID) {
			status, found := commandService.GetStatus(input.CommandID)
			if !found {
				return nil, notFoundErr("command not found")
			}
			if status.PluginID != input.PluginID {
				return nil, pluginErr("command not owned by plugin")
			}
			return &CommandStatusOutput{Body: status}, nil
		}
		resp := routeRPC(input.PluginID, "commands/status/get", map[string]string{"command_id": input.CommandID})
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var status types.CommandStatus
		json.Unmarshal(resp.Result, &status)
		return &CommandStatusOutput{Body: status}, nil
	})
}
