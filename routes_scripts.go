package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// --- Script types ---

type GetScriptInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type ScriptOutput struct{ Body json.RawMessage }

type SetScriptInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		Source string `json:"source" doc:"Lua script source code"`
	}
}

type DeleteScriptInput struct {
	PluginID   string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID   string `path:"device_id" doc:"Device ID"`
	EntityID   string `path:"entity_id" doc:"Entity ID"`
	PurgeState bool   `query:"purge_state" doc:"Also delete persisted script state (default: false)"`
}

type GetScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type ScriptStateOutput struct{ Body json.RawMessage }

type SetScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		State map[string]any `json:"state" doc:"Key-value state map persisted across script invocations"`
	}
}

type DeleteScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}

func registerScriptRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-script",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Get script",
		Description: "Returns the automation script source for an entity.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *GetScriptInput) (*ScriptOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/get", params)
		if resp.Error != nil {
			if resp.Error.Code == -32004 || resp.Error.Code == -32005 {
				return nil, notFoundErr(resp.Error.Message)
			}
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-script",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Set script",
		Description: "Installs or replaces an automation script on an entity.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *SetScriptInput) (*ScriptOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "source": input.Body.Source}
		resp := routeRPC(input.PluginID, "scripts/put", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-script",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Delete script",
		Description: "Removes the automation script from an entity. Use ?purge_state=true to also delete persisted script state.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *DeleteScriptInput) (*ScriptOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "purge_state": input.PurgeState}
		resp := routeRPC(input.PluginID, "scripts/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-script-state",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Get script state",
		Description: "Returns the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *GetScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/state/get", params)
		if resp.Error != nil {
			if resp.Error.Code == -32005 || strings.Contains(strings.ToLower(resp.Error.Message), "not found") {
				return nil, notFoundErr(resp.Error.Message)
			}
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-script-state",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Set script state",
		Description: "Replaces the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *SetScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "state": input.Body.State}
		resp := routeRPC(input.PluginID, "scripts/state/put", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-script-state",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Delete script state",
		Description: "Clears the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *DeleteScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/state/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})
}
