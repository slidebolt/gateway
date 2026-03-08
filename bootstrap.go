package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

func run() {
	apiHost := os.Getenv(runner.EnvAPIHost)
	if apiHost == "" {
		apiHost = "127.0.0.1"
	}
	apiPort, err := runner.RequireEnv(runner.EnvAPIPort)
	if err != nil {
		log.Fatal(err)
	}
	natsURL, err := runner.RequireEnv(runner.EnvNATSURL)
	if err != nil {
		log.Fatal(err)
	}
	rpcSubject := os.Getenv(runner.EnvPluginRPCSbj)
	dataDir, err := runner.RequireEnv(runner.EnvPluginData)
	if err != nil {
		log.Fatal(err)
	}

	vstore = loadVirtualStore(dataDir)
	historyPath := filepath.Join(dataDir, "history.db")
	history, err = openHistoryStore(historyPath)
	if err != nil {
		log.Fatalf("Gateway: failed to open history store: %v", err)
	}
	defer func() {
		if err := history.Close(); err != nil {
			log.Printf("Gateway: history close error: %v", err)
		}
	}()
	gatewayRT = gatewayRuntimeInfo{NATSURL: natsURL, Version: os.Getenv("APP_VERSION")}

	gatewayID := strings.TrimPrefix(rpcSubject, runner.SubjectRPCPrefix)
	if gatewayID == "" {
		gatewayID = "gateway"
	}
	writeRuntimeDescriptor(apiHost, apiPort, natsURL, gatewayID)

	for i := 0; i < 10; i++ {
		nc, err = nats.Connect(natsURL)
		if err == nil {
			break
		}
		log.Printf("Gateway: NATS connect failed (attempt %d/10): %v", i+1, err)
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatalf("Gateway: failed to connect to NATS after 10 attempts: %v", err)
	}
	defer nc.Close()

	startNATSDiscoveryBridge()

	js, err = nc.JetStream()
	if err != nil {
		log.Fatalf("Gateway: failed to initialize JetStream: %v", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "EVENTS",
		Subjects: []string{runner.SubjectEntityEvents},
		Storage:  nats.FileStorage,
		MaxMsgs:  5000,
	})
	if err != nil {
		log.Printf("Warning: failed to add EVENTS stream: %v", err)
	}

	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "COMMANDS",
		Subjects: []string{runner.SubjectCommandStatus},
		Storage:  nats.FileStorage,
		MaxMsgs:  5000,
	})
	if err != nil {
		log.Printf("Warning: failed to add COMMANDS stream: %v", err)
	}

	historyCtx, stopHistory := context.WithCancel(context.Background())
	startHistoryConsumers(historyCtx)

	subscribeRegistry()
	subscribeEntityEvents()
	selfRegister(rpcSubject)
	startDiscoveryProbe()

	r := gin.Default()

	config := huma.DefaultConfig("SlideBolt Gateway API", "1.0.0")
	config.Info.Description = "REST API for managing plugins, devices, entities, scripts, commands, and events. " +
		"Plugin-scoped routes proxy requests to the target plugin over NATS. " +
		"Also available as an MCP server over stdio."

	// Keep error responses as {"error": "message"} to match existing wire format.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		detail := msg
		if len(errs) > 0 && errs[0] != nil && errs[0].Error() != "" {
			detail = errs[0].Error()
		}
		return &apiError{status: status, Message: detail}
	}

	api := humagin.New(r, config)
	registerRoutes(api)
	r.GET("/api/topics/subscribe", sseHandler)

	srv := &http.Server{
		Addr:    apiHost + ":" + apiPort,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// MCP bridge over Stdio — tools are generated directly from the OpenAPI spec,
	// so every REST route is automatically available to AI agents.
	mcpBridge := NewMCPBridge(api, "http://"+apiHost+":"+apiPort)
	go mcpBridge.Serve()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down gateway...")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stopHistory()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Gateway shutdown error: %v", err)
	}

	_ = nc.Drain()
	log.Println("Gateway exiting")
}

func subscribeRegistry() {
	_, _ = nc.Subscribe(runner.SubjectRegistration, func(m *nats.Msg) {
		var reg types.Registration
		if err := json.Unmarshal(m.Data, &reg); err == nil {
			for _, schema := range reg.Manifest.Schemas {
				types.RegisterDomain(schema)
			}
			regMu.Lock()
			registry[reg.Manifest.ID] = pluginRecord{
				Registration: reg,
				Valid:        true,
			}
			regMu.Unlock()
		}
	})
}

func selfRegister(rpcSubject string) {
	if rpcSubject == "" {
		return
	}
	gatewayID := strings.TrimPrefix(rpcSubject, runner.SubjectRPCPrefix)
	manifest := types.Manifest{ID: gatewayID, Name: "SlideBolt Gateway", Version: "1.0.0"}
	reg := types.Registration{Manifest: manifest, RPCSubject: rpcSubject}
	regData, _ := json.Marshal(reg)

	_, _ = nc.Subscribe(rpcSubject, func(m *nats.Msg) {
		var req types.Request
		json.Unmarshal(m.Data, &req)
		var result any
		var rpcErr *types.RPCError
		if req.Method == runner.HealthEndpoint {
			result = map[string]string{"status": "perfect", "service": "gateway"}
		} else {
			rpcErr = &types.RPCError{Code: -32601, Message: "method not found"}
		}
		var resBytes json.RawMessage
		if result != nil {
			resBytes, _ = json.Marshal(result)
		}
		resp := types.Response{JSONRPC: types.JSONRPCVersion, Result: resBytes, Error: rpcErr}
		if req.ID != nil {
			resp.ID = *req.ID
		}
		data, _ := json.Marshal(resp)
		m.Respond(data)
	})

	_, _ = nc.Subscribe(runner.SubjectSearchPlugins, func(m *nats.Msg) {
		res := types.SearchPluginsResponse{
			PluginID: gatewayID,
			Matches:  []types.Manifest{manifest},
		}
		data, _ := json.Marshal(res)
		_ = m.Respond(data)
	})

	_, _ = nc.Subscribe(runner.SubjectSearchDevices, func(m *nats.Msg) {
		res := types.SearchDevicesResponse{
			PluginID: gatewayID,
			Matches:  []types.Device{},
		}
		data, _ := json.Marshal(res)
		_ = m.Respond(data)
	})

	_, _ = nc.Subscribe(runner.SubjectSearchEntities, func(m *nats.Msg) {
		res := types.SearchEntitiesResponse{
			PluginID: gatewayID,
			Matches:  []types.Entity{},
		}
		data, _ := json.Marshal(res)
		_ = m.Respond(data)
	})

	_ = nc.Publish(runner.SubjectRegistration, regData)
	_, _ = nc.Subscribe(runner.SubjectDiscoveryProbe, func(m *nats.Msg) {
		_ = nc.Publish(runner.SubjectRegistration, regData)
	})
}

func startDiscoveryProbe() {
	go func() {
		for {
			_ = nc.Publish(runner.SubjectDiscoveryProbe, []byte("probe"))
			time.Sleep(2 * time.Second)
		}
	}()
}
