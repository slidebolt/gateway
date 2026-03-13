package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/slidebolt/sdk-types"
)

// --- Types ---

type CreateEventSubscriptionInput struct {
	Body EventFilter
}

type CreateEventSubscriptionOutput struct {
	Body struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"created_at"`
	}
}

type GetEventSubscriptionEventsInput struct {
	ID string `path:"id" doc:"Subscription ID"`
}

type GetEventSubscriptionEventsOutput struct {
	Body struct {
		Events []types.EntityEventEnvelope `json:"events"`
	}
}

type DeleteEventSubscriptionInput struct {
	ID string `path:"id" doc:"Subscription ID"`
}

type StreamEventSubscriptionInput struct {
	ID string `path:"id" doc:"Subscription ID"`
}

func registerEventSubscriptionRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-event-subscription",
		Method:      http.MethodPost,
		Path:        "/api/events/subscriptions",
		Summary:     "Create dynamic event subscription",
		Description: "Registers a dynamic subscription that matches events from entities satisfying entity_query with an optional action filter. Returns a subscription ID. Use the stream or events endpoints to consume matched events.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *CreateEventSubscriptionInput) (*CreateEventSubscriptionOutput, error) {
		if dynamicEventService == nil {
			return nil, &apiError{status: http.StatusServiceUnavailable, Message: "dynamic event service not available"}
		}
		id, _ := dynamicEventService.Subscribe(input.Body)
		out := &CreateEventSubscriptionOutput{}
		out.Body.ID = id
		out.Body.CreatedAt = time.Now().UTC()
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-subscription-events",
		Method:      http.MethodGet,
		Path:        "/api/events/subscriptions/{id}/events",
		Summary:     "Poll buffered subscription events",
		Description: "Returns and drains all events currently buffered for the subscription. Useful for polling clients and testing without SSE.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *GetEventSubscriptionEventsInput) (*GetEventSubscriptionEventsOutput, error) {
		if dynamicEventService == nil {
			return nil, &apiError{status: http.StatusServiceUnavailable, Message: "dynamic event service not available"}
		}
		events := dynamicEventService.Drain(input.ID)
		if events == nil {
			return nil, notFoundErr(fmt.Sprintf("subscription %q not found", input.ID))
		}
		out := &GetEventSubscriptionEventsOutput{}
		out.Body.Events = events
		if out.Body.Events == nil {
			out.Body.Events = []types.EntityEventEnvelope{}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-event-subscription",
		Method:      http.MethodDelete,
		Path:        "/api/events/subscriptions/{id}",
		Summary:     "Delete dynamic event subscription",
		Description: "Cancels a dynamic event subscription and closes its event channel.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *DeleteEventSubscriptionInput) (*DeleteOutput, error) {
		if dynamicEventService == nil {
			return nil, &apiError{status: http.StatusServiceUnavailable, Message: "dynamic event service not available"}
		}
		dynamicEventService.Unsubscribe(input.ID)
		result, _ := json.Marshal(map[string]string{"id": input.ID, "status": "deleted"})
		return &DeleteOutput{Body: result}, nil
	})

	// SSE stream endpoint — Phase 2 delivery mechanism.
	// Clients open this connection and receive events as server-sent events
	// in the format: "data: <EntityEventEnvelope JSON>\n\n"
	huma.Register(api, huma.Operation{
		OperationID: "stream-subscription-events",
		Method:      http.MethodGet,
		Path:        "/api/events/subscriptions/{id}/stream",
		Summary:     "Stream subscription events via SSE",
		Description: "Opens a Server-Sent Events stream that delivers matched EntityEventEnvelopes in real time. The connection stays open until the subscription is deleted or the client disconnects.",
		Tags:        []string{"events"},
	}, func(ctx context.Context, input *StreamEventSubscriptionInput) (*huma.StreamResponse, error) {
		if dynamicEventService == nil {
			return nil, &apiError{status: http.StatusServiceUnavailable, Message: "dynamic event service not available"}
		}
		// Locate the subscription channel without consuming it.
		dynamicEventService.mu.RLock()
		sub, ok := dynamicEventService.subs[input.ID]
		dynamicEventService.mu.RUnlock()
		if !ok {
			return nil, notFoundErr(fmt.Sprintf("subscription %q not found", input.ID))
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/event-stream")
				ctx.SetHeader("Cache-Control", "no-cache")
				ctx.SetHeader("X-Accel-Buffering", "no")

				w := ctx.BodyWriter()
				flusher, canFlush := w.(http.Flusher)
				reqCtx := ctx.Context()

				for {
					select {
					case env, open := <-sub.ch:
						if !open {
							return
						}
						data, err := json.Marshal(env)
						if err != nil {
							continue
						}
						fmt.Fprintf(w, "data: %s\n\n", data)
						if canFlush {
							flusher.Flush()
						}
					case <-reqCtx.Done():
						return
					}
				}
			},
		}, nil
	})
}
