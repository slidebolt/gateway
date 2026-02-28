package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

// MCPBridge wraps a standard MCP server and maps it to SlideBolt's internal logic.
type MCPBridge struct {
	mcpServer *server.MCPServer
}

func NewMCPBridge() *MCPBridge {
	s := server.NewMCPServer("SlideBolt Gateway", "1.1.0")

	b := &MCPBridge{mcpServer: s}
	b.installResources()
	b.installTools()

	return b
}

func (b *MCPBridge) Serve() {
	if err := server.ServeStdio(b.mcpServer); err != nil {
		log.Printf("MCP Server error: %v", err)
	}
}

func (b *MCPBridge) installResources() {
	// 1. Registry Resource
	b.mcpServer.AddResource(mcp.NewResource("slidebolt://registry", "Registry", mcp.WithResourceDescription("List of all active plugins and their manifests")),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			regMu.RLock()
			defer regMu.RUnlock()
			data, _ := json.MarshalIndent(registry, "", "  ")
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "slidebolt://registry",
					MIMEType: "application/json",
					Text:     string(data),
				},
			}, nil
		})

	// 2. Runtime Resource
	b.mcpServer.AddResource(mcp.NewResource("slidebolt://runtime", "Runtime", mcp.WithResourceDescription("Gateway runtime configuration")),
		func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			data, _ := json.MarshalIndent(gatewayRT, "", "  ")
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "slidebolt://runtime",
					MIMEType: "application/json",
					Text:     string(data),
				},
			}, nil
		})
}

func (b *MCPBridge) installTools() {
	// --- COMMAND TOOL ---
	b.mcpServer.AddTool(mcp.NewTool("execute_command",
		mcp.WithDescription("Execute a command on a specific entity (e.g., turn on a light, set brightness)"),
		mcp.WithString("plugin_id", mcp.Required(), mcp.Description("ID of the target plugin")),
		mcp.WithString("device_id", mcp.Required(), mcp.Description("ID of the target device")),
		mcp.WithString("entity_id", mcp.Required(), mcp.Description("ID of the target entity")),
		mcp.WithString("action", mcp.Required(), mcp.Description("The command action to perform (e.g., set_brightness, turn_on)")),
		mcp.WithObject("payload", mcp.Description("Optional JSON object for command parameters")),
	), b.handleExecuteCommand)

	// --- SEARCH TOOLS ---
	b.mcpServer.AddTool(mcp.NewTool("search_devices",
		mcp.WithDescription("Search for devices matching a name pattern"),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern for device ID or name")),
	), b.handleSearchDevices)

	b.mcpServer.AddTool(mcp.NewTool("search_entities",
		mcp.WithDescription("Search for entities matching patterns"),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern for entity properties")),
	), b.handleSearchEntities)

	// --- BATCH TOOLS ---
	b.mcpServer.AddTool(mcp.NewTool("batch_get_devices",
		mcp.WithDescription("Fetch multiple devices by ID from a specific plugin"),
		mcp.WithString("plugin_id", mcp.Required(), mcp.Description("Plugin ID")),
	), b.handleBatchGetDevices)
}

func (b *MCPBridge) handleExecuteCommand(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	pID, _ := args["plugin_id"].(string)
	dID, _ := args["device_id"].(string)
	eID, _ := args["entity_id"].(string)
	action, _ := args["action"].(string)
	
	payloadMap, ok := args["payload"].(map[string]any)
	if !ok {
		payloadMap = make(map[string]any)
	}
	payloadMap["type"] = action
	payloadBytes, _ := json.Marshal(payloadMap)
	
	result, err := b.callInternalRPC(pID, "entities/commands/create", map[string]any{
		"device_id": dID,
		"entity_id": eID,
		"payload":   json.RawMessage(payloadBytes),
	})
	
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	
	data, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func (b *MCPBridge) handleSearchDevices(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	pattern, _ := args["pattern"].(string)
	results := b.broadcastSearch(runner.SubjectSearchDevices, types.SearchQuery{Pattern: pattern})
	data, _ := json.MarshalIndent(results, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func (b *MCPBridge) handleSearchEntities(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	pattern, _ := args["pattern"].(string)
	results := b.broadcastSearch(runner.SubjectSearchEntities, types.SearchQuery{Pattern: pattern})
	data, _ := json.MarshalIndent(results, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func (b *MCPBridge) handleBatchGetDevices(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}
	pID, _ := args["plugin_id"].(string)
	
	results, err := b.callInternalRPC(pID, "devices/list", nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	
	data, _ := json.MarshalIndent(results, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func (b *MCPBridge) callInternalRPC(pluginID, method string, params any) (any, error) {
	regMu.RLock()
	reg, ok := registry[pluginID]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin %s not found", pluginID)
	}

	paramsBytes, _ := json.Marshal(params)
	id := json.RawMessage(`1`)
	req := types.Request{JSONRPC: types.JSONRPCVersion, ID: &id, Method: method, Params: paramsBytes}
	data, _ := json.Marshal(req)
	
	msg, err := nc.Request(reg.RPCSubject, data, 2000)
	if err != nil {
		return nil, err
	}
	
	var resp types.Response
	json.Unmarshal(msg.Data, &resp)
	if resp.Error != nil {
		return nil, fmt.Errorf(resp.Error.Message)
	}
	return resp.Result, nil
}

func (b *MCPBridge) broadcastSearch(subject string, query types.SearchQuery) []any {
	data, _ := json.Marshal(query)
	msgs, err := nc.Request(subject, data, 500)
	if err != nil {
		return []any{}
	}
	
	var pluginResults []any
	if err := json.Unmarshal(msgs.Data, &pluginResults); err == nil {
		return pluginResults
	}
	return []any{}
}