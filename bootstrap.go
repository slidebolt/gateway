package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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
	apiPort := runner.MustGetEnv(runner.EnvAPIPort)
	natsURL := runner.MustGetEnv(runner.EnvNATSURL)
	rpcSubject := os.Getenv(runner.EnvPluginRPCSbj)
	dataDir := runner.MustGetEnv(runner.EnvPluginData)

	vstore = loadVirtualStore(dataDir)
	gatewayRT = gatewayRuntimeInfo{NATSURL: natsURL}

	gatewayID := strings.TrimPrefix(rpcSubject, runner.SubjectRPCPrefix)
	if gatewayID == "" {
		gatewayID = "gateway"
	}
	writeRuntimeDescriptor(apiHost, apiPort, natsURL, gatewayID)

	var err error
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

	subscribeRegistry()
	subscribeEntityEvents()
	selfRegister(rpcSubject)
	startDiscoveryProbe()

	r := gin.Default()
	registerRoutes(r)

	srv := &http.Server{
		Addr:    apiHost + ":" + apiPort,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down gateway...")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
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
			regMu.Lock()
			registry[reg.Manifest.ID] = reg
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
