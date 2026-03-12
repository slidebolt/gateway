package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- System types ---

type HealthInput struct {
	PluginID string `query:"id" doc:"Plugin ID for plugin-specific health check (optional)"`
}
type HealthOutput struct{ Body map[string]any }

type RuntimeOutput struct{ Body gatewayRuntimeInfo }

type BackupInput struct{}
type PruneInput struct{}

type PruneOutput struct {
	Body struct {
		Message string `json:"message"`
	}
}

type ListPluginsOutput struct{ Body map[string]types.Registration }

func registerSystemRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health-check",
		Method:      http.MethodGet,
		Path:        types.RPCMethodHealthCheck,
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
		resp := routeRPC(record.Registration.Manifest.ID, types.RPCMethodHealthCheck, nil)
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
			out[k] = v.Registration
		}
		return &ListPluginsOutput{Body: out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "system-backup",
		Method:      http.MethodGet,
		Path:        "/api/system/backup",
		Summary:     "System Backup (tar.gz)",
		Description: "Streams a tar.gz archive of the system configuration and state. Excludes logs, history database, and NATS binary storage.",
		Tags:        []string{"system"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Backup archive",
				Content: map[string]*huma.MediaType{
					"application/gzip": {
						Schema: &huma.Schema{
							Type:   "string",
							Format: "binary",
						},
					},
				},
			},
		},
	}, func(ctx context.Context, input *BackupInput) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				gwDataDir := gatewayDataDir
				rootDataDir := filepath.Dir(gwDataDir)

				ctx.SetHeader("Content-Type", "application/gzip")
				ctx.SetHeader("Content-Disposition", "attachment; filename=\"slidebolt-backup-"+time.Now().Format("20060102-150405")+".tar.gz\"")

				gzw := gzip.NewWriter(ctx.BodyWriter())
				defer gzw.Close()
				tw := tar.NewWriter(gzw)
				defer tw.Close()

				_ = filepath.WalkDir(rootDataDir, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					rel, err := filepath.Rel(rootDataDir, path)
					if err != nil {
						return err
					}
					if rel == "." {
						return nil
					}

					if d.IsDir() {
						if d.Name() == "nats" {
							return filepath.SkipDir
						}
						return nil
					}

					ext := strings.ToLower(filepath.Ext(path))
					if ext != ".json" && ext != ".lua" {
						return nil
					}
					if d.Name() == "runtime.json" {
						return nil
					}

					info, err := d.Info()
					if err != nil {
						return nil
					}
					header, err := tar.FileInfoHeader(info, "")
					if err != nil {
						return err
					}
					header.Name = rel

					if err := tw.WriteHeader(header); err != nil {
						return err
					}
					f, err := diskIO.OpenRead(path)
					if err != nil {
						return nil
					}
					defer f.Close()
					_, err = io.Copy(tw, f)
					return err
				})
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "system-prune",
		Method:      http.MethodPost,
		Path:        "/api/system/prune",
		Summary:     "System Prune (Clean Everything)",
		Description: "Wipes the event/command history database, clears NATS JetStream messages, and truncates all service log files.",
		Tags:        []string{"system"},
	}, func(ctx context.Context, input *PruneInput) (*PruneOutput, error) {
		if historyService != nil {
			if err := historyService.Prune(); err != nil {
				log.Printf("Prune: history failed: %v", err)
			}
		}

		if js != nil {
			_ = js.PurgeStream("EVENTS")
			_ = js.PurgeStream("COMMANDS")
		}

		rootDataDir := filepath.Dir(gatewayDataDir)
		logDir := filepath.Join(filepath.Dir(rootDataDir), "logs")
		if entries, err := diskIO.ReadDir(logDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
					_ = diskIO.Truncate(filepath.Join(logDir, entry.Name()), 0)
				}
			}
		}

		res := &PruneOutput{}
		res.Body.Message = "History, NATS streams, and logs have been cleared."
		return res, nil
	})
}
