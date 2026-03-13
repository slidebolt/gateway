package main

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

// buildRouter creates and configures the Gin router with all API routes registered.
// It uses the package-level globals (nc, js, historyService, registryService,
// commandService) which must be initialized before calling this.
// Callers can wrap the returned *gin.Engine in an http.Server or httptest.NewServer.
func buildRouter() (*gin.Engine, huma.API) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(requestLogger(), gin.Recovery())

	config := huma.DefaultConfig("SlideBolt Gateway API", "1.0.0")
	config.Info.Description = "REST API for managing plugins, devices, entities, scripts, commands, and events. " +
		"Plugin-scoped routes proxy requests to the target plugin over NATS. " +
		"Also available as an MCP server over stdio."

	// Keep error responses as {"error": "message"} to match existing wire format.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		detail := msg
		if len(errs) > 0 && errs[0] != nil && errs[0].Error() != "" {
			detail = errs[0].Error()
		}
		return &apiError{status: status, Message: detail}
	}

	api := humagin.New(r, config)
	registerRoutes(api)
	if historyService != nil {
		historyService.RegisterRoutes(api)
		r.GET("/api/topics/subscribe", historyService.SSEHandler())
	}

	return r, api
}
