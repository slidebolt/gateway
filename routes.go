package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

func registerRoutes(r *gin.Engine) {
	r.GET(runner.HealthEndpoint, healthHandler)
	r.GET("/_internal/runtime", runtimeInfoHandler)
	r.GET("/api/plugins", pluginsHandler)
	registerDeviceRoutes(r)
	registerEntityRoutes(r)
	registerCommandRoutes(r)
	registerEventRoutes(r)
	registerSearchRoutes(r)
	r.GET("/api/journal/events", eventJournalHandler)
}

func runtimeInfoHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gatewayRT)
}

func healthHandler(c *gin.Context) {
	id := c.Query("id")
	if id == "" {
		c.JSON(http.StatusOK, gin.H{"status": "perfect", "service": "gateway"})
		return
	}
	resp := routeRPC(id, runner.HealthEndpoint, nil)
	if resp.Error != nil {
		c.JSON(http.StatusServiceUnavailable, resp.Error)
		return
	}
	c.JSON(http.StatusOK, resp.Result)
}

func pluginsHandler(c *gin.Context) {
	regMu.RLock()
	defer regMu.RUnlock()
	c.JSON(http.StatusOK, registry)
}

func registerDeviceRoutes(r *gin.Engine) {
	r.GET("/api/plugins/:id/devices", func(c *gin.Context) {
		resp := routeRPC(c.Param("id"), "devices/list", nil)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})

	r.POST("/api/plugins/:id/devices", func(c *gin.Context) {
		var dev types.Device
		if err := c.ShouldBindJSON(&dev); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp := routeRPC(c.Param("id"), "devices/create", dev)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})

	r.PUT("/api/plugins/:id/devices", func(c *gin.Context) {
		var dev types.Device
		if err := c.ShouldBindJSON(&dev); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp := routeRPC(c.Param("id"), "devices/update", dev)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})

	r.DELETE("/api/plugins/:id/devices/:device_id", func(c *gin.Context) {
		resp := routeRPC(c.Param("id"), "devices/delete", c.Param("device_id"))
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})
}

