package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
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

// ---------------------------------------------------------------------------
// Input / output types — one per route, named clearly for OpenAPI schema gen.
// ---------------------------------------------------------------------------

// --- System ---

type HealthInput struct {
	PluginID string `query:"id" doc:"Plugin ID for plugin-specific health check (optional)"`
}
type HealthOutput struct{ Body map[string]any }

type RuntimeOutput struct{ Body gatewayRuntimeInfo }
type HistoryStatsOutput struct{ Body historyStats }

type ListPluginsOutput struct{ Body map[string]types.Registration }

// --- Devices ---

type ListDevicesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type ListDevicesOutput struct{ Body []types.Device }

type RefreshDevicesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
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

// --- Entities ---

type entityWithSchema struct {
	types.Entity
	Schema *types.DomainDescriptor `json:"schema,omitempty"`
}

type ListEntitiesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}
type ListEntitiesOutput struct{ Body []entityWithSchema }

type RefreshEntitiesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}

type CreateEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	Body     types.Entity
}
type EntityOutput struct{ Body json.RawMessage }

type UpdateEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	Body     types.Entity
}

type PatchEntityNameInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		LocalName string `json:"local_name" doc:"New user-facing name"`
	}
}

type PatchEntityLabelsInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		Labels map[string][]string `json:"labels" doc:"Full labels map (string -> string[])"`
	}
}

type DeleteEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type GetEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type GetEntityOutput struct{ Body entityWithSchema }

type GetEntityEventsInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}

type entityEventsDescriptor struct {
	PluginID    string                   `json:"plugin_id"`
	DeviceID    string                   `json:"device_id"`
	EntityID    string                   `json:"entity_id"`
	Domain      string                   `json:"domain"`
	ValidEvents []types.ActionDescriptor `json:"valid_events"`
}

type EntityEventsOutput struct{ Body entityEventsDescriptor }

type CreateVirtualEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID that will own the virtual entity"`
	DeviceID string `path:"device_id" doc:"Device ID that will own the virtual entity"`
	Body     struct {
		ID             string   `json:"id" doc:"ID for the virtual entity (must be unique within the device)"`
		LocalName      string   `json:"local_name,omitempty" doc:"Display name (defaults to source entity's local_name)"`
		Actions        []string `json:"actions,omitempty" doc:"Subset of actions to expose (defaults to source entity's actions)"`
		SourcePluginID string   `json:"source_plugin_id" doc:"Plugin ID of the source entity"`
		SourceDeviceID string   `json:"source_device_id" doc:"Device ID of the source entity"`
		SourceEntityID string   `json:"source_entity_id" doc:"Entity ID of the source entity"`
		MirrorSource   *bool    `json:"mirror_source,omitempty" doc:"Keep virtual entity state in sync with source (default: true)"`
	}
}
type VirtualEntityOutput struct{ Body types.Entity }

// --- Scripts ---

type GetScriptInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type ScriptOutput struct{ Body json.RawMessage }

type SetScriptInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		Source string `json:"source" doc:"Lua script source code"`
	}
}

type DeleteScriptInput struct {
	PluginID   string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID   string `path:"device_id" doc:"Device ID"`
	EntityID   string `path:"entity_id" doc:"Entity ID"`
	PurgeState bool   `query:"purge_state" doc:"Also delete persisted script state (default: false)"`
}

type GetScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}
type ScriptStateOutput struct{ Body json.RawMessage }

type SetScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		State map[string]any `json:"state" doc:"Key-value state map persisted across script invocations"`
	}
}

type DeleteScriptStateInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}

// --- Snapshots ---

type SaveSnapshotInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
	Body     struct {
		Name   string              `json:"name" doc:"Human-readable snapshot name (e.g. MovieTime)"`
		Labels map[string][]string `json:"labels,omitempty" doc:"Optional labels for discovery"`
	}
}
type SnapshotOutput struct{ Body json.RawMessage }
type ListSnapshotsOutput struct{ Body json.RawMessage }

type DeleteSnapshotInput struct {
	PluginID   string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID   string `path:"device_id" doc:"Device ID"`
	EntityID   string `path:"entity_id" doc:"Entity ID"`
	SnapshotID string `path:"snapshot_id" doc:"Snapshot UUID"`
}

type RestoreSnapshotInput struct {
	PluginID   string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID   string `path:"device_id" doc:"Device ID"`
	EntityID   string `path:"entity_id" doc:"Entity ID"`
	SnapshotID string `path:"snapshot_id" doc:"Snapshot UUID"`
}

// --- Commands ---

