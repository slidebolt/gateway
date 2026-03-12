package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// --- Snapshot types ---

type CreateSnapshotInput struct {
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

func registerSnapshotRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "save-snapshot",
		Method:      http.MethodPost,
		Path:        "/api/plugins/{plugin_id}/devices/{device_id}/entities/{entity_id}/snapshots",
		Summary:     "Save entity snapshot",
		Description: "Captures the current effective state of an entity as a named snapshot. Returns the snapshot with its assigned UUID.",
		Tags:        []string{"snapshots"},
	}, func(ctx context.Context, input *CreateSnapshotInput) (*SnapshotOutput, error) {
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
