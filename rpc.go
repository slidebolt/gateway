package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/slidebolt/sdk-types"
)

func routeRPC(pluginID, method string, params any) types.Response {
	regMu.RLock()
	record, exists := registry[pluginID]
	regMu.RUnlock()
	if !exists {
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32000, Message: "plugin not registered"}}
	}
	reg := record.Registration
	paramsBytes, _ := json.Marshal(params)
	id := json.RawMessage(`1`)
	req := types.Request{JSONRPC: types.JSONRPCVersion, ID: &id, Method: method, Params: paramsBytes}
	data, _ := json.Marshal(req)
	msg, err := nc.Request(reg.RPCSubject, data, 2*time.Second)
	if err != nil {
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32000, Message: "plugin timeout"}}
	}
	var resp types.Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return types.Response{JSONRPC: types.JSONRPCVersion, Error: &types.RPCError{Code: -32700, Message: "malformed response from plugin"}}
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
	resp := routeRPC(pluginID, "entities/list", gin.H{"device_id": deviceID})
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
	resp := routeRPC(pluginID, "devices/list", nil)
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
	resp := routeRPC(pluginID, "commands/status/get", gin.H{"command_id": commandID})
	if resp.Error != nil {
		return types.CommandStatus{}, errors.New(resp.Error.Message)
	}
	var st types.CommandStatus
	if err := json.Unmarshal(resp.Result, &st); err != nil {
		return types.CommandStatus{}, err
	}
	return st, nil
}

func fetchAnyCommandStatus(pluginID, commandID string) (types.CommandStatus, error) {
	vstore.mu.RLock()
	if rec, ok := vstore.commands[commandID]; ok {
		vstore.mu.RUnlock()
		return rec.Status, nil
	}
	vstore.mu.RUnlock()
	return fetchCommandStatus(pluginID, commandID)
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
