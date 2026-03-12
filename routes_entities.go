package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- Entity types ---

type ListEntitiesInput struct {
	PluginID string `path:"plugin_id" doc:"Plugin ID"`
	DeviceID string `path:"device_id" doc:"Device ID"`
}
type ListEntitiesOutput struct{ Body []EntityResponse }

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
type GetEntityOutput struct{ Body EntityResponse }

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

func registerEntityRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entities",
		Method:      http.MethodGet,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities",
		Summary:     "List entities",
		Description: "Returns all entities for a device. Each entity includes an inline schema describing the domain's available commands and events.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, input *ListEntitiesInput) (*ListEntitiesOutput, error) {
		resp := routeRPC(input.PluginID, "entities/list", map[string]string{"device_id": input.DeviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			return nil, pluginErr(err.Error())
		}
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
		saveEntityToRegistry(input.DeviceID, resp.Result, input.Body)
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
		saveEntityToRegistry(input.DeviceID, resp.Result, input.Body)
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
		saveEntityToRegistry(input.DeviceID, resp.Result, payload)
		historyService.BroadcastEntity(input.PluginID, input.DeviceID, input.EntityID)
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
		saveEntityToRegistry(input.DeviceID, resp.Result, payload)
		historyService.BroadcastEntity(input.PluginID, input.DeviceID, input.EntityID)
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
		_ = registryService.DeleteEntity(input.DeviceID, input.EntityID)
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
				return &GetEntityOutput{Body: toEntityResponse(e)}, nil
			}
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
}
