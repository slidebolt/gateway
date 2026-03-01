package main

// MCPBridge generates MCP tools automatically from the huma OpenAPI spec.
// Every REST route is available as an MCP tool — no per-route MCP code needed.
// When a new route is added to routes.go, it appears in MCP automatically.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type MCPBridge struct {
	mcpServer *server.MCPServer
}

func NewMCPBridge(api huma.API, baseURL string) *MCPBridge {
	s := server.NewMCPServer("SlideBolt Gateway", "1.1.0",
		server.WithToolCapabilities(true),
	)
	b := &MCPBridge{mcpServer: s}
	b.buildFromOpenAPI(api, baseURL)
	return b
}

func (b *MCPBridge) Serve() {
	if err := server.ServeStdio(b.mcpServer); err != nil {
		log.Printf("MCP Server error: %v", err)
	}
}

// buildFromOpenAPI walks the huma OpenAPI spec and registers one MCP tool per
// operation. The tool handler proxies the call to the REST API over HTTP.
func (b *MCPBridge) buildFromOpenAPI(api huma.API, baseURL string) {
	oapi := api.OpenAPI()
	if oapi == nil {
		return
	}

	type opEntry struct {
		method string
		op     *huma.Operation
	}

	for path, item := range oapi.Paths {
		ops := []opEntry{
			{"GET", item.Get},
			{"POST", item.Post},
			{"PUT", item.Put},
			{"DELETE", item.Delete},
			{"PATCH", item.Patch},
		}
		for _, e := range ops {
			if e.op == nil {
				continue
			}
			b.registerTool(e.method, path, e.op, baseURL)
		}
	}
}

func (b *MCPBridge) registerTool(method, path string, op *huma.Operation, baseURL string) {
	name := op.OperationID
	if name == "" {
		name = strings.ToLower(method) + "_" + pathToToolName(path)
	}

	desc := op.Summary
	if op.Description != "" {
		desc += "\n\n" + op.Description
	}

	var opts []mcp.ToolOption
	opts = append(opts, mcp.WithDescription(desc))

	for _, param := range op.Parameters {
		if param.In == "path" || param.In == "query" || param.In == "header" {
			pOpts := []mcp.PropertyOption{mcp.Description(param.Description)}
			opts = append(opts, mcp.WithString(param.Name, pOpts...))
		}
	}

	if op.RequestBody != nil {
		opts = append(opts, mcp.WithObject("body",
			mcp.Description("Request body as a JSON object"),
		))
	}

	b.mcpServer.AddTool(
		mcp.NewTool(name, opts...),
		makeHTTPHandler(method, path, op, baseURL),
	)
}

func makeHTTPHandler(method, pathTemplate string, op *huma.Operation, baseURL string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		if args == nil {
			args = map[string]any{}
		}

		// Fill path parameters.
		urlPath := pathTemplate
		queryParams := url.Values{}

		for _, param := range op.Parameters {
			val, ok := args[param.Name]
			if !ok || val == nil {
				continue
			}
			strVal := fmt.Sprintf("%v", val)
			switch param.In {
			case "path":
				urlPath = strings.ReplaceAll(urlPath, "{"+param.Name+"}", url.PathEscape(strVal))
			case "query":
				queryParams.Set(param.Name, strVal)
			case "header":
				// headers handled below
			}
		}

		fullURL := baseURL + urlPath
		if len(queryParams) > 0 {
			fullURL += "?" + queryParams.Encode()
		}

		var bodyReader io.Reader
		if body, ok := args["body"]; ok && body != nil {
			bodyBytes, err := marshalBody(body)
			if err != nil {
				return mcp.NewToolResultError("could not marshal body: " + err.Error()), nil
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if bodyReader != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}

		// Pass through header parameters.
		for _, param := range op.Parameters {
			if param.In != "header" {
				continue
			}
			if val, ok := args[param.Name]; ok && val != nil {
				httpReq.Header.Set(param.Name, fmt.Sprintf("%v", val))
			}
		}

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 {
			return mcp.NewToolResultError(string(respBody)), nil
		}
		return mcp.NewToolResultText(string(respBody)), nil
	}
}

func marshalBody(v any) ([]byte, error) {
	switch b := v.(type) {
	case []byte:
		return b, nil
	case string:
		return []byte(b), nil
	default:
		return json.Marshal(v)
	}
}

func pathToToolName(p string) string {
	p = strings.Trim(p, "/")
	p = strings.ReplaceAll(p, "{", "")
	p = strings.ReplaceAll(p, "}", "")
	p = strings.ReplaceAll(p, "/", "_")
	p = strings.ReplaceAll(p, "-", "_")
	return p
}
