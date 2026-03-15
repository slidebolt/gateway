package main

import (
	"encoding/csv"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/slidebolt/sdk-types"
)

// encodeLabels serialises a multi-value label map as a space-separated
// "key:value" string. Keys and values are sorted for stable output.
// Returns "" when labels is nil or empty.
func encodeLabels(labels map[string][]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := make([]string, len(labels[k]))
		copy(vals, labels[k])
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, k+":"+v)
		}
	}
	return strings.Join(parts, "|")
}

// decodeLabels parses a "key:value|key:value" encoded label string.
// Multiple tokens with the same key accumulate into a slice.
// Returns nil when s is empty or contains no valid tokens.
func decodeLabels(s string) map[string][]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := map[string][]string{}
	for _, token := range strings.Split(s, "|") {
		token = strings.TrimSpace(token)
		idx := strings.IndexByte(token, ':')
		if idx <= 0 {
			continue
		}
		k, v := token[:idx], token[idx+1:]
		out[k] = append(out[k], v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// csvImportResult is the JSON body returned by the import endpoints.
type csvImportResult struct {
	Updated int              `json:"updated"`
	Skipped int              `json:"skipped"`
	Errors  []csvImportError `json:"errors,omitempty"`
}

type csvImportError struct {
	Row      int    `json:"row"`
	PluginID string `json:"plugin_id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	EntityID string `json:"entity_id,omitempty"`
	Error    string `json:"error"`
}

// registerCSVRoutes adds CSV export/import endpoints to the Gin engine.
func registerCSVRoutes(r *gin.Engine) {
	r.GET("/api/export/devices.csv", handleExportDevices)
	r.GET("/api/export/entities.csv", handleExportEntities)
	r.POST("/api/import/devices.csv", handleImportDevices)
	r.POST("/api/import/entities.csv", handleImportEntities)
}

func handleExportDevices(c *gin.Context) {
	devices := registryService.FindDevices(types.SearchQuery{})
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="devices.csv"`)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"plugin_id", "device_id", "local_name", "labels"})
	for _, d := range devices {
		_ = w.Write([]string{d.PluginID, d.ID, d.LocalName, encodeLabels(d.Labels)})
	}
	w.Flush()
}

func handleExportEntities(c *gin.Context) {
	entities := registryService.FindEntities(types.SearchQuery{})
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="entities.csv"`)
	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"plugin_id", "device_id", "entity_id", "local_name", "labels"})
	for _, e := range entities {
		_ = w.Write([]string{e.PluginID, e.DeviceID, e.ID, e.LocalName, encodeLabels(e.Labels)})
	}
	w.Flush()
}

func handleImportDevices(c *gin.Context) {
	cr := csv.NewReader(c.Request.Body)
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read CSV header: " + err.Error()})
		return
	}
	col := csvColIndex(header)
	for _, required := range []string{"plugin_id", "device_id"} {
		if _, ok := col[required]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required column: " + required})
			return
		}
	}

	var result csvImportResult
	rowNum := 1 // header is row 1
	for {
		rowNum++
		row, err := cr.Read()
		if err != nil {
			break
		}
		pluginID := csvCol(row, col, "plugin_id")
		deviceID := csvCol(row, col, "device_id")
		localName := csvCol(row, col, "local_name")
		labelsStr := csvCol(row, col, "labels")

		if pluginID == "" || deviceID == "" || (localName == "" && labelsStr == "") {
			result.Skipped++
			continue
		}

		ok := true

		if localName != "" {
			payload := types.Device{ID: deviceID, LocalName: localName}
			resp := routeRPC(pluginID, "devices/update", payload)
			if resp.Error != nil {
				result.Errors = append(result.Errors, csvImportError{
					Row: rowNum, PluginID: pluginID, DeviceID: deviceID,
					Error: "name: " + resp.Error.Message,
				})
				ok = false
			} else {
				saveDeviceToRegistry(pluginID, resp.Result, payload)
			}
		}

		if ok && labelsStr != "" {
			payload := types.Device{ID: deviceID, Labels: decodeLabels(labelsStr)}
			resp := routeRPC(pluginID, "devices/update", payload)
			if resp.Error != nil {
				result.Errors = append(result.Errors, csvImportError{
					Row: rowNum, PluginID: pluginID, DeviceID: deviceID,
					Error: "labels: " + resp.Error.Message,
				})
				ok = false
			} else {
				saveDeviceToRegistry(pluginID, resp.Result, payload)
			}
		}

		if ok {
			result.Updated++
		}
	}
	c.JSON(http.StatusOK, result)
}

func handleImportEntities(c *gin.Context) {
	cr := csv.NewReader(c.Request.Body)
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read CSV header: " + err.Error()})
		return
	}
	col := csvColIndex(header)
	for _, required := range []string{"plugin_id", "device_id", "entity_id"} {
		if _, ok := col[required]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing required column: " + required})
			return
		}
	}

	var result csvImportResult
	rowNum := 1
	for {
		rowNum++
		row, err := cr.Read()
		if err != nil {
			break
		}
		pluginID := csvCol(row, col, "plugin_id")
		deviceID := csvCol(row, col, "device_id")
		entityID := csvCol(row, col, "entity_id")
		localName := csvCol(row, col, "local_name")
		labelsStr := csvCol(row, col, "labels")

		if pluginID == "" || deviceID == "" || entityID == "" || (localName == "" && labelsStr == "") {
			result.Skipped++
			continue
		}

		payload := types.Entity{ID: entityID, DeviceID: deviceID}
		if localName != "" {
			payload.LocalName = localName
		}
		if labelsStr != "" {
			payload.Labels = decodeLabels(labelsStr)
		}

		resp := routeRPC(pluginID, "entities/update", payload)
		if resp.Error != nil {
			result.Errors = append(result.Errors, csvImportError{
				Row: rowNum, PluginID: pluginID, DeviceID: deviceID, EntityID: entityID,
				Error: resp.Error.Message,
			})
			continue
		}
		saveEntityToRegistry(pluginID, deviceID, resp.Result, payload)
		result.Updated++
	}
	c.JSON(http.StatusOK, result)
}

// csvColIndex builds a column-name → index map from a CSV header row.
func csvColIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(h)] = i
	}
	return m
}

// csvCol reads a named field from a CSV row, returning "" if absent.
func csvCol(row []string, col map[string]int, name string) string {
	idx, ok := col[name]
	if !ok || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}
