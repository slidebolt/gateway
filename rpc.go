package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/slidebolt/sdk-types"
)

func routeRPC(pluginID, method string, params any) types.Response {
	started := time.Now()
	traceRPC := method == "entities/commands/create" || method == "commands/status/get" || method == "entities/list"
	regMu.RLock()
	record, exists := registry[pluginID]
	regMu.RUnlock()
	if !exists {
		log.Printf("gateway rpc: plugin not registered plugin=%s method=%s", pluginID, method)
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32000, Message: "plugin not registered"}}
	}
	reg := record.Registration
	paramsBytes, _ := json.Marshal(params)
	if traceRPC {
		log.Printf("gateway virtual-cmd: rpc start plugin=%s method=%s payload=%s", pluginID, method, string(paramsBytes))
	}
	id := json.RawMessage(`1`)
	req := types.Request{JSONRPC: types.JSONRPCVersion, ID: &id, Method: method, Params: paramsBytes}
	data, _ := json.Marshal(req)
	msg, err := nc.Request(reg.RPCSubject, data, 2*time.Second)
	if err != nil {
		log.Printf("gateway rpc: timeout plugin=%s method=%s duration_ms=%d err=%v", pluginID, method, time.Since(started).Milliseconds(), err)
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32000, Message: "plugin timeout"}}
	}
	var resp types.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		log.Printf("gateway rpc: malformed response plugin=%s method=%s duration_ms=%d err=%v", pluginID, method, time.Since(started).Milliseconds(), err)
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32700, Message: "malformed response from plugin"}}
	}
	if resp.Error != nil {
		log.Printf("gateway rpc: plugin returned error plugin=%s method=%s duration_ms=%d code=%d msg=%q", pluginID, method, time.Since(started).Milliseconds(), resp.Error.Code, resp.Error.Message)
	}
	if traceRPC && resp.Error == nil {
		log.Printf("gateway virtual-cmd: rpc ok plugin=%s method=%s duration_ms=%d", pluginID, method, time.Since(started).Milliseconds())
	}
	return resp
}

func parseEntities(resp types.Response) ([]types.Entity, error) {
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	var entities []types.Entity
	if err := json.Unmarshal(resp.Result, &entities); err != nil {
		return nil, err
	}
	return entities, nil
}

func parseDevices(resp types.Response) ([]types.Device, error) {
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	var devices []types.Device
	if err := json.Unmarshal(resp.Result, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

func findEntity(pluginID, deviceID, entityID string) (types.Entity, error) {
	if registryService != nil {
		results := registryService.FindEntities(types.SearchQuery{
			PluginID: pluginID,
			DeviceID: deviceID,
			EntityID: entityID,
			Limit:    1,
		})
		for _, ent := range results {
			if ent.PluginID == pluginID && ent.ID == entityID {
				if ent.DeviceID == "" {
					ent.DeviceID = deviceID
				}
				return ent, nil
			}
		}
	}

	// Gateway-owned entities exist only in the registry. Skip the NATS fallback
	// to avoid a timeout waiting for a plugin that doesn't exist.
	if isGatewayOwned(pluginID) {
		return types.Entity{}, fmt.Errorf("entity not found")
	}

	resp := routeRPC(pluginID, types.RPCMethodEntitiesList, gin.H{"device_id": deviceID})
	entities, err := parseEntities(resp)
	if err != nil {
		return types.Entity{}, err
	}
	for _, e := range entities {
		if e.ID == entityID {
			return e, nil
		}
	}
	return types.Entity{}, fmt.Errorf("entity not found")
}

func findDevice(pluginID, deviceID string) (types.Device, error) {
	if registryService != nil {
		results := registryService.FindDevices(types.SearchQuery{
			PluginID: pluginID,
			DeviceID: deviceID,
			Limit:    1,
		})
		for _, dev := range results {
			if dev.PluginID == pluginID && dev.ID == deviceID {
				return dev, nil
			}
		}
	}

	resp := routeRPC(pluginID, types.RPCMethodDevicesList, nil)
	devices, err := parseDevices(resp)
	if err != nil {
		return types.Device{}, err
	}
	for _, d := range devices {
		if d.ID == deviceID {
			return d, nil
		}
	}
	return types.Device{}, fmt.Errorf("device not found")
}

func fetchCommandStatus(pluginID, commandID string) (types.CommandStatus, error) {
	resp := routeRPC(pluginID, types.RPCMethodCommandsStatusGet, gin.H{"command_id": commandID})
	if resp.Error != nil {
		return types.CommandStatus{}, errors.New(resp.Error.Message)
	}
	var st types.CommandStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		return types.CommandStatus{}, err
	}
	return st, nil
}

func parseActionType(payload json.RawMessage) (string, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return "", err
	}
	if probe.Type == "" {
		return "", fmt.Errorf("payload.type is required")
	}
	return probe.Type, nil
}

func containsAction(actions []string, action string) bool {
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}
