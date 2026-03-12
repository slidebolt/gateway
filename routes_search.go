package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	regsvc "github.com/slidebolt/registry"
	"github.com/slidebolt/sdk-types"
)

// --- Search types ---

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

		f := regsvc.Filter{
			Pattern:  pattern,
			Labels:   parseLabels(rawLabels),
			PluginID: strings.TrimSpace(input.PluginID),
			DeviceID: strings.TrimSpace(input.DeviceID),
			Limit:    input.Limit,
		}
		records, err := regsvc.QueryService(registryService).System().FindDevices(f)
		if err != nil {
			return &SearchDevicesOutput{Body: []types.Device{}}, nil
		}
		results := make([]types.Device, 0, len(records))
		for _, rec := range records {
			results = append(results, rec.Device)
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
	nc.PublishRequest(types.SubjectSearchPlugins, sub.Subject, data)

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

func startNATSDiscoveryBridge() {
	log.Printf("gateway: starting NATS discovery bridge on subject %s", types.SubjectGatewayDiscovery)
	_, err := nc.Subscribe(types.SubjectGatewayDiscovery, func(m *nats.Msg) {
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