func registerEntityRoutes(r *gin.Engine) {
	r.GET("/api/plugins/:id/devices/:did/entities", func(c *gin.Context) {
		pluginID := c.Param("id")
		deviceID := c.Param("did")
		resp := routeRPC(pluginID, "entities/list", gin.H{"device_id": deviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		vstore.mu.RLock()
		for _, rec := range vstore.entities {
			if rec.OwnerPluginID == pluginID && rec.OwnerDeviceID == deviceID {
				entities = append(entities, rec.Entity)
			}
		}
		vstore.mu.RUnlock()
		c.JSON(http.StatusOK, entities)
	})

	r.POST("/api/plugins/:id/devices/:did/entities", func(c *gin.Context) {
		var ent types.Entity
		if err := c.ShouldBindJSON(&ent); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		ent.DeviceID = c.Param("did")
		resp := routeRPC(c.Param("id"), "entities/create", ent)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})

	r.POST("/api/plugins/:id/devices/:did/entities/virtual", createVirtualEntityHandler)
}

func createVirtualEntityHandler(c *gin.Context) {
	pluginID := c.Param("id")
	deviceID := c.Param("did")
	var req struct {
		ID             string   `json:"id"`
		LocalName      string   `json:"local_name"`
		Actions        []string `json:"actions"`
		SourcePluginID string   `json:"source_plugin_id"`
		SourceDeviceID string   `json:"source_device_id"`
		SourceEntityID string   `json:"source_entity_id"`
		MirrorSource   *bool    `json:"mirror_source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ID == "" || req.SourcePluginID == "" || req.SourceDeviceID == "" || req.SourceEntityID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id, source_plugin_id, source_device_id, source_entity_id are required"})
		return
	}
	key := entityKey(pluginID, deviceID, req.ID)
	if _, err := findEntity(pluginID, deviceID, req.ID); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "entity id already exists in plugin"})
		return
	}
	vstore.mu.RLock()
	_, exists := vstore.entities[key]
	vstore.mu.RUnlock()
	if exists {
		c.JSON(http.StatusConflict, gin.H{"error": "virtual entity id already exists"})
		return
	}
	source, err := findEntity(req.SourcePluginID, req.SourceDeviceID, req.SourceEntityID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "source entity not found"})
		return
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
	ent := types.Entity{ID: req.ID, DeviceID: deviceID, Domain: source.Domain, LocalName: localName, Actions: actions, Data: source.Data}
	ent.Data.SyncStatus = "in_sync"
	ent.Data.UpdatedAt = time.Now().UTC()

	rec := virtualEntityRecord{
		OwnerPluginID:  pluginID,
		OwnerDeviceID:  deviceID,
		SourcePluginID: req.SourcePluginID,
		SourceDeviceID: req.SourceDeviceID,
		SourceEntityID: req.SourceEntityID,
		MirrorSource:   mirror,
		Entity:         ent,
	}
	vstore.mu.Lock()
	vstore.entities[key] = rec
	vstore.persistLocked()
	vstore.mu.Unlock()
	c.JSON(http.StatusCreated, ent)
}

func registerCommandRoutes(r *gin.Engine) {
	r.POST("/api/plugins/:id/devices/:did/entities/:eid/commands", func(c *gin.Context) {
		pluginID := c.Param("id")
		deviceID := c.Param("did")
		entityID := c.Param("eid")
		var payload json.RawMessage
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		key := entityKey(pluginID, deviceID, entityID)
		vstore.mu.RLock()
		vrec, isVirtual := vstore.entities[key]
		vstore.mu.RUnlock()
		if isVirtual {
			actionType, err := parseActionType(payload)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			if len(vrec.Entity.Actions) > 0 && !containsAction(vrec.Entity.Actions, actionType) {
				c.JSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("action %q not supported by this virtual entity", actionType)})
				return
			}
			params := gin.H{"device_id": vrec.SourceDeviceID, "entity_id": vrec.SourceEntityID, "payload": payload}
			sourceResp := routeRPC(vrec.SourcePluginID, "entities/commands/create", params)
			if sourceResp.Error != nil {
				c.JSON(http.StatusForbidden, sourceResp.Error)
				return
			}
			var sourceStatus types.CommandStatus
			if err := json.Unmarshal(sourceResp.Result, &sourceStatus); err != nil {
				c.JSON(http.StatusForbidden, gin.H{"error": "invalid source command status"})
				return
			}
			now := time.Now().UTC()
			virtualCID := nextID("vcmd")
			status := types.CommandStatus{CommandID: virtualCID, PluginID: pluginID, DeviceID: deviceID, EntityID: entityID, EntityType: vrec.Entity.Domain, State: types.CommandPending, CreatedAt: now, LastUpdatedAt: now}
			vstore.mu.Lock()
			vstore.commands[virtualCID] = virtualCommandRecord{OwnerPluginID: pluginID, SourcePluginID: vrec.SourcePluginID, SourceCommand: sourceStatus.CommandID, VirtualKey: key, Status: status}
			vrec.Entity.Data.LastCommandID = virtualCID
			vrec.Entity.Data.SyncStatus = "pending"
			vrec.Entity.Data.UpdatedAt = now
			vstore.entities[key] = vrec
			vstore.persistLocked()
			vstore.mu.Unlock()
			go monitorVirtualCommand(virtualCID)
			c.JSON(http.StatusAccepted, status)
			return
		}
		params := gin.H{"device_id": deviceID, "entity_id": entityID, "payload": payload}
		resp := routeRPC(pluginID, "entities/commands/create", params)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusAccepted, resp.Result)
	})

	r.GET("/api/plugins/:id/commands/:cid", func(c *gin.Context) {
		pluginID := c.Param("id")
		cid := c.Param("cid")
		vstore.mu.RLock()
		rec, isVirtual := vstore.commands[cid]
		vstore.mu.RUnlock()
		if isVirtual {
			if rec.OwnerPluginID != pluginID {
				c.JSON(http.StatusForbidden, gin.H{"error": "command not owned by plugin"})
				return
			}
			c.JSON(http.StatusOK, rec.Status)
			return
		}
		resp := routeRPC(pluginID, "commands/status/get", gin.H{"command_id": cid})
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})
}

func registerEventRoutes(r *gin.Engine) {
	r.POST("/api/plugins/:id/devices/:did/entities/:eid/events", func(c *gin.Context) {
		pluginID := c.Param("id")
		deviceID := c.Param("did")
		entityID := c.Param("eid")
		var payload json.RawMessage
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		correlationID := c.GetHeader("X-Correlation-ID")
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
			vstore.appendEventLocked(observedEvent{Name: classifyEventName(vrec.Entity.Domain, payload, true), PluginID: pluginID, DeviceID: deviceID, EntityID: entityID, EventID: vrec.Entity.Data.LastEventID, CreatedAt: time.Now().UTC()})
			vstore.persistLocked()
			out := vrec.Entity
			vstore.mu.Unlock()
			c.JSON(http.StatusOK, out)
			return
		}
		params := gin.H{"device_id": deviceID, "entity_id": entityID, "payload": payload, "correlation_id": correlationID}
		resp := routeRPC(pluginID, "entities/events/ingest", params)
		if resp.Error != nil {
			c.JSON(http.StatusForbidden, resp.Error)
			return
		}
		c.JSON(http.StatusOK, resp.Result)
	})
}

func registerSearchRoutes(r *gin.Engine) {
	r.GET("/api/search/plugins", func(c *gin.Context) {
		pattern := c.Query("q")
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
		c.JSON(http.StatusOK, results)
	})

	r.GET("/api/search/devices", func(c *gin.Context) {
		pattern := c.Query("q")
		if pattern == "" {
			pattern = "*"
		}
		query := types.SearchQuery{Pattern: pattern}
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
		c.JSON(http.StatusOK, results)
	})
}

func eventJournalHandler(c *gin.Context) {
	pluginID := c.Query("plugin_id")
	deviceID := c.Query("device_id")
	entityID := c.Query("entity_id")
	vstore.mu.RLock()
	defer vstore.mu.RUnlock()
	out := make([]observedEvent, 0)
	for _, evt := range vstore.events {
		if pluginID != "" && evt.PluginID != pluginID {
			continue
		}
		if deviceID != "" && evt.DeviceID != deviceID {
			continue
		}
		if entityID != "" && evt.EntityID != entityID {
			continue
		}
		out = append(out, evt)
	}
	c.JSON(http.StatusOK, out)
}
