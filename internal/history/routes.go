package history

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

type listJournalEventsInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	DeviceID string `query:"device_id" doc:"Filter by device ID"`
	EntityID string `query:"entity_id" doc:"Filter by entity ID"`
	Limit    int    `query:"limit" doc:"Max number of events to return (default: 100, max: 500)"`
}
type listJournalEventsOutput struct{ Body []observedEvent }

type pluginRatesInput struct {
	Window int `query:"window" doc:"Time window in seconds (default: 30)"`
}
type pluginRatesOutput struct{ Body []pluginRate }

type deviceRatesInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	Window   int    `query:"window" doc:"Time window in seconds (default: 30)"`
}
type deviceRatesOutput struct{ Body []deviceRate }

type entityRatesInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	DeviceID string `query:"device_id" doc:"Filter by device ID"`
	Window   int    `query:"window" doc:"Time window in seconds (default: 30)"`
}
type entityRatesOutput struct{ Body []entityRate }

type traceEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Since    string `query:"since" doc:"RFC3339 timestamp to fetch from"`
}
type traceOutput struct{ Body []traceEntry }

type getAnyCommandStatusInput struct {
	CommandID string `path:"command_id" doc:"Command ID"`
}
type commandStatusOutput struct{ Body types.CommandStatus }

type statsOutput struct{ Body Stats }

// RegisterRoutes registers all history-related HTTP routes on the given Huma API.
func (h *History) RegisterRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-event-journal",
		Method:      http.MethodGet,
		Path:        "/api/journal/events",
		Summary:     "Event journal",
		Description: "Returns a filtered log of recent entity state-change events observed by the gateway.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *listJournalEventsInput) (*listJournalEventsOutput, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		events, err := h.listEvents(input.PluginID, input.DeviceID, input.EntityID, limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query event history")
		}
		return &listJournalEventsOutput{Body: events}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-plugin-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/plugin-rates",
		Summary:     "Per-plugin activity rates",
		Description: "Returns per-plugin event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *pluginRatesInput) (*pluginRatesOutput, error) {
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := h.pluginRates(window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query plugin rates")
		}
		return &pluginRatesOutput{Body: rates}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-device-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/device-rates",
		Summary:     "Per-device activity rates",
		Description: "Returns per-device event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *deviceRatesInput) (*deviceRatesOutput, error) {
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := h.deviceRates(input.PluginID, window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query device rates")
		}
		return &deviceRatesOutput{Body: rates}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-entity-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/entity-rates",
		Summary:     "Per-entity activity rates",
		Description: "Returns per-entity event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *entityRatesInput) (*entityRatesOutput, error) {
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := h.entityRates(input.PluginID, input.DeviceID, window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query entity rates")
		}
		return &entityRatesOutput{Body: rates}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "trace-entity",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/trace",
		Summary:     "Trace entity events and commands",
		Description: "Returns events and commands for an entity since a given timestamp, sorted chronologically. Poll with the timestamp of the last entry to get live activity without backfill.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *traceEntityInput) (*traceOutput, error) {
		since := time.Now().UTC()
		if input.Since != "" {
			if t, err := time.Parse(time.RFC3339Nano, input.Since); err == nil {
				since = t
			} else if t, err := time.Parse(time.RFC3339, input.Since); err == nil {
				since = t
			}
		}
		entries, err := h.traceSince(input.PluginID, input.DeviceID, input.EntityID, since)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query entity trace")
		}
		return &traceOutput{Body: entries}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-any-command-status",
		Method:      http.MethodGet,
		Path:        "/api/commands/{command_id}",
		Summary:     "Get command status by ID",
		Description: "Returns the status of any command by ID, regardless of which plugin owns it. Covers commands issued directly between plugins via Ctx:SendCommand.",
		Tags:        []string{"commands"},
	}, func(ctx context.Context, input *getAnyCommandStatusInput) (*commandStatusOutput, error) {
		status, found, err := h.latestCommandStatus(input.CommandID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query command history")
		}
		if !found {
			return nil, huma.Error404NotFound("command not found", nil)
		}
		return &commandStatusOutput{Body: status}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "history-stats",
		Method:      http.MethodGet,
		Path:        "/_internal/history/stats",
		Summary:     "History store stats",
		Description: "Returns total event and command counts from the history store.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, _ *struct{}) (*statsOutput, error) {
		s, err := h.stats()
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to read history stats")
		}
		return &statsOutput{Body: s}, nil
	})
}
