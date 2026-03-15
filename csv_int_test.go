//go:build integration

package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/slidebolt/sdk-types"
)

// doCSVPost sends a POST with text/csv body to the test server.
func doCSVPost(t *testing.T, h *apiHarness, path, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.Server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "text/csv")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// parseCSVResponse reads all records from a response body and closes it.
func parseCSVResponse(t *testing.T, resp *http.Response) [][]string {
	t.Helper()
	defer resp.Body.Close()
	r := csv.NewReader(resp.Body)
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV response: %v", err)
	}
	return records
}

func TestIntegration_ExportDevicesCSV_Header(t *testing.T) {
	h := newAPIHarness(t)

	resp := h.get(t, "/api/export/devices.csv")
	assertStatus(t, resp, http.StatusOK)

	records := parseCSVResponse(t, resp)
	if len(records) == 0 {
		t.Fatal("expected at least a header row")
	}
	header := records[0]
	want := []string{"plugin_id", "device_id", "local_name", "labels"}
	if len(header) != len(want) {
		t.Fatalf("header columns: got %v, want %v", header, want)
	}
	for i, col := range want {
		if header[i] != col {
			t.Errorf("header[%d]: got %q, want %q", i, header[i], col)
		}
	}
}

func TestIntegration_ExportDevicesCSV_Data(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "csv-export-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{
				ID:        "dev-1",
				PluginID:  "csv-export-plugin",
				SourceID:  "dev-1",
				LocalName: "Living Room Hub",
				Labels:    map[string][]string{"room": {"living"}, "floor": {"ground"}},
			},
			{
				ID:        "dev-2",
				PluginID:  "csv-export-plugin",
				SourceID:  "dev-2",
				LocalName: "Basement Hub",
			},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	resp := h.get(t, "/api/export/devices.csv")
	assertStatus(t, resp, http.StatusOK)

	records := parseCSVResponse(t, resp)
	// header + at least 2 data rows
	if len(records) < 3 {
		t.Fatalf("expected header + 2 data rows, got %d rows", len(records))
	}

	// Build a lookup by device_id (col 1).
	byDeviceID := map[string][]string{}
	for _, row := range records[1:] {
		if len(row) >= 2 {
			byDeviceID[row[1]] = row
		}
	}

	row1, ok := byDeviceID["dev-1"]
	if !ok {
		t.Fatal("dev-1 not found in export")
	}
	if row1[2] != "Living Room Hub" {
		t.Errorf("dev-1 local_name: got %q, want %q", row1[2], "Living Room Hub")
	}
	// Labels are sorted: floor:ground|room:living
	if row1[3] != "floor:ground|room:living" {
		t.Errorf("dev-1 labels: got %q, want %q", row1[3], "floor:ground|room:living")
	}

	row2, ok := byDeviceID["dev-2"]
	if !ok {
		t.Fatal("dev-2 not found in export")
	}
	if row2[2] != "Basement Hub" {
		t.Errorf("dev-2 local_name: got %q, want %q", row2[2], "Basement Hub")
	}
	if row2[3] != "" {
		t.Errorf("dev-2 labels: got %q, want empty", row2[3])
	}
}

func TestIntegration_ExportEntitiesCSV_Header(t *testing.T) {
	h := newAPIHarness(t)

	resp := h.get(t, "/api/export/entities.csv")
	assertStatus(t, resp, http.StatusOK)

	records := parseCSVResponse(t, resp)
	if len(records) == 0 {
		t.Fatal("expected at least a header row")
	}
	header := records[0]
	want := []string{"plugin_id", "device_id", "entity_id", "local_name", "labels"}
	if len(header) != len(want) {
		t.Fatalf("header: got %v, want %v", header, want)
	}
	for i, col := range want {
		if header[i] != col {
			t.Errorf("header[%d]: got %q, want %q", i, header[i], col)
		}
	}
}

