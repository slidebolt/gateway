package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// ---------------------------------------------------------------------------
// Batch input / output types
// ---------------------------------------------------------------------------

type BatchGetDevicesInput struct {
	Body []types.BatchDeviceRef `doc:"List of (plugin_id, device_id) pairs to fetch"`
}
type BatchGetDevicesOutput struct{ Body []types.BatchResult }

type BatchCreateDevicesInput struct {
	Body []types.BatchDeviceItem `doc:"List of (plugin_id, device) items to create"`
}
type BatchCreateDevicesOutput struct{ Body []types.BatchResult }

type BatchUpdateDevicesInput struct {
	Body []types.BatchDeviceItem `doc:"List of (plugin_id, device) items to update"`
}
type BatchUpdateDevicesOutput struct{ Body []types.BatchResult }

type BatchDeleteDevicesInput struct {
	Body []types.BatchDeviceRef `doc:"List of (plugin_id, device_id) pairs to delete"`
}
type BatchDeleteDevicesOutput struct{ Body []types.BatchResult }

type BatchGetEntitiesInput struct {
	Body []types.BatchEntityRef `doc:"List of (plugin_id, device_id, entity_id) triples to fetch"`
}
type BatchGetEntitiesOutput struct{ Body []types.BatchResult }

type BatchCreateEntitiesInput struct {
	Body []types.BatchEntityItem `doc:"List of (plugin_id, device_id, entity) items to create"`
}
type BatchCreateEntitiesOutput struct{ Body []types.BatchResult }

type BatchUpdateEntitiesInput struct {
	Body []types.BatchEntityItem `doc:"List of (plugin_id, device_id, entity) items to update"`
}
type BatchUpdateEntitiesOutput struct{ Body []types.BatchResult }

type BatchDeleteEntitiesInput struct {
	Body []types.BatchEntityRef `doc:"List of (plugin_id, device_id, entity_id) triples to delete"`
}
type BatchDeleteEntitiesOutput struct{ Body []types.BatchResult }

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

func registerBatchRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "batch-get-devices",
		Method:      http.MethodPost,
		Path:        "/api/batch/devices",
		Summary:     "Fetch devices",
		Description: "Fetches specific devices by (plugin_id, device_id). Groups requests by plugin for efficiency.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchGetDevicesInput) (*BatchGetDevicesOutput, error) {
		return &BatchGetDevicesOutput{Body: batchGetDevices(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-create-devices",
		Method:      http.MethodPost,
		Path:        "/api/batch/devices/create",
		Summary:     "Create devices",
		Description: "Creates multiple devices across plugins in a single call.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchCreateDevicesInput) (*BatchCreateDevicesOutput, error) {
		return &BatchCreateDevicesOutput{Body: batchCreateDevices(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-update-devices",
		Method:      http.MethodPut,
		Path:        "/api/batch/devices",
		Summary:     "Update devices",
		Description: "Updates multiple devices across plugins in a single call.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchUpdateDevicesInput) (*BatchUpdateDevicesOutput, error) {
		return &BatchUpdateDevicesOutput{Body: batchUpdateDevices(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-delete-devices",
		Method:      http.MethodDelete,
		Path:        "/api/batch/devices",
		Summary:     "Delete devices",
		Description: "Deletes multiple devices across plugins. Pass device refs in the request body.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchDeleteDevicesInput) (*BatchDeleteDevicesOutput, error) {
		return &BatchDeleteDevicesOutput{Body: batchDeleteDevices(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-get-entities",
		Method:      http.MethodPost,
		Path:        "/api/batch/entities",
		Summary:     "Fetch entities",
		Description: "Fetches specific entities by (plugin_id, device_id, entity_id). Groups requests by device for efficiency.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchGetEntitiesInput) (*BatchGetEntitiesOutput, error) {
		return &BatchGetEntitiesOutput{Body: batchGetEntities(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-create-entities",
		Method:      http.MethodPost,
		Path:        "/api/batch/entities/create",
		Summary:     "Create entities",
		Description: "Creates multiple entities across plugins in a single call.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchCreateEntitiesInput) (*BatchCreateEntitiesOutput, error) {
		return &BatchCreateEntitiesOutput{Body: batchCreateEntities(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-update-entities",
		Method:      http.MethodPut,
		Path:        "/api/batch/entities",
		Summary:     "Update entities",
		Description: "Updates multiple entities across plugins in a single call.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchUpdateEntitiesInput) (*BatchUpdateEntitiesOutput, error) {
		return &BatchUpdateEntitiesOutput{Body: batchUpdateEntities(input.Body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "batch-delete-entities",
		Method:      http.MethodDelete,
		Path:        "/api/batch/entities",
		Summary:     "Delete entities",
		Description: "Deletes multiple entities across plugins. Pass entity refs in the request body.",
		Tags:        []string{"batch"},
	}, func(ctx context.Context, input *BatchDeleteEntitiesInput) (*BatchDeleteEntitiesOutput, error) {
		return &BatchDeleteEntitiesOutput{Body: batchDeleteEntities(input.Body)}, nil
	})
}

// ---------------------------------------------------------------------------
// Handlers (logic unchanged from original batch.go)
// ---------------------------------------------------------------------------

func batchGetDevices(refs []types.BatchDeviceRef) []types.BatchResult {
	byPlugin := map[string][]string{}
	for _, ref := range refs {
		byPlugin[ref.PluginID] = append(byPlugin[ref.PluginID], ref.DeviceID)
	}
	index := map[string]types.Device{}
	pluginErr := map[string]string{}
	for pluginID, ids := range byPlugin {
		resp := routeRPC(pluginID, "devices/list", nil)
		if resp.Error != nil {
			for _, id := range ids {
				pluginErr[pluginID+"|"+id] = resp.Error.Message
			}
			continue
		}
		var devices []types.Device
		if err := json.Unmarshal(resp.Result, &devices); err != nil {
			for _, id := range ids {
				pluginErr[pluginID+"|"+id] = err.Error()
			}
			continue
		}
		for _, d := range devices {
			index[pluginID+"|"+d.ID] = d
		}
	}
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		key := ref.PluginID + "|" + ref.DeviceID
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID}
		if errMsg, bad := pluginErr[key]; bad {
			r.Error = errMsg
		} else if dev, found := index[key]; found {
			r.OK = true
			r.Data, _ = json.Marshal(dev)
		} else {
			r.Error = "not found"
		}
		results[i] = r
	}
	return results
}

func batchCreateDevices(items []types.BatchDeviceItem) []types.BatchResult {
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.Device.ID}
		resp := routeRPC(item.PluginID, "devices/create", item.Device)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	return results
}

func batchUpdateDevices(items []types.BatchDeviceItem) []types.BatchResult {
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.Device.ID}
		resp := routeRPC(item.PluginID, "devices/update", item.Device)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	return results
}

func batchDeleteDevices(refs []types.BatchDeviceRef) []types.BatchResult {
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID}
		resp := routeRPC(ref.PluginID, "devices/delete", ref.DeviceID)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
		}
		results[i] = r
	}
	return results
}

func batchGetEntities(refs []types.BatchEntityRef) []types.BatchResult {
	type deviceKey struct{ pluginID, deviceID string }
	byDevice := map[deviceKey][]string{}
	for _, ref := range refs {
		k := deviceKey{ref.PluginID, ref.DeviceID}
		byDevice[k] = append(byDevice[k], ref.EntityID)
	}
	index := map[string]types.Entity{}
	deviceErr := map[string]string{}
	for k := range byDevice {
		resp := routeRPC(k.pluginID, "entities/list", map[string]string{"device_id": k.deviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			deviceErr[k.pluginID+"|"+k.deviceID] = err.Error()
			continue
		}
		for _, e := range entities {
			index[k.pluginID+"|"+k.deviceID+"|"+e.ID] = e
		}
	}
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID, EntityID: ref.EntityID}
		devKey := ref.PluginID + "|" + ref.DeviceID
		if errMsg, bad := deviceErr[devKey]; bad {
			r.Error = errMsg
		} else if ent, found := index[devKey+"|"+ref.EntityID]; found {
			r.OK = true
			r.Data, _ = json.Marshal(ent)
		} else {
			r.Error = "not found"
		}
		results[i] = r
	}
	return results
}

func batchCreateEntities(items []types.BatchEntityItem) []types.BatchResult {
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		item.Entity.DeviceID = item.DeviceID
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.DeviceID, EntityID: item.Entity.ID}
		resp := routeRPC(item.PluginID, "entities/create", item.Entity)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	return results
}

func batchUpdateEntities(items []types.BatchEntityItem) []types.BatchResult {
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		item.Entity.DeviceID = item.DeviceID
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.DeviceID, EntityID: item.Entity.ID}
		resp := routeRPC(item.PluginID, "entities/update", item.Entity)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	return results
}

func batchDeleteEntities(refs []types.BatchEntityRef) []types.BatchResult {
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID, EntityID: ref.EntityID}
		params := map[string]string{"device_id": ref.DeviceID, "entity_id": ref.EntityID}
		resp := routeRPC(ref.PluginID, "entities/delete", params)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
		}
		results[i] = r
	}
	return results
}
