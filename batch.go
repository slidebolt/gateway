package main

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/slidebolt/sdk-types"
)

func registerBatchRoutes(r *gin.Engine) {
	// --- Devices ---
	r.POST("/api/batch/devices", batchGetDevices)
	r.POST("/api/batch/devices/create", batchCreateDevices)
	r.PUT("/api/batch/devices", batchUpdateDevices)
	r.DELETE("/api/batch/devices", batchDeleteDevices)

	// --- Entities ---
	r.POST("/api/batch/entities", batchGetEntities)
	r.POST("/api/batch/entities/create", batchCreateEntities)
	r.PUT("/api/batch/entities", batchUpdateEntities)
	r.DELETE("/api/batch/entities", batchDeleteEntities)
}

// batchGetDevices fetches specific devices by plugin_id + device_id.
func batchGetDevices(c *gin.Context) {
	var refs []types.BatchDeviceRef
	if err := c.ShouldBindJSON(&refs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Group refs by plugin so we only call each plugin once.
	byPlugin := map[string][]string{}
	for _, ref := range refs {
		byPlugin[ref.PluginID] = append(byPlugin[ref.PluginID], ref.DeviceID)
	}

	// Fetch all devices per plugin and index them.
	index := map[string]types.Device{}
	pluginErr := map[string]string{}
	for pluginID, ids := range byPlugin {
		resp := routeRPC(pluginID, "devices/list", nil)
		if resp.Error != nil {
			for _, id := range ids {
				pluginErr[pluginID+"|"+id] = resp.Error.Message
			}
			continue
		}
		var devices []types.Device
		if err := json.Unmarshal(resp.Result, &devices); err != nil {
			for _, id := range ids {
				pluginErr[pluginID+"|"+id] = err.Error()
			}
			continue
		}
		for _, d := range devices {
			index[pluginID+"|"+d.ID] = d
		}
	}

	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		key := ref.PluginID + "|" + ref.DeviceID
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID}
		if errMsg, bad := pluginErr[key]; bad {
			r.Error = errMsg
		} else if dev, found := index[key]; found {
			r.OK = true
			r.Data, _ = json.Marshal(dev)
		} else {
			r.Error = "not found"
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchCreateDevices creates multiple devices, each routed to its plugin.
func batchCreateDevices(c *gin.Context) {
	var items []types.BatchDeviceItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.Device.ID}
		resp := routeRPC(item.PluginID, "devices/create", item.Device)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchUpdateDevices updates multiple devices, each routed to its plugin.
func batchUpdateDevices(c *gin.Context) {
	var items []types.BatchDeviceItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.Device.ID}
		resp := routeRPC(item.PluginID, "devices/update", item.Device)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchDeleteDevices deletes multiple devices by plugin_id + device_id.
func batchDeleteDevices(c *gin.Context) {
	var refs []types.BatchDeviceRef
	if err := c.ShouldBindJSON(&refs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID}
		resp := routeRPC(ref.PluginID, "devices/delete", ref.DeviceID)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchGetEntities fetches specific entities by plugin_id + device_id + entity_id.
func batchGetEntities(c *gin.Context) {
	var refs []types.BatchEntityRef
	if err := c.ShouldBindJSON(&refs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Group refs by (plugin_id, device_id) so we only list once per device.
	type deviceKey struct{ pluginID, deviceID string }
	byDevice := map[deviceKey][]string{}
	for _, ref := range refs {
		k := deviceKey{ref.PluginID, ref.DeviceID}
		byDevice[k] = append(byDevice[k], ref.EntityID)
	}

	index := map[string]types.Entity{}
	deviceErr := map[string]string{}
	for k := range byDevice {
		resp := routeRPC(k.pluginID, "entities/list", gin.H{"device_id": k.deviceID})
		entities, err := parseEntities(resp)
		if err != nil {
			deviceErr[k.pluginID+"|"+k.deviceID] = err.Error()
			continue
		}
		for _, e := range entities {
			index[k.pluginID+"|"+k.deviceID+"|"+e.ID] = e
		}
	}

	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID, EntityID: ref.EntityID}
		devKey := ref.PluginID + "|" + ref.DeviceID
		if errMsg, bad := deviceErr[devKey]; bad {
			r.Error = errMsg
		} else if ent, found := index[devKey+"|"+ref.EntityID]; found {
			r.OK = true
			r.Data, _ = json.Marshal(ent)
		} else {
			r.Error = "not found"
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchCreateEntities creates multiple entities, each routed to its plugin.
func batchCreateEntities(c *gin.Context) {
	var items []types.BatchEntityItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		item.Entity.DeviceID = item.DeviceID
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.DeviceID, EntityID: item.Entity.ID}
		resp := routeRPC(item.PluginID, "entities/create", item.Entity)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchUpdateEntities updates multiple entities, each routed to its plugin.
func batchUpdateEntities(c *gin.Context) {
	var items []types.BatchEntityItem
	if err := c.ShouldBindJSON(&items); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(items))
	for i, item := range items {
		item.Entity.DeviceID = item.DeviceID
		r := types.BatchResult{PluginID: item.PluginID, DeviceID: item.DeviceID, EntityID: item.Entity.ID}
		resp := routeRPC(item.PluginID, "entities/update", item.Entity)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
			r.Data = resp.Result
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}

// batchDeleteEntities deletes multiple entities by plugin_id + device_id + entity_id.
func batchDeleteEntities(c *gin.Context) {
	var refs []types.BatchEntityRef
	if err := c.ShouldBindJSON(&refs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	results := make([]types.BatchResult, len(refs))
	for i, ref := range refs {
		r := types.BatchResult{PluginID: ref.PluginID, DeviceID: ref.DeviceID, EntityID: ref.EntityID}
		params := gin.H{"device_id": ref.DeviceID, "entity_id": ref.EntityID}
		resp := routeRPC(ref.PluginID, "entities/delete", params)
		if resp.Error != nil {
			r.Error = resp.Error.Message
		} else {
			r.OK = true
		}
		results[i] = r
	}
	c.JSON(http.StatusOK, results)
}