func TestIntegration_ExportEntitiesCSV_Data(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "csv-ent-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "bridge", PluginID: "csv-ent-plugin", SourceID: "bridge", LocalName: "Bridge"},
		},
		Entities: []types.Entity{
			{
				ID: "light-1", PluginID: "csv-ent-plugin", DeviceID: "bridge",
				Domain: "light", LocalName: "Ceiling Light",
				Labels: map[string][]string{"room": {"living"}},
			},
			{
				ID: "switch-1", PluginID: "csv-ent-plugin", DeviceID: "bridge",
				Domain: "switch", LocalName: "Wall Switch",
			},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	resp := h.get(t, "/api/export/entities.csv")
	assertStatus(t, resp, http.StatusOK)

	records := parseCSVResponse(t, resp)
	if len(records) < 3 {
		t.Fatalf("expected header + 2 rows, got %d", len(records))
	}

	// col indices: plugin_id=0, device_id=1, entity_id=2, local_name=3, labels=4
	byEntityID := map[string][]string{}
	for _, row := range records[1:] {
		if len(row) >= 3 {
			byEntityID[row[2]] = row
		}
	}

	row, ok := byEntityID["light-1"]
	if !ok {
		t.Fatal("light-1 not found in export")
	}
	if row[3] != "Ceiling Light" {
		t.Errorf("light-1 local_name: got %q", row[3])
	}
	if row[4] != "room:living" {
		t.Errorf("light-1 labels: got %q, want %q", row[4], "room:living")
	}
}

