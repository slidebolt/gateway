package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- Device types ---

type ListDevicesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type ListDevicesOutput struct{ Body []types.Device }

type RefreshDevicesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type FlushPluginStorageInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type ResetPluginInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type FlushPluginStorageOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}
type ResetPluginOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

type GetPluginLogLevelInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}

type SetPluginLogLevelInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	Body     struct {
		Level string `json:"level" doc:"Log level: trace|debug|info|warn|error"`
	}
}

type PluginLogLevelOutput struct {
	Body struct {
		Level string `json:"level"`
	}
}

type CreateDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	Body     types.Device
}
type DeviceOutput struct{ Body json.RawMessage }

type UpdateDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	Body     types.Device
}

type PatchDeviceNameInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	Body     struct {
		LocalName string `json:"local_name" doc:"New user-facing name"`
	}
}

type PatchDeviceLabelsInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	Body     struct {
		Labels map[string][]string `json:"labels" doc:"Full labels map (string -> string[])"`
	}
}

type DeleteDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}
type DeleteOutput struct{ Body json.RawMessage }
type GetDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}
type GetDeviceOutput struct{ Body types.Device }

func registerDeviceRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-devices",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices",
		Summary:     "List devices",
		Description: "Returns all devices owned by the given plugin.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *ListDevicesInput) (*ListDevicesOutput, error) {
		resp := routeRPC(input.PluginID, "devices/list", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var devices []types.Device
		json.Unmarshal(resp.Result, &devices)
		return &ListDevicesOutput{Body: devices}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "refresh-devices",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/refresh",
		Summary:     "Refresh plugin discovery",
		Description: "Re-runs discovery for all devices and entities managed by the plugin.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *RefreshDevicesInput) (*ListDevicesOutput, error) {
		resp := routeRPC(input.PluginID, "devices/refresh", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var devices []types.Device
		json.Unmarshal(resp.Result, &devices)
		return &ListDevicesOutput{Body: devices}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "flush-plugin-storage",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/storage/flush",
		Summary:     "Flush plugin storage to disk",
		Description: "Forces the plugin runner to persist its in-memory canonical state to disk immediately.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, input *FlushPluginStorageInput) (*FlushPluginStorageOutput, error) {
		resp := routeRPC(input.PluginID, "storage/flush", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		out := FlushPluginStorageOutput{}
		out.Body.OK = true
		return &out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "reset-plugin",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/reset",
		Summary:     "Reset plugin",
		Description: "Calls plugin OnReset hook. Default plugin behavior is no-op until implemented.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, input *ResetPluginInput) (*ResetPluginOutput, error) {
		resp := routeRPC(input.PluginID, "plugin/reset", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		out := ResetPluginOutput{}
		out.Body.OK = true
		return &out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-plugin-log-level",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/logging/level",
		Summary:     "Get plugin log level",
		Description: "Returns current runtime log level for one plugin process.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, input *GetPluginLogLevelInput) (*PluginLogLevelOutput, error) {
		resp := routeRPC(input.PluginID, "logging/get_level", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var out struct {
			Level string `json:"level"`
		}
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, pluginErr("failed to decode plugin log level response")
		}
		return &PluginLogLevelOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-plugin-log-level",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/logging/level",
		Summary:     "Set plugin log level",
		Description: "Updates runtime log level for one plugin process without restart.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, input *SetPluginLogLevelInput) (*PluginLogLevelOutput, error) {
		level := strings.TrimSpace(strings.ToLower(input.Body.Level))
		if level == "" {
			return nil, badReqErr("level is required")
		}
		resp := routeRPC(input.PluginID, "logging/set_level", map[string]string{"level": level})
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var out struct {
			Level string `json:"level"`
		}
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			return nil, pluginErr("failed to decode plugin log level response")
		}
		return &PluginLogLevelOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-device",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices",
		Summary:     "Create device",
		Description: "Creates a new device in the given plugin.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *CreateDeviceInput) (*DeviceOutput, error) {
		resp := routeRPC(input.PluginID, "devices/create", input.Body)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		saveDeviceToRegistry(input.PluginID, resp.Result, input.Body)
		return &DeviceOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-device",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices",
		Summary:     "Update device",
		Description: "Updates device properties (local_name, labels). The device.id field identifies which device to update.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *UpdateDeviceInput) (*DeviceOutput, error) {
		resp := routeRPC(input.PluginID, "devices/update", input.Body)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		saveDeviceToRegistry(input.PluginID, resp.Result, input.Body)
		historyService.BroadcastDevice(input.PluginID, input.Body.ID)
		return &DeviceOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "patch-device-name",
		Method:      http.MethodPatch,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/name",
		Summary:     "Patch device name",
		Description: "Updates only device.local_name.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *PatchDeviceNameInput) (*DeviceOutput, error) {
		name := strings.TrimSpace(input.Body.LocalName)
		if name == "" {
			return nil, badReqErr("local_name is required")
		}
		payload := types.Device{ID: input.DeviceID, LocalName: name}
		resp := routeRPC(input.PluginID, "devices/update", payload)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		saveDeviceToRegistry(input.PluginID, resp.Result, payload)
		historyService.BroadcastDevice(input.PluginID, input.DeviceID)
		return &DeviceOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "patch-device-labels",
		Method:      http.MethodPatch,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/labels",
		Summary:     "Patch device labels",
		Description: "Updates only device.labels. Provide map[string][]string.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *PatchDeviceLabelsInput) (*DeviceOutput, error) {
		if len(input.Body.Labels) == 0 {
			return nil, badReqErr("labels is required")
		}
		payload := types.Device{ID: input.DeviceID, Labels: input.Body.Labels}
		resp := routeRPC(input.PluginID, "devices/update", payload)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		saveDeviceToRegistry(input.PluginID, resp.Result, payload)
		historyService.BroadcastDevice(input.PluginID, input.DeviceID)
		return &DeviceOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-device",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}",
		Summary:     "Delete device",
		Description: "Deletes a device from the given plugin.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *DeleteDeviceInput) (*DeleteOutput, error) {
		resp := routeRPC(input.PluginID, "devices/delete", input.DeviceID)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		_ = registryService.DeleteDevice(input.DeviceID)
		return &DeleteOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-device",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}",
		Summary:     "Get device",
		Description: "Returns one device by ID.",
		Tags:        []string{"devices"},
	}, func(ctx context.Context, input *GetDeviceInput) (*GetDeviceOutput, error) {
		resp := routeRPC(input.PluginID, "devices/list", nil)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var devices []types.Device
		if err := json.Unmarshal(resp.Result, &devices); err != nil {
			return nil, pluginErr("failed to decode devices")
		}
		for _, d := range devices {
			if d.ID == input.DeviceID {
				return &GetDeviceOutput{Body: d}, nil
			}
		}
		return nil, notFoundErr("device not found")
	})
}
