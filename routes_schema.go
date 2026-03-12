package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- Schema types ---

type GetDomainInput struct {
	Domain string `path:"domain" doc:"Domain name (e.g. light, switch, sensor, binary_sensor)"`
}
type DomainListOutput struct{ Body []types.DomainDescriptor }
type DomainOutput struct{ Body types.DomainDescriptor }

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