type SendCommandInput struct {
	PluginID string         `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string         `path:"device_id" doc:"Device ID"`
	EntityID string         `path:"entity_id" doc:"Entity ID"`
	Body     map[string]any `doc:"Domain-specific command payload. Must include an 'action' field (e.g. {\"action\":\"turn_on\"})."`
}
type CommandStatusOutput struct{ Body types.CommandStatus }

type GetCommandStatusInput struct {
	PluginID  string `path:"plugin_id" doc:"Plugin ID"`
	CommandID string `path:"command_id" doc:"Command ID returned by the send-command endpoint"`
}

type GetAnyCommandStatusInput struct {
	CommandID string `path:"command_id" doc:"Command ID"`
}

// --- Events ---

type IngestEventInput struct {
	PluginID      string         `path:"plugin_id" doc:"Plugin ID"`
	DeviceID      string         `path:"device_id" doc:"Device ID"`
	EntityID      string         `path:"entity_id" doc:"Entity ID"`
	CorrelationID string         `header:"X-Correlation-ID" doc:"Optional command ID this event is responding to. Marks that command as succeeded."`
	Body          map[string]any `doc:"Domain-specific event payload. Must include an 'action' field (e.g. {\"action\":\"state\",\"on\":true})."`
}
type IngestEventOutput struct{ Body types.Entity }

type ListJournalEventsInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	DeviceID string `query:"device_id" doc:"Filter by device ID"`
	EntityID string `query:"entity_id" doc:"Filter by entity ID"`
	Limit    int    `query:"limit" doc:"Max number of events to return (default: 100, max: 500)"`
}
type ListJournalEventsOutput struct{ Body []observedEvent }

type PluginRatesInput struct {
	Window int `query:"window" doc:"Window size in seconds (default: 30)"`
}
type PluginRatesOutput struct{ Body []pluginRate }

type DeviceRatesInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	Window   int    `query:"window" doc:"Window size in seconds (default: 30)"`
}
type DeviceRatesOutput struct{ Body []deviceRate }

type EntityRatesInput struct {
	PluginID string `query:"plugin_id" doc:"Filter by plugin ID"`
	DeviceID string `query:"device_id" doc:"Filter by device ID"`
	Window   int    `query:"window" doc:"Window size in seconds (default: 30)"`
}
type EntityRatesOutput struct{ Body []entityRate }

// --- Search ---

type SearchPluginsInput struct {
	Pattern string `query:"q" doc:"Glob-style search pattern (default: *)"`
}
type SearchPluginsOutput struct{ Body []types.Manifest }

type SearchDevicesInput struct {
	Pattern  string   `query:"q" doc:"Glob-style search pattern (default: *)"`
	Labels   []string `query:"label,explode" doc:"Label filters in key:value format. Multiple values use AND logic (e.g. room:kitchen)."`
	PluginID string   `query:"plugin_id" doc:"Optional plugin scope."`
	DeviceID string   `query:"device_id" doc:"Optional device scope."`
	Limit    int      `query:"limit" doc:"Optional max results; applied after aggregation."`
}
type SearchDevicesOutput struct{ Body []types.Device }

type SearchEntitiesInput struct {
	Pattern  string   `query:"q" doc:"Glob-style search pattern or text to search for (default: *)"`
	Labels   []string `query:"label,explode" doc:"Label filters in key:value format. Multiple values use AND logic."`
	PluginID string   `query:"plugin_id" doc:"Optional plugin scope."`
	DeviceID string   `query:"device_id" doc:"Optional device scope."`
	EntityID string   `query:"entity_id" doc:"Optional entity scope."`
	Domain   string   `query:"domain" doc:"Optional domain scope (e.g. light, switch)."`
	Limit    int      `query:"limit" doc:"Optional max results; applied after aggregation."`
}
type SearchEntitiesOutput struct{ Body []entityWithPlugin }

// --- Schema ---

type GetDomainInput struct {
	Domain string `path:"domain" doc:"Domain name (e.g. light, switch, sensor, binary_sensor)"`
}
type DomainListOutput struct{ Body []types.DomainDescriptor }
type DomainOutput struct{ Body types.DomainDescriptor }

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
	registerSearchRoutes(api)
	registerSchemaRoutes(api)
	registerBatchRoutes(api)
}

func registerSystemRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health-check",
		Method:      http.MethodGet,
		Path:        runner.HealthEndpoint,
		Summary:     "Health check",
		Description: "Returns gateway health. Pass ?id=plugin_id to check a specific plugin's health.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, input *HealthInput) (*HealthOutput, error) {
		if input.PluginID == "" {
			return &HealthOutput{Body: map[string]any{"status": "ok"}}, nil
		}
		regMu.RLock()
		record, ok := registry[input.PluginID]
		regMu.RUnlock()
		if !ok {
			return nil, pluginErr("plugin not found")
		}
		resp := routeRPC(record.Registration.Manifest.ID, runner.HealthEndpoint, nil)
		if resp.Error != nil {
			return nil, &apiError{status: http.StatusServiceUnavailable, Message: resp.Error.Message}
		}
		var result map[string]any
		json.Unmarshal(resp.Result, &result)
		return &HealthOutput{Body: result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "runtime-info",
		Method:      http.MethodGet,
		Path:        "/_internal/runtime",
		Summary:     "Gateway runtime info",
		Description: "Returns gateway runtime metadata including the NATS server URL.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, input *struct{}) (*RuntimeOutput, error) {
		return &RuntimeOutput{Body: gatewayRT}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "history-stats",
		Method:      http.MethodGet,
		Path:        "/_internal/history/stats",
		Summary:     "History store stats",
		Description: "Returns SQLite-backed gateway history row counts for events and command statuses.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, input *struct{}) (*HistoryStatsOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		stats, err := history.Stats()
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to read history stats")
		}
		return &HistoryStatsOutput{Body: stats}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-plugins",
		Method:      http.MethodGet,
		Path:        "/api/plugins",
		Summary:     "List registered plugins",
		Description: "Returns all plugins that have registered with the gateway via NATS, keyed by plugin ID.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, input *struct{}) (*ListPluginsOutput, error) {
		regMu.RLock()
		defer regMu.RUnlock()
		out := make(map[string]types.Registration, len(registry))
		for k, v := range registry {
			out[k] = v.Registration
		}
		return &ListPluginsOutput{Body: out}, nil
	})
}

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
		broker.broadcast(sseMessage{Type: "device", PluginID: input.PluginID, DeviceID: input.Body.ID})
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
		broker.broadcast(sseMessage{Type: "device", PluginID: input.PluginID, DeviceID: input.DeviceID})
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
		broker.broadcast(sseMessage{Type: "device", PluginID: input.PluginID, DeviceID: input.DeviceID})
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

func registerEntityRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entities",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities",
		Summary:     "List entities",
		Description: "Returns all entities for a device, including virtual entities. Each entity includes an inline schema describing the domain's available commands and events.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *ListEntitiesInput) (*ListEntitiesOutput, error) {
		resp := routeRPC(input.PluginID, "entities/list", map[string]string{"device_id": input.DeviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			return nil, pluginErr(err.Error())
		}
		vstore.mu.RLock()
		for _, rec := range vstore.entities {
			if rec.OwnerPluginID == input.PluginID && rec.OwnerDeviceID == input.DeviceID {
				entities = append(entities, rec.Entity)
			}
		}
		vstore.mu.RUnlock()
		return &ListEntitiesOutput{Body: withSchema(entities)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "refresh-entities",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/refresh",
		Summary:     "Refresh device entities",
		Description: "Re-runs discovery for entities of a specific device.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *RefreshEntitiesInput) (*ListEntitiesOutput, error) {
		resp := routeRPC(input.PluginID, "entities/refresh", map[string]string{"device_id": input.DeviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			return nil, pluginErr(err.Error())
		}
		return &ListEntitiesOutput{Body: withSchema(entities)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-entity",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities",
		Summary:     "Create entity",
		Description: "Creates a new entity in the given device.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *CreateEntityInput) (*EntityOutput, error) {
		input.Body.DeviceID = input.DeviceID
		resp := routeRPC(input.PluginID, "entities/create", input.Body)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &EntityOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-entity",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities",
		Summary:     "Update entity",
		Description: "Updates entity properties. The entity.id field identifies which entity to update.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *UpdateEntityInput) (*EntityOutput, error) {
		input.Body.DeviceID = input.DeviceID
		resp := routeRPC(input.PluginID, "entities/update", input.Body)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &EntityOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "patch-entity-name",
		Method:      http.MethodPatch,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/name",
		Summary:     "Patch entity name",
		Description: "Updates only entity.local_name.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *PatchEntityNameInput) (*EntityOutput, error) {
		name := strings.TrimSpace(input.Body.LocalName)
		if name == "" {
			return nil, badReqErr("local_name is required")
		}
		payload := types.Entity{ID: input.EntityID, DeviceID: input.DeviceID, LocalName: name}
		resp := routeRPC(input.PluginID, "entities/update", payload)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		broker.broadcast(sseMessage{Type: "entity", PluginID: input.PluginID, DeviceID: input.DeviceID, EntityID: input.EntityID})
		return &EntityOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "patch-entity-labels",
		Method:      http.MethodPatch,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/labels",
		Summary:     "Patch entity labels",
		Description: "Updates only entity.labels. Provide map[string][]string.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *PatchEntityLabelsInput) (*EntityOutput, error) {
		if len(input.Body.Labels) == 0 {
			return nil, badReqErr("labels is required")
		}
		payload := types.Entity{ID: input.EntityID, DeviceID: input.DeviceID, Labels: input.Body.Labels}
		resp := routeRPC(input.PluginID, "entities/update", payload)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		broker.broadcast(sseMessage{Type: "entity", PluginID: input.PluginID, DeviceID: input.DeviceID, EntityID: input.EntityID})
		return &EntityOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-entity",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}",
		Summary:     "Delete entity",
		Description: "Deletes an entity from a device.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *DeleteEntityInput) (*DeleteOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "entities/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &DeleteOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-entity",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}",
		Summary:     "Get entity",
		Description: "Returns one entity by ID.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
		resp := routeRPC(input.PluginID, "entities/list", map[string]string{"device_id": input.DeviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			return nil, pluginErr(err.Error())
		}
		for _, e := range entities {
			if e.ID == input.EntityID {
				with := entityWithSchema{Entity: e}
				if desc, ok := types.GetDomainDescriptor(e.Domain); ok {
					filtered := filterDescriptor(desc, e.Actions)
					with.Schema = &filtered
				}
				return &GetEntityOutput{Body: with}, nil
			}
		}
		vstore.mu.RLock()
		defer vstore.mu.RUnlock()
		if rec, ok := vstore.entities[entityKey(input.PluginID, input.DeviceID, input.EntityID)]; ok {
			with := entityWithSchema{Entity: rec.Entity}
			if desc, ok := types.GetDomainDescriptor(rec.Entity.Domain); ok {
				filtered := filterDescriptor(desc, rec.Entity.Actions)
				with.Schema = &filtered
			}
			return &GetEntityOutput{Body: with}, nil
		}
		return nil, notFoundErr("entity not found")
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-entity-valid-events",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/events",
		Summary:     "Get valid events for entity",
		Description: "Returns the canonical valid event types and required fields for this entity based on its domain schema and action capabilities.",
		Tags:        []string{"entities", "schema"},
	}, func(ctx context.Context, input *GetEntityEventsInput) (*EntityEventsOutput, error) {
		resp := routeRPC(input.PluginID, "entities/list", map[string]string{"device_id": input.DeviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			return nil, pluginErr(err.Error())
		}

		var target *types.Entity
		for i := range entities {
			if entities[i].ID == input.EntityID {
				target = &entities[i]
				break
			}
		}
		if target == nil {
			vstore.mu.RLock()
			if rec, ok := vstore.entities[entityKey(input.PluginID, input.DeviceID, input.EntityID)]; ok {
				copyEnt := rec.Entity
				target = &copyEnt
			}
			vstore.mu.RUnlock()
		}
		if target == nil {
			return nil, notFoundErr("entity not found")
		}

		desc, ok := types.GetDomainDescriptor(target.Domain)
		if !ok {
			return nil, notFoundErr("domain schema not found for entity")
		}
		filtered := filterDescriptor(desc, target.Actions)
		return &EntityEventsOutput{Body: entityEventsDescriptor{
			PluginID:    input.PluginID,
			DeviceID:    input.DeviceID,
			EntityID:    input.EntityID,
			Domain:      target.Domain,
			ValidEvents: filtered.Events,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-virtual-entity",
		Method:        http.MethodPost,
		Path:          "/api/plugins/{plugin_id}/devices/{device_id}/entities/virtual",
		Summary:       "Create virtual entity",
		Description:   "Creates a virtual entity that mirrors another entity from any plugin. Commands are forwarded to the source entity and state stays in sync via event subscription.",
		Tags:          []string{"entities"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *CreateVirtualEntityInput) (*VirtualEntityOutput, error) {
		req := input.Body
		if req.ID == "" || req.SourcePluginID == "" || req.SourceDeviceID == "" || req.SourceEntityID == "" {
			return nil, badReqErr("id, source_plugin_id, source_device_id, source_entity_id are required")
		}
		key := entityKey(input.PluginID, input.DeviceID, req.ID)
		if _, err := findEntity(input.PluginID, input.DeviceID, req.ID); err == nil {
			return nil, conflictErr("entity id already exists in plugin")
		}
		vstore.mu.RLock()
		_, exists := vstore.entities[key]
		vstore.mu.RUnlock()
		if exists {
			return nil, conflictErr("virtual entity id already exists")
		}
		source, err := findEntity(req.SourcePluginID, req.SourceDeviceID, req.SourceEntityID)
		if err != nil {
			return nil, pluginErr("source entity not found")
		}
		mirror := true
		if req.MirrorSource != nil {
			mirror = *req.MirrorSource
		}
		actions := req.Actions
		if len(actions) == 0 {
			actions = append([]string(nil), source.Actions...)
		}
		localName := req.LocalName
		if localName == "" {
			localName = source.LocalName
		}
		ent := types.Entity{
			ID: req.ID, DeviceID: input.DeviceID, Domain: source.Domain,
			LocalName: localName, Actions: actions, Data: source.Data,
		}
		ent.Data.SyncStatus = types.SyncStatusSynced
		ent.Data.UpdatedAt = time.Now().UTC()
		rec := virtualEntityRecord{
			OwnerPluginID: input.PluginID, OwnerDeviceID: input.DeviceID,
			SourcePluginID: req.SourcePluginID, SourceDeviceID: req.SourceDeviceID,
			SourceEntityID: req.SourceEntityID, MirrorSource: mirror, Entity: ent,
		}
		vstore.mu.Lock()
		vstore.entities[key] = rec
		vstore.persistLocked()
		vstore.mu.Unlock()
		return &VirtualEntityOutput{Body: ent}, nil
	})
}

func registerScriptRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-script",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Get script",
		Description: "Returns the automation script source for an entity.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *GetScriptInput) (*ScriptOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/get", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-script",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Set script",
		Description: "Installs or replaces an automation script on an entity.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *SetScriptInput) (*ScriptOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "source": input.Body.Source}
		resp := routeRPC(input.PluginID, "scripts/put", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-script",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script",
		Summary:     "Delete script",
		Description: "Removes the automation script from an entity. Use ?purge_state=true to also delete persisted script state.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *DeleteScriptInput) (*ScriptOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "purge_state": input.PurgeState}
		resp := routeRPC(input.PluginID, "scripts/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-script-state",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Get script state",
		Description: "Returns the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *GetScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/state/get", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-script-state",
		Method:      http.MethodPut,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Set script state",
		Description: "Replaces the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *SetScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]any{"device_id": input.DeviceID, "entity_id": input.EntityID, "state": input.Body.State}
		resp := routeRPC(input.PluginID, "scripts/state/put", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-script-state",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/script/state",
		Summary:     "Delete script state",
		Description: "Clears the persisted key-value state store for an entity's script.",
		Tags:        []string{"scripts"},
	}, func(ctx context.Context, input *DeleteScriptStateInput) (*ScriptStateOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "scripts/state/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ScriptStateOutput{Body: resp.Result}, nil
	})
}

func registerSnapshotRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "save-snapshot",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/snapshots",
		Summary:     "Save entity snapshot",
		Description: "Captures the current effective state of an entity as a named snapshot. Returns the snapshot with its assigned UUID.",
		Tags:        []string{"snapshots"},
	}, func(ctx context.Context, input *SaveSnapshotInput) (*SnapshotOutput, error) {
		params := map[string]any{
			"device_id": input.DeviceID,
			"entity_id": input.EntityID,
			"name":      input.Body.Name,
			"labels":    input.Body.Labels,
		}
		resp := routeRPC(input.PluginID, "entities/snapshots/save", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &SnapshotOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-snapshots",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/snapshots",
		Summary:     "List entity snapshots",
		Description: "Returns all snapshots for an entity, ordered by creation time.",
		Tags:        []string{"snapshots"},
	}, func(ctx context.Context, input *GetEntityInput) (*ListSnapshotsOutput, error) {
		params := map[string]string{"device_id": input.DeviceID, "entity_id": input.EntityID}
		resp := routeRPC(input.PluginID, "entities/snapshots/list", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &ListSnapshotsOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-snapshot",
		Method:      http.MethodDelete,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/snapshots/{snapshot_id}",
		Summary:     "Delete entity snapshot",
		Description: "Removes a snapshot by ID from an entity.",
		Tags:        []string{"snapshots"},
	}, func(ctx context.Context, input *DeleteSnapshotInput) (*SnapshotOutput, error) {
		params := map[string]string{
			"device_id":   input.DeviceID,
			"entity_id":   input.EntityID,
			"snapshot_id": input.SnapshotID,
		}
		resp := routeRPC(input.PluginID, "entities/snapshots/delete", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &SnapshotOutput{Body: resp.Result}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "restore-snapshot",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/snapshots/{snapshot_id}/restore",
		Summary:     "Restore entity snapshot",
		Description: "Replays the saved state as commands through the normal command pipeline, returning the entity to the snapshot state.",
		Tags:        []string{"snapshots"},
	}, func(ctx context.Context, input *RestoreSnapshotInput) (*SnapshotOutput, error) {
		params := map[string]string{
			"device_id":   input.DeviceID,
			"entity_id":   input.EntityID,
			"snapshot_id": input.SnapshotID,
		}
		resp := routeRPC(input.PluginID, "entities/snapshots/restore", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		return &SnapshotOutput{Body: resp.Result}, nil
	})
}

func registerCommandRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "send-command",
		Method:        http.MethodPost,
		Path:          "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/commands",
		Summary:       "Send command",
		Description:   "Sends a domain-specific command to an entity. For virtual entities, forwards to the source. Returns CommandStatus with state=pending; poll get-command-status for completion.",
		Tags:          []string{"commands"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, input *SendCommandInput) (*CommandStatusOutput, error) {
		pluginID, deviceID, entityID := input.PluginID, input.DeviceID, input.EntityID
		payloadBytes, _ := json.Marshal(input.Body)
		payload := json.RawMessage(payloadBytes)

		key := entityKey(pluginID, deviceID, entityID)
		vstore.mu.RLock()
		vrec, isVirtual := vstore.entities[key]
		vstore.mu.RUnlock()

		if isVirtual {
			actionType, err := parseActionType(payload)
			if err != nil {
				return nil, badReqErr(err.Error())
			}
			if len(vrec.Entity.Actions) > 0 && !containsAction(vrec.Entity.Actions, actionType) {
				return nil, pluginErr(fmt.Sprintf("action %q not supported by this virtual entity", actionType))
			}
			params := map[string]any{"device_id": vrec.SourceDeviceID, "entity_id": vrec.SourceEntityID, "payload": payload}
			sourceResp := routeRPC(vrec.SourcePluginID, "entities/commands/create", params)
			if sourceResp.Error != nil {
				return nil, pluginErr(sourceResp.Error.Message)
			}
			var sourceStatus types.CommandStatus
			if err := json.Unmarshal(sourceResp.Result, &sourceStatus); err != nil {
				return nil, pluginErr("invalid source command status")
			}
			now := time.Now().UTC()
			virtualCID := nextID("vcmd")
			status := types.CommandStatus{
				CommandID: virtualCID, PluginID: pluginID, DeviceID: deviceID, EntityID: entityID,
				EntityType: vrec.Entity.Domain, State: types.CommandPending, CreatedAt: now, LastUpdatedAt: now,
			}
			vstore.mu.Lock()
			vstore.commands[virtualCID] = virtualCommandRecord{
				OwnerPluginID: pluginID, SourcePluginID: vrec.SourcePluginID,
				SourceCommand: sourceStatus.CommandID, VirtualKey: key, Status: status,
			}
			vrec.Entity.Data.LastCommandID = virtualCID
			vrec.Entity.Data.SyncStatus = types.SyncStatusPending
			vrec.Entity.Data.UpdatedAt = now
			vstore.entities[key] = vrec
			vstore.persistLocked()
			vstore.mu.Unlock()
			go monitorVirtualCommand(virtualCID)
			return &CommandStatusOutput{Body: status}, nil
		}

		params := map[string]any{"device_id": deviceID, "entity_id": entityID, "payload": payload}
		resp := routeRPC(pluginID, "entities/commands/create", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var status types.CommandStatus
		json.Unmarshal(resp.Result, &status)
		return &CommandStatusOutput{Body: status}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-command-status",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/commands/{command_id}",
		Summary:     "Get command status",
		Description: "Polls the status of a previously issued command. State transitions: pending → succeeded | failed.",
		Tags:        []string{"commands"},
	}, func(ctx context.Context, input *GetCommandStatusInput) (*CommandStatusOutput, error) {
		vstore.mu.RLock()
		rec, isVirtual := vstore.commands[input.CommandID]
		vstore.mu.RUnlock()
		if isVirtual {
			if rec.OwnerPluginID != input.PluginID {
				return nil, pluginErr("command not owned by plugin")
			}
			return &CommandStatusOutput{Body: rec.Status}, nil
		}
		resp := routeRPC(input.PluginID, "commands/status/get", map[string]string{"command_id": input.CommandID})
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var status types.CommandStatus
		json.Unmarshal(resp.Result, &status)
		return &CommandStatusOutput{Body: status}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-any-command-status",
		Method:      http.MethodGet,
		Path:        "/api/commands/{command_id}",
		Summary:     "Get command status by ID",
		Description: "Returns the status of any command by ID, regardless of which plugin owns it. Covers commands issued directly between plugins via Ctx:SendCommand.",
		Tags:        []string{"commands"},
	}, func(ctx context.Context, input *GetAnyCommandStatusInput) (*CommandStatusOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		status, found, err := history.LatestCommandStatus(input.CommandID)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query command history")
		}
		if !found {
			return nil, notFoundErr("command not found")
		}
		return &CommandStatusOutput{Body: status}, nil
	})
}

func registerEventRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "ingest-event",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/events",
		Summary:     "Ingest entity event",
		Description: "Reports a state-change event from a device/entity. Updates entity state (reported, effective). Pass X-Correlation-ID to link this event to a prior command, which marks that command succeeded.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *IngestEventInput) (*IngestEventOutput, error) {
		pluginID, deviceID, entityID := input.PluginID, input.DeviceID, input.EntityID
		payloadBytes, _ := json.Marshal(input.Body)
		payload := json.RawMessage(payloadBytes)
		correlationID := input.CorrelationID

		key := entityKey(pluginID, deviceID, entityID)
		vstore.mu.RLock()
		vrec, isVirtual := vstore.entities[key]
		vstore.mu.RUnlock()

		if isVirtual {
			vstore.mu.Lock()
			vrec.Entity.Data.Reported = payload
			vrec.Entity.Data.Effective = payload
			vrec.Entity.Data.SyncStatus = types.SyncStatusSynced
			vrec.Entity.Data.LastEventID = nextID("vevt")
			if correlationID != "" {
				vrec.Entity.Data.LastCommandID = correlationID
				if cmdRec, ok := vstore.commands[correlationID]; ok {
					cmdRec.Status.State = types.CommandSucceeded
					cmdRec.Status.LastUpdatedAt = time.Now().UTC()
					vstore.commands[correlationID] = cmdRec
				}
			}
			vrec.Entity.Data.UpdatedAt = time.Now().UTC()
			vstore.entities[key] = vrec
			vstore.persistLocked()
			out := vrec.Entity
			vstore.mu.Unlock()
			return &IngestEventOutput{Body: out}, nil
		}

		params := map[string]any{"device_id": deviceID, "entity_id": entityID, "payload": payload, "correlation_id": correlationID}
		resp := routeRPC(pluginID, "entities/events/ingest", params)
		if resp.Error != nil {
			return nil, pluginErr(resp.Error.Message)
		}
		var ent types.Entity
		json.Unmarshal(resp.Result, &ent)
		return &IngestEventOutput{Body: ent}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-event-journal",
		Method:      http.MethodGet,
		Path:        "/api/journal/events",
		Summary:     "Event journal",
		Description: "Returns a filtered log of recent entity state-change events observed by the gateway.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *ListJournalEventsInput) (*ListJournalEventsOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		events, err := history.ListEvents(input.PluginID, input.DeviceID, input.EntityID, limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query event history")
		}
		return &ListJournalEventsOutput{Body: events}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-plugin-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/plugin-rates",
		Summary:     "Per-plugin activity rates",
		Description: "Returns per-plugin event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *PluginRatesInput) (*PluginRatesOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := history.PluginRates(window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query plugin rates")
		}
		return &PluginRatesOutput{Body: rates}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-device-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/device-rates",
		Summary:     "Per-device activity rates",
		Description: "Returns per-device event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *DeviceRatesInput) (*DeviceRatesOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := history.DeviceRates(input.PluginID, window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query device rates")
		}
		return &DeviceRatesOutput{Body: rates}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-entity-rates",
		Method:      http.MethodGet,
		Path:        "/api/history/entity-rates",
		Summary:     "Per-entity activity rates",
		Description: "Returns per-entity event and command rates over a recent time window.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *EntityRatesInput) (*EntityRatesOutput, error) {
		if history == nil {
			return nil, huma.Error500InternalServerError("History store not available")
		}
		window := input.Window
		if window <= 0 {
			window = 30
		}
		rates, err := history.EntityRates(input.PluginID, input.DeviceID, window)
		if err != nil {
			return nil, huma.Error500InternalServerError("Failed to query entity rates")
		}
		return &EntityRatesOutput{Body: rates}, nil
	})
}

type entityWithPlugin struct {
	types.Entity
	PluginID string `json:"plugin_id"`
}

func performEntitySearch(query types.SearchQuery) []entityWithPlugin {
	data, _ := json.Marshal(query)
	results := make([]entityWithPlugin, 0)

	regMu.RLock()
	expected := make(map[string]bool)
	for id, rec := range registry {
		if rec.Valid {
			expected[id] = true
		}
	}
	regMu.RUnlock()

	sub, _ := nc.SubscribeSync(nats.NewInbox())
	nc.PublishRequest(runner.SubjectSearchEntities, sub.Subject, data)

	timeout := time.After(300 * time.Millisecond)
gatherLoop:
	for len(expected) > 0 {
		select {
		case <-timeout:
			break gatherLoop
		default:
			msg, err := sub.NextMsg(10 * time.Millisecond)
			if err != nil {
				if errors.Is(err, nats.ErrTimeout) {
					continue
				}
				break gatherLoop
			}
			var res types.SearchEntitiesResponse
			if err := json.Unmarshal(msg.Data, &res); err == nil {
				for _, ent := range res.Matches {
					results = append(results, entityWithPlugin{
						Entity:   ent,
						PluginID: res.PluginID,
					})
				}
				delete(expected, res.PluginID)
			}
		}
	}
	_ = sub.Unsubscribe()

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}
	return results
}

func startNATSDiscoveryBridge() {
		log.Printf("gateway: starting NATS discovery bridge on subject %s", runner.SubjectGatewayDiscovery)
		_, err := nc.Subscribe(runner.SubjectGatewayDiscovery, func(m *nats.Msg) {
			queryStr := string(m.Data)
			log.Printf("gateway: received discovery request: %s", queryStr)
			// Extract query params from string like "?label=Room:Kitchen"
			// We can reuse the http logic by creating a dummy request
			u, err := url.Parse("http://localhost/api/search/entities" + queryStr)
			if err != nil {
				log.Printf("gateway: failed to parse discovery query %q: %v", queryStr, err)
				return
			}
	
			q := u.Query()
			pattern := q.Get("q")
			if pattern == "" {
				pattern = q.Get("pattern")
			}
			if pattern == "" {
				pattern = "*"
			}
	
			limit, _ := strconv.Atoi(q.Get("limit"))
	
			query := types.SearchQuery{
				Pattern:  pattern,
				Labels:   parseLabels(q["label"]),
				PluginID: strings.TrimSpace(q.Get("plugin_id")),
				DeviceID: strings.TrimSpace(q.Get("device_id")),
				EntityID: strings.TrimSpace(q.Get("entity_id")),
				Domain:   strings.TrimSpace(q.Get("domain")),
				Limit:    limit,
			}
	
			results := performEntitySearch(query)
			log.Printf("gateway: discovery query %q found %d matches", queryStr, len(results))
			resp, _ := json.Marshal(results)
			m.Respond(resp)
		})
		if err != nil {
			log.Printf("gateway: failed to subscribe to discovery subject: %v", err)
		}
	}
	
	func registerSearchRoutes(api huma.API) {
		huma.Register(api, huma.Operation{
			OperationID: "search-plugins",
			Method:      http.MethodGet,
			Path:        "/api/search/plugins",
			Summary:     "Search plugins",
			Description: "Broadcasts a search over NATS and collects plugin manifests from all responding plugins.",
			Tags:        []string{"search"},
		}, searchPluginsHandler)
	
		huma.Register(api, huma.Operation{
			OperationID: "search-devices",
			Method:      http.MethodGet,
			Path:        "/api/search/devices",
			Summary:     "Search devices",
			Description: "Broadcasts a device search over NATS and collects results from all plugins.",
			Tags:        []string{"search"},
		}, func(ctx context.Context, input *SearchDevicesInput) (*SearchDevicesOutput, error) {
			pattern := input.Pattern
			if pattern == "" {
				pattern = "*"
			}
			var rawLabels []string
			if iface, ok := ctx.(interface{ Gin() *gin.Context }); ok {
				rawLabels = iface.Gin().QueryArray("label")
			}
			if len(rawLabels) == 0 {
				rawLabels = input.Labels
			}
	
			query := types.SearchQuery{
				Pattern:  pattern,
				Labels:   parseLabels(rawLabels),
				PluginID: strings.TrimSpace(input.PluginID),
				DeviceID: strings.TrimSpace(input.DeviceID),
				Limit:    input.Limit,
			}
			data, _ := json.Marshal(query)
			results := make([]types.Device, 0)
	
			regMu.RLock()
			expected := make(map[string]bool)
			for id, rec := range registry {
				if rec.Valid {
					expected[id] = true
				}
			}
			regMu.RUnlock()
	
			sub, _ := nc.SubscribeSync(nats.NewInbox())
			nc.PublishRequest(runner.SubjectSearchDevices, sub.Subject, data)
	
			timeout := time.After(300 * time.Millisecond)
		gatherLoop:
			for len(expected) > 0 {
				select {
				case <-timeout:
					break gatherLoop
				default:
					msg, err := sub.NextMsg(10 * time.Millisecond)
					if err != nil {
						if errors.Is(err, nats.ErrTimeout) {
							continue
						}
						break gatherLoop
					}
					var res types.SearchDevicesResponse
					if err := json.Unmarshal(msg.Data, &res); err == nil {
						results = append(results, res.Matches...)
						delete(expected, res.PluginID)
					}
				}
			}
			_ = sub.Unsubscribe()
	
			if query.Limit > 0 && len(results) > query.Limit {
				results = results[:query.Limit]
			}
			return &SearchDevicesOutput{Body: results}, nil
		})
	
		huma.Register(api, huma.Operation{
			OperationID: "search-entities",
			Method:      http.MethodGet,
			Path:        "/api/search/entities",
			Summary:     "Search entities",
			Description: "Broadcasts an entity search over NATS and collects results from all plugins.",
			Tags:        []string{"search"},
		}, func(ctx context.Context, input *SearchEntitiesInput) (*SearchEntitiesOutput, error) {
			pattern := input.Pattern
			if pattern == "" {
				pattern = "*"
			}
			var rawLabels []string
			if iface, ok := ctx.(interface{ Gin() *gin.Context }); ok {
				rawLabels = iface.Gin().QueryArray("label")
			}
			if len(rawLabels) == 0 {
				rawLabels = input.Labels
			}
	
			query := types.SearchQuery{
				Pattern:  pattern,
				Labels:   parseLabels(rawLabels),
				PluginID: strings.TrimSpace(input.PluginID),
				DeviceID: strings.TrimSpace(input.DeviceID),
				EntityID: strings.TrimSpace(input.EntityID),
				Domain:   strings.TrimSpace(input.Domain),
				Limit:    input.Limit,
			}
			results := performEntitySearch(query)
			return &SearchEntitiesOutput{Body: results}, nil
		})
	}
	
	func searchPluginsHandler(ctx context.Context, input *SearchPluginsInput) (*SearchPluginsOutput, error) {
		pattern := input.Pattern
		if pattern == "" {
			pattern = "*"
		}
		query := types.SearchQuery{Pattern: pattern}
		data, _ := json.Marshal(query)
		results := make([]types.Manifest, 0)
	
		regMu.RLock()
		expected := make(map[string]bool)
		for id, rec := range registry {
			if rec.Valid {
				expected[id] = true
			}
		}
		regMu.RUnlock()
	
		sub, _ := nc.SubscribeSync(nats.NewInbox())
		nc.PublishRequest(runner.SubjectSearchPlugins, sub.Subject, data)
	
		timeout := time.After(300 * time.Millisecond)
	gatherLoop:
		for len(expected) > 0 {
			select {
			case <-timeout:
				break gatherLoop
			default:
				msg, err := sub.NextMsg(10 * time.Millisecond)
				if err != nil {
					if errors.Is(err, nats.ErrTimeout) {
						continue
					}
					break gatherLoop
				}
				var res types.SearchPluginsResponse
				if err := json.Unmarshal(msg.Data, &res); err == nil {
					results = append(results, res.Matches...)
					delete(expected, res.PluginID)
				}
			}
		}
		_ = sub.Unsubscribe()

		return &SearchPluginsOutput{Body: results}, nil
	}
	
	func registerSchemaRoutes(api huma.API) {
	
	huma.Register(api, huma.Operation{
		OperationID: "list-domains",
		Method:      http.MethodGet,
		Path:        "/api/schema/domains",
		Summary:     "List domain descriptors",
		Description: "Returns schema descriptors for all known entity domains. Each descriptor lists available commands and events with their field definitions.",
		Tags:        []string{"schema"},
	}, func(ctx context.Context, input *struct{}) (*DomainListOutput, error) {
		return &DomainListOutput{Body: types.AllDomainDescriptors()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-domain",
		Method:      http.MethodGet,
		Path:        "/api/schema/domains/{domain}",
		Summary:     "Get domain descriptor",
		Description: "Returns the schema descriptor for a specific entity domain (e.g. light, switch, sensor).",
		Tags:        []string{"schema"},
	}, func(ctx context.Context, input *GetDomainInput) (*DomainOutput, error) {
		desc, ok := types.GetDomainDescriptor(input.Domain)
		if !ok {
			return nil, notFoundErr("unknown domain")
		}
		return &DomainOutput{Body: desc}, nil
	})
}

// ---------------------------------------------------------------------------
// Helpers (unchanged logic from original routes.go)
// ---------------------------------------------------------------------------

func withSchema(entities []types.Entity) []entityWithSchema {
	out := make([]entityWithSchema, len(entities))
	for i, e := range entities {
		r := entityWithSchema{Entity: e}
		if desc, ok := types.GetDomainDescriptor(e.Domain); ok {
			filtered := filterDescriptor(desc, e.Actions)
			r.Schema = &filtered
		}
		out[i] = r
	}
	return out
}

func filterDescriptor(desc types.DomainDescriptor, actions []string) types.DomainDescriptor {
	if len(actions) == 0 {
		return desc
	}
	allowed := make(map[string]bool, len(actions))
	for _, a := range actions {
		allowed[a] = true
	}
	filtered := types.DomainDescriptor{Domain: desc.Domain}
	for _, cmd := range desc.Commands {
		if allowed[cmd.Action] {
			filtered.Commands = append(filtered.Commands, cmd)
		}
	}
	for _, evt := range desc.Events {
		if allowed[evt.Action] {
			filtered.Events = append(filtered.Events, evt)
		}
	}
	return filtered
}

func parseLabels(pairs []string) map[string][]string {
	if len(pairs) == 0 {
		return nil
	}
	labels := make(map[string][]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			labels[k] = append(labels[k], v)
		}
	}
	return labels
}
