// Package mcp provides an MCP bridge that auto-generates tools from the
// gateway's OpenAPI spec, so every REST route is available as an MCP tool
// without per-route MCP code.
package mcp

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
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Bridge wraps an MCP server and proxies tool calls to the REST API.
type Bridge struct {
	mcpServer *server.MCPServer
}

// New creates a Bridge populated with one MCP tool per OpenAPI operation.
func New(api huma.API, baseURL string) *Bridge {
	s := server.NewMCPServer("SlideBolt Gateway", "1.1.0",
		server.WithToolCapabilities(true),
	)
	b := &Bridge{mcpServer: s}
	b.buildFromOpenAPI(api, baseURL)
	return b
}

// Serve runs the MCP server over stdio (blocks until the process exits).
func (b *Bridge) Serve() {
	if err := server.ServeStdio(b.mcpServer); err != nil {
		log.Printf("MCP Server error: %v", err)
	}
}

func (b *Bridge) buildFromOpenAPI(api huma.API, baseURL string) {
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

func (b *Bridge) registerTool(method, path string, op *huma.Operation, baseURL string) {
	name := op.OperationID
	if name == "" {
		name = strings.ToLower(method) + "_" + pathToToolName(path)
	}

	desc := op.Summary
	if op.Description != "" {
		desc += "\n\n" + op.Description
	}

	var opts []mcplib.ToolOption
	opts = append(opts, mcplib.WithDescription(desc))

	for _, param := range op.Parameters {
		if param.In == "path" || param.In == "query" || param.In == "header" {
			pOpts := []mcplib.PropertyOption{mcplib.Description(param.Description)}
			opts = append(opts, mcplib.WithString(param.Name, pOpts...))
		}
	}

	if op.RequestBody != nil {
		opts = append(opts, mcplib.WithObject("body",
			mcplib.Description("Request body as a JSON object"),
		))
	}

	b.mcpServer.AddTool(
		mcplib.NewTool(name, opts...),
		makeHTTPHandler(method, path, op, baseURL),
	)
}

func makeHTTPHandler(method, pathTemplate string, op *huma.Operation, baseURL string) func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		args, _ := req.Params.Arguments.(map[string]any)
		if args == nil {
			args = map[string]any{}
		}

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
				return mcplib.NewToolResultError("could not marshal body: " + err.Error()), nil
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		if bodyReader != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}

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
			return mcplib.NewToolResultError(err.Error()), nil
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 400 {
			return mcplib.NewToolResultError(string(respBody)), nil
		}
		return mcplib.NewToolResultText(string(respBody)), nil
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
