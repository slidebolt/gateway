package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

func pluginErr(msg string) error       { return &apiError{status: http.StatusForbidden, Message: msg} }
func badReqErr(msg string) error       { return &apiError{status: http.StatusBadRequest, Message: msg} }
func notFoundErr(msg string) error     { return &apiError{status: http.StatusNotFound, Message: msg} }
func conflictErr(msg string) error     { return &apiError{status: http.StatusConflict, Message: msg} }

// ---------------------------------------------------------------------------
// Input / output types — one per route, named clearly for OpenAPI schema gen.
// ---------------------------------------------------------------------------

// --- System ---

type HealthInput struct {
	PluginID string `query:"id" doc:"Plugin ID for plugin-specific health check (optional)"`
}
type HealthOutput struct{ Body map[string]any }

type RuntimeOutput struct{ Body gatewayRuntimeInfo }

type ListPluginsOutput struct{ Body map[string]types.Registration }

// --- Devices ---

type ListDevicesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
}
type ListDevicesOutput struct{ Body []types.Device }

type CreateDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	Body     types.Device
}
type DeviceOutput struct{ Body json.RawMessage }

type UpdateDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	Body     types.Device
}

type DeleteDeviceInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}
type DeleteOutput struct{ Body json.RawMessage }

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

type DeleteEntityInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
	EntityID string `path:"entity_id" doc:"Entity ID"`
}

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
}
type ListJournalEventsOutput struct{ Body []observedEvent }

// --- Search ---

type SearchPluginsInput struct {
	Pattern string `query:"q" doc:"Glob-style search pattern (default: *)"`
}
type SearchPluginsOutput struct{ Body []types.Manifest }

type SearchDevicesInput struct {
	Pattern string   `query:"q" doc:"Glob-style search pattern (default: *)"`
	Labels  []string `query:"label" doc:"Label filters in key:value format. Multiple values use AND logic (e.g. room:kitchen)."`
}
type SearchDevicesOutput struct{ Body []types.Device }

type SearchEntitiesInput struct {
	Labels []string `query:"label" doc:"Label filters in key:value format. Multiple values use AND logic."`
}
type SearchEntitiesOutput struct{ Body []types.Entity }

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
		reg, ok := registry[input.PluginID]
		regMu.RUnlock()
		if !ok {
			return nil, pluginErr("plugin not found")
		}
		resp := routeRPC(reg.Manifest.ID, runner.HealthEndpoint, nil)
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
			out[k] = v
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
		ent.Data.SyncStatus = "in_sync"
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
			vrec.Entity.Data.SyncStatus = "pending"
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
			vrec.Entity.Data.SyncStatus = "in_sync"
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
			vstore.appendEventLocked(observedEvent{
				Name: classifyEventName(vrec.Entity.Domain, payload, true),
				PluginID: pluginID, DeviceID: deviceID, EntityID: entityID,
				EventID: vrec.Entity.Data.LastEventID, CreatedAt: time.Now().UTC(),
			})
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
		vstore.mu.RLock()
		defer vstore.mu.RUnlock()
		out := make([]observedEvent, 0)
		for _, evt := range vstore.events {
			if input.PluginID != "" && evt.PluginID != input.PluginID {
				continue
			}
			if input.DeviceID != "" && evt.DeviceID != input.DeviceID {
				continue
			}
			if input.EntityID != "" && evt.EntityID != input.EntityID {
				continue
			}
			out = append(out, evt)
		}
		return &ListJournalEventsOutput{Body: out}, nil
	})
}

func registerSearchRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "search-plugins",
		Method:      http.MethodGet,
		Path:        "/api/search/plugins",
		Summary:     "Search plugins",
		Description: "Broadcasts a search over NATS and collects plugin manifests from all responding plugins (500ms window).",
		Tags:        []string{"search"},
	}, func(ctx context.Context, input *SearchPluginsInput) (*SearchPluginsOutput, error) {
		pattern := input.Pattern
		if pattern == "" {
			pattern = "*"
		}
		query := types.SearchQuery{Pattern: pattern}
		data, _ := json.Marshal(query)
		results := make([]types.Manifest, 0)
		sub, _ := nc.SubscribeSync(nats.NewInbox())
		nc.PublishRequest(runner.SubjectSearchPlugins, sub.Subject, data)
		start := time.Now()
		for time.Since(start) < 500*time.Millisecond {
			msg, err := sub.NextMsg(100 * time.Millisecond)
			if err != nil {
				break
			}
			var m types.Manifest
			json.Unmarshal(msg.Data, &m)
			results = append(results, m)
		}
		return &SearchPluginsOutput{Body: results}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "search-devices",
		Method:      http.MethodGet,
		Path:        "/api/search/devices",
		Summary:     "Search devices",
		Description: "Broadcasts a device search over NATS and collects results from all plugins (500ms window).",
		Tags:        []string{"search"},
	}, func(ctx context.Context, input *SearchDevicesInput) (*SearchDevicesOutput, error) {
		pattern := input.Pattern
		if pattern == "" {
			pattern = "*"
		}
		// Huma input.Labels might not capture all repeated parameters in all configurations.
		// Pulling directly from the context via the underlying Gin request to be sure.
		var rawLabels []string
		if iface, ok := ctx.(interface{ Gin() *gin.Context }); ok {
			rawLabels = iface.Gin().QueryArray("label")
		}
		if len(rawLabels) == 0 {
			rawLabels = input.Labels
		}

		query := types.SearchQuery{Pattern: pattern, Labels: parseLabels(rawLabels)}
		data, _ := json.Marshal(query)
		results := make([]types.Device, 0)
		sub, _ := nc.SubscribeSync(nats.NewInbox())
		nc.PublishRequest(runner.SubjectSearchDevices, sub.Subject, data)
		start := time.Now()
		for time.Since(start) < 500*time.Millisecond {
			msg, err := sub.NextMsg(100 * time.Millisecond)
			if err != nil {
				break
			}
			var d []types.Device
			json.Unmarshal(msg.Data, &d)
			results = append(results, d...)
		}
		return &SearchDevicesOutput{Body: results}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "search-entities",
		Method:      http.MethodGet,
		Path:        "/api/search/entities",
		Summary:     "Search entities",
		Description: "Broadcasts an entity search over NATS and collects results from all plugins (500ms window).",
		Tags:        []string{"search"},
	}, func(ctx context.Context, input *SearchEntitiesInput) (*SearchEntitiesOutput, error) {
		var rawLabels []string
		if iface, ok := ctx.(interface{ Gin() *gin.Context }); ok {
			rawLabels = iface.Gin().QueryArray("label")
		}
		if len(rawLabels) == 0 {
			rawLabels = input.Labels
		}

		query := types.SearchQuery{Pattern: "*", Labels: parseLabels(rawLabels)}
		data, _ := json.Marshal(query)
		results := make([]types.Entity, 0)
		sub, _ := nc.SubscribeSync(nats.NewInbox())
		nc.PublishRequest(runner.SubjectSearchEntities, sub.Subject, data)
		start := time.Now()
		for time.Since(start) < 500*time.Millisecond {
			msg, err := sub.NextMsg(100 * time.Millisecond)
			if err != nil {
				break
			}
			var e []types.Entity
			json.Unmarshal(msg.Data, &e)
			results = append(results, e...)
		}
		return &SearchEntitiesOutput{Body: results}, nil
	})
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

func parseLabels(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	labels := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			labels[k] = v
		}
	}
	return labels
}