func TestIntegration_ImportDevicesCSV_UpdatesNameAndLabels(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "csv-import-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "dev-a", SourceID: "dev-a", LocalName: ""},
			{ID: "dev-b", SourceID: "dev-b", LocalName: "Old Name"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	csvBody := strings.Join([]string{
		"plugin_id,device_id,local_name,labels",
		"csv-import-plugin,dev-a,Kitchen Hub,room:kitchen|floor:ground",
		"csv-import-plugin,dev-b,Updated Name,",
	}, "\n")

	resp := doCSVPost(t, h, "/api/import/devices.csv", csvBody)
	assertStatus(t, resp, http.StatusOK)

	var result csvImportResult
	readJSON(t, resp, &result)

	if result.Updated != 2 {
		t.Errorf("updated: got %d, want 2 (errors: %v)", result.Updated, result.Errors)
	}
	if result.Skipped != 0 {
		t.Errorf("skipped: got %d, want 0", result.Skipped)
	}
	if len(result.Errors) != 0 {
		t.Errorf("errors: %v", result.Errors)
	}

	// Verify the plugin's in-memory state was updated.
	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	for _, d := range plugin.Devices {
		switch d.ID {
		case "dev-a":
			if d.LocalName != "Kitchen Hub" {
				t.Errorf("dev-a local_name: got %q, want %q", d.LocalName, "Kitchen Hub")
			}
			if d.Labels["room"][0] != "kitchen" {
				t.Errorf("dev-a labels: got %v", d.Labels)
			}
		case "dev-b":
			if d.LocalName != "Updated Name" {
				t.Errorf("dev-b local_name: got %q, want %q", d.LocalName, "Updated Name")
			}
		}
	}
}

func TestIntegration_ImportDevicesCSV_SkipsBlankRows(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "csv-skip-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "dev-1", SourceID: "dev-1", LocalName: "Original"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	// Rows with both local_name and labels empty should be skipped (no-op).
	csvBody := strings.Join([]string{
		"plugin_id,device_id,local_name,labels",
		"csv-skip-plugin,dev-1,,", // both empty → skip
		",dev-1,Name,",            // no plugin_id → skip
		"csv-skip-plugin,,Name,",  // no device_id → skip
	}, "\n")

	resp := doCSVPost(t, h, "/api/import/devices.csv", csvBody)
	assertStatus(t, resp, http.StatusOK)

	var result csvImportResult
	readJSON(t, resp, &result)

	if result.Updated != 0 {
		t.Errorf("updated: got %d, want 0", result.Updated)
	}
	if result.Skipped != 3 {
		t.Errorf("skipped: got %d, want 3", result.Skipped)
	}
}

func TestIntegration_ImportDevicesCSV_ReportsErrorForUnknownDevice(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID:      "csv-err-plugin",
		nc:      h.NC,
		Devices: []types.Device{},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	csvBody := strings.Join([]string{
		"plugin_id,device_id,local_name,labels",
		"csv-err-plugin,nonexistent,Some Name,",
	}, "\n")

	resp := doCSVPost(t, h, "/api/import/devices.csv", csvBody)
	assertStatus(t, resp, http.StatusOK)

	var result csvImportResult
	readJSON(t, resp, &result)

	if result.Updated != 0 {
		t.Errorf("updated: got %d, want 0", result.Updated)
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error for unknown device")
	}
}

func TestIntegration_ImportDevicesCSV_MissingRequiredColumn(t *testing.T) {
	h := newAPIHarness(t)

	// CSV with no plugin_id column.
	csvBody := "device_id,local_name\ndev-1,My Device"
	resp := doCSVPost(t, h, "/api/import/devices.csv", csvBody)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestIntegration_ImportEntitiesCSV_UpdatesNameAndLabels(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "csv-ent-import",
		nc: h.NC,
		Devices: []types.Device{
			{ID: "bridge", SourceID: "bridge", LocalName: "Bridge"},
		},
		Entities: []types.Entity{
			{ID: "light-1", DeviceID: "bridge", Domain: "light", LocalName: ""},
			{ID: "light-2", DeviceID: "bridge", Domain: "light", LocalName: "Old"},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)

	csvBody := strings.Join([]string{
		"plugin_id,device_id,entity_id,local_name,labels",
		"csv-ent-import,bridge,light-1,Ceiling Light,room:living",
		"csv-ent-import,bridge,light-2,Updated Light,",
	}, "\n")

	resp := doCSVPost(t, h, "/api/import/entities.csv", csvBody)
	assertStatus(t, resp, http.StatusOK)

	var result csvImportResult
	readJSON(t, resp, &result)

	if result.Updated != 2 {
		t.Errorf("updated: got %d, want 2 (errors: %v)", result.Updated, result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}

	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	for _, e := range plugin.Entities {
		switch e.ID {
		case "light-1":
			if e.LocalName != "Ceiling Light" {
				t.Errorf("light-1 local_name: got %q", e.LocalName)
			}
			if len(e.Labels["room"]) == 0 || e.Labels["room"][0] != "living" {
				t.Errorf("light-1 labels: got %v", e.Labels)
			}
		case "light-2":
			if e.LocalName != "Updated Light" {
				t.Errorf("light-2 local_name: got %q", e.LocalName)
			}
		}
	}
}

func TestIntegration_ImportEntitiesCSV_MissingRequiredColumn(t *testing.T) {
	h := newAPIHarness(t)

	// Missing entity_id column.
	csvBody := "plugin_id,device_id,local_name\np,d,name"
	resp := doCSVPost(t, h, "/api/import/entities.csv", csvBody)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestIntegration_ExportImportRoundtrip(t *testing.T) {
	h := newAPIHarness(t)

	plugin := &SimulatedPlugin{
		ID: "roundtrip-plugin",
		nc: h.NC,
		Devices: []types.Device{
			{
				ID: "dev-rt", PluginID: "roundtrip-plugin", SourceID: "dev-rt",
				LocalName: "Round Trip Device",
				Labels:    map[string][]string{"zone": {"a", "b"}},
			},
		},
	}
	plugin.MustStart(t)
	t.Cleanup(plugin.Stop)
	plugin.seedRegistry(t)

	// Export.
	resp := h.get(t, "/api/export/devices.csv")
	assertStatus(t, resp, http.StatusOK)

	records := parseCSVResponse(t, resp)
	if len(records) < 2 {
		t.Fatalf("expected at least 2 rows in export, got %d", len(records))
	}

	// Find dev-rt in the export.
	col := csvColIndex(records[0])
	var exportedRow []string
	for _, row := range records[1:] {
		if csvCol(row, col, "device_id") == "dev-rt" {
			exportedRow = row
			break
		}
	}
	if exportedRow == nil {
		t.Fatal("dev-rt not found in export")
	}

	exportedLabels := csvCol(exportedRow, col, "labels")
	if exportedLabels != "zone:a|zone:b" {
		t.Errorf("exported labels: got %q, want %q", exportedLabels, "zone:a|zone:b")
	}

	// Modify the name in the exported CSV and import back.
	newName := "Renamed Device"
	importCSV := strings.Join([]string{
		"plugin_id,device_id,local_name,labels",
		"roundtrip-plugin,dev-rt," + newName + "," + exportedLabels,
	}, "\n")

	importResp := doCSVPost(t, h, "/api/import/devices.csv", importCSV)
	assertStatus(t, importResp, http.StatusOK)

	var result csvImportResult
	if err := json.NewDecoder(importResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	importResp.Body.Close()

	if result.Updated != 1 {
		t.Errorf("updated: got %d, want 1 (errors: %v)", result.Updated, result.Errors)
	}

	plugin.mu.RLock()
	defer plugin.mu.RUnlock()
	for _, d := range plugin.Devices {
		if d.ID == "dev-rt" && d.LocalName != newName {
			t.Errorf("after import: local_name = %q, want %q", d.LocalName, newName)
		}
	}
}
