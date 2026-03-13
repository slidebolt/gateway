package main

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// apiError serialises as {"error":"message"} and satisfies huma.StatusError.
type apiError struct {
	status  int
	Message string `json:"error"`
}

func (e *apiError) Error() string  { return e.Message }
func (e *apiError) GetStatus() int { return e.status }

func pluginErr(msg string) error   { return &apiError{status: http.StatusForbidden, Message: msg} }
func badReqErr(msg string) error   { return &apiError{status: http.StatusBadRequest, Message: msg} }
func notFoundErr(msg string) error { return &apiError{status: http.StatusNotFound, Message: msg} }
func conflictErr(msg string) error { return &apiError{status: http.StatusConflict, Message: msg} }
func timeoutErr(msg string) error  { return &apiError{status: http.StatusGatewayTimeout, Message: msg} }
func upstreamErr(msg string) error {
	return &apiError{status: http.StatusServiceUnavailable, Message: msg}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

func registerRoutes(api huma.API) {
	registerSystemRoutes(api)
	registerDeviceRoutes(api)
	registerEntityRoutes(api)
	registerScriptRoutes(api)
	registerSnapshotRoutes(api)
	registerCommandRoutes(api)
	registerEventRoutes(api)
	registerEventSubscriptionRoutes(api)
	registerSearchRoutes(api)
	registerSchemaRoutes(api)
	registerBatchRoutes(api)
}
