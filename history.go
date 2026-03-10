package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/slidebolt/sdk-types"
)

type historyStats struct {
	EventCount   int64 `json:"event_count"`
	CommandCount int64 `json:"command_count"`
}

type pluginRate struct {
	PluginID       string  `json:"plugin_id"`
	EventCount     int64   `json:"event_count"`
	CommandCount   int64   `json:"command_count"`
	WindowSeconds  int     `json:"window_seconds"`
	EventsPerSec   float64 `json:"events_per_sec"`
	CommandsPerSec float64 `json:"commands_per_sec"`
	TotalPerSec    float64 `json:"total_per_sec"`
}

type deviceRate struct {
	PluginID       string  `json:"plugin_id"`
	DeviceID       string  `json:"device_id"`
	EventCount     int64   `json:"event_count"`
	CommandCount   int64   `json:"command_count"`
	WindowSeconds  int     `json:"window_seconds"`
	EventsPerSec   float64 `json:"events_per_sec"`
	CommandsPerSec float64 `json:"commands_per_sec"`
	TotalPerSec    float64 `json:"total_per_sec"`
}

type entityRate struct {
	PluginID       string  `json:"plugin_id"`
	DeviceID       string  `json:"device_id"`
	EntityID       string  `json:"entity_id"`
	EventCount     int64   `json:"event_count"`
	CommandCount   int64   `json:"command_count"`
	WindowSeconds  int     `json:"window_seconds"`
	EventsPerSec   float64 `json:"events_per_sec"`
	CommandsPerSec float64 `json:"commands_per_sec"`
	TotalPerSec    float64 `json:"total_per_sec"`
}

type historyStore struct {
	db *sql.DB
}

func openHistoryStore(path string) (*historyStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite connection-level PRAGMAs (busy_timeout, journal mode) are not
	// consistently applied across a pool of multiple connections.
	// Keep a single shared connection to avoid transient lock/drop behavior
	// under concurrent readers/writers.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, err
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS history_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			stream_seq INTEGER NOT NULL UNIQUE,
			name TEXT NOT NULL,
			plugin_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_history_events_filters
			ON history_events (plugin_id, device_id, entity_id, created_at DESC, id DESC);`,
		`CREATE TABLE IF NOT EXISTS history_command_status (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			stream_seq INTEGER NOT NULL UNIQUE,
			command_id TEXT NOT NULL,
			plugin_id TEXT NOT NULL,
			device_id TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_updated_at TEXT NOT NULL,
			payload_json TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_history_command_lookup
			ON history_command_status (command_id, stream_seq DESC);`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	migrations := []string{
		// Migrations: add columns if not present (idempotent — SQLite ignores duplicate column errors).
		`ALTER TABLE history_events ADD COLUMN payload_json TEXT`,
		`ALTER TABLE history_events ADD COLUMN entity_type TEXT NOT NULL DEFAULT ''`,
		// Stores the original command payload (body sent by the caller) keyed by command_id.
		`CREATE TABLE IF NOT EXISTS history_command_payloads (
			command_id   TEXT PRIMARY KEY,
			payload_json TEXT NOT NULL
		)`,
	}
	for _, stmt := range migrations {
		_, _ = db.Exec(stmt)
	}

	return &historyStore{db: db}, nil
}

func (h *historyStore) Close() error {
	if h == nil || h.db == nil {
		return nil
	}
	return h.db.Close()
}

func (h *historyStore) InsertEvent(streamSeq uint64, ts time.Time, env types.EntityEventEnvelope) error {
	if h == nil {
		return nil
	}
	payloadStr := ""
	if len(env.Payload) > 0 {
		payloadStr = string(env.Payload)
	}
	_, err := h.db.Exec(
		`INSERT OR IGNORE INTO history_events
		(stream_seq, name, plugin_id, device_id, entity_id, entity_type, event_id, created_at, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		streamSeq,
		classifyEventName(env.EntityType, env.Payload, false),
		env.PluginID,
		env.DeviceID,
		env.EntityID,
		env.EntityType,
		env.EventID,
		ts.UTC().Format(time.RFC3339Nano),
		payloadStr,
	)
	return err
}

func (h *historyStore) InsertCommandStatus(streamSeq uint64, status types.CommandStatus) error {
	if h == nil {
		return nil
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(
		`INSERT OR IGNORE INTO history_command_status
		(stream_seq, command_id, plugin_id, device_id, entity_id, state, created_at, last_updated_at, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		streamSeq,
		status.CommandID,
		status.PluginID,
		status.DeviceID,
		status.EntityID,
		string(status.State),
		status.CreatedAt.UTC().Format(time.RFC3339Nano),
		status.LastUpdatedAt.UTC().Format(time.RFC3339Nano),
		string(raw),
	)
	return err
}

// StoreCommandPayload records the original command body for a given command_id.
// Called at dispatch time so TraceSince can show the real Payload.type.
func (h *historyStore) StoreCommandPayload(commandID string, payload json.RawMessage) error {
	if h == nil || len(payload) == 0 {
		return nil
	}
	_, err := h.db.Exec(
		`INSERT OR IGNORE INTO history_command_payloads (command_id, payload_json) VALUES (?, ?)`,
		commandID, string(payload),
	)
	return err
}

func (h *historyStore) LatestCommandStatus(commandID string) (types.CommandStatus, bool, error) {
	var raw string
	err := h.db.QueryRow(
		`SELECT payload_json
		 FROM history_command_status
		 WHERE command_id = ?
		 ORDER BY stream_seq DESC
		 LIMIT 1`,
		commandID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return types.CommandStatus{}, false, nil
	}
	if err != nil {
		return types.CommandStatus{}, false, err
	}
	var out types.CommandStatus
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return types.CommandStatus{}, false, fmt.Errorf("decode command status: %w", err)
	}
	return out, true, nil
}

func (h *historyStore) ListEvents(pluginID, deviceID, entityID string, limit int) ([]observedEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := h.db.Query(
		`SELECT name, plugin_id, device_id, entity_id, event_id, created_at
		 FROM history_events
		 WHERE (? = '' OR plugin_id = ?)
		   AND (? = '' OR device_id = ?)
		   AND (? = '' OR entity_id = ?)
		 ORDER BY created_at DESC, id DESC
		 LIMIT ?`,
		pluginID, pluginID, deviceID, deviceID, entityID, entityID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]observedEvent, 0)
	for rows.Next() {
		var evt observedEvent
		var createdAt string
		if err := rows.Scan(&evt.Name, &evt.PluginID, &evt.DeviceID, &evt.EntityID, &evt.EventID, &createdAt); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			evt.CreatedAt = t.UTC()
		} else {
			evt.CreatedAt = time.Now().UTC()
		}
		out = append(out, evt)
	}
	return out, rows.Err()
}

func (h *historyStore) Stats() (historyStats, error) {
	var stats historyStats
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM history_events`).Scan(&stats.EventCount); err != nil {
		return stats, err
	}
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM history_command_status`).Scan(&stats.CommandCount); err != nil {
		return stats, err
	}
	return stats, nil
}

func (h *historyStore) PluginRates(windowSeconds int) ([]pluginRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)

	rates := make(map[string]*pluginRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, COUNT(1)
		 FROM history_events
		 WHERE created_at >= ?
		 GROUP BY plugin_id`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	for eventRows.Next() {
		var pluginID string
		var count int64
		if err := eventRows.Scan(&pluginID, &count); err != nil {
			eventRows.Close()
			return nil, err
		}
		if rates[pluginID] == nil {
			rates[pluginID] = &pluginRate{PluginID: pluginID}
		}
		rates[pluginID].EventCount = count
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, err
	}
	eventRows.Close()

	commandRows, err := h.db.Query(
		`SELECT plugin_id, COUNT(DISTINCT command_id)
		 FROM history_command_status
		 WHERE created_at >= ?
		 GROUP BY plugin_id`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	for commandRows.Next() {
		var pluginID string
		var count int64
		if err := commandRows.Scan(&pluginID, &count); err != nil {
			commandRows.Close()
			return nil, err
		}
		if rates[pluginID] == nil {
			rates[pluginID] = &pluginRate{PluginID: pluginID}
		}
		rates[pluginID].CommandCount = count
	}
	if err := commandRows.Err(); err != nil {
		commandRows.Close()
		return nil, err
	}
	commandRows.Close()

	out := make([]pluginRate, 0, len(rates))
	windowF := float64(windowSeconds)
	for _, r := range rates {
		r.WindowSeconds = windowSeconds
		r.EventsPerSec = float64(r.EventCount) / windowF
		r.CommandsPerSec = float64(r.CommandCount) / windowF
		r.TotalPerSec = r.EventsPerSec + r.CommandsPerSec
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalPerSec == out[j].TotalPerSec {
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].TotalPerSec > out[j].TotalPerSec
	})
	return out, nil
}

func (h *historyStore) DeviceRates(pluginID string, windowSeconds int) ([]deviceRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)

	type key struct {
		pluginID string
		deviceID string
	}
	rates := make(map[key]*deviceRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, device_id, COUNT(1)
		 FROM history_events
		 WHERE created_at >= ?
		   AND (? = '' OR plugin_id = ?)
		 GROUP BY plugin_id, device_id`,
		cutoff, pluginID, pluginID,
	)
	if err != nil {
		return nil, err
	}
	for eventRows.Next() {
		var pID, dID string
		var count int64
		if err := eventRows.Scan(&pID, &dID, &count); err != nil {
			eventRows.Close()
			return nil, err
		}
		k := key{pluginID: pID, deviceID: dID}
		if rates[k] == nil {
			rates[k] = &deviceRate{PluginID: pID, DeviceID: dID}
		}
		rates[k].EventCount = count
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, err
	}
	eventRows.Close()

	commandRows, err := h.db.Query(
		`SELECT plugin_id, device_id, COUNT(DISTINCT command_id)
		 FROM history_command_status
		 WHERE created_at >= ?
		   AND (? = '' OR plugin_id = ?)
		 GROUP BY plugin_id, device_id`,
		cutoff, pluginID, pluginID,
	)
	if err != nil {
		return nil, err
	}
	for commandRows.Next() {
		var pID, dID string
		var count int64
		if err := commandRows.Scan(&pID, &dID, &count); err != nil {
			commandRows.Close()
			return nil, err
		}
		k := key{pluginID: pID, deviceID: dID}
		if rates[k] == nil {
			rates[k] = &deviceRate{PluginID: pID, DeviceID: dID}
		}
		rates[k].CommandCount = count
	}
	if err := commandRows.Err(); err != nil {
		commandRows.Close()
		return nil, err
	}
	commandRows.Close()

	out := make([]deviceRate, 0, len(rates))
	windowF := float64(windowSeconds)
	for _, r := range rates {
		r.WindowSeconds = windowSeconds
		r.EventsPerSec = float64(r.EventCount) / windowF
		r.CommandsPerSec = float64(r.CommandCount) / windowF
		r.TotalPerSec = r.EventsPerSec + r.CommandsPerSec
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalPerSec == out[j].TotalPerSec {
			if out[i].PluginID == out[j].PluginID {
				return out[i].DeviceID < out[j].DeviceID
			}
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].TotalPerSec > out[j].TotalPerSec
	})
	return out, nil
}

func (h *historyStore) EntityRates(pluginID, deviceID string, windowSeconds int) ([]entityRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)

	type key struct {
		pluginID string
		deviceID string
		entityID string
	}
	rates := make(map[key]*entityRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, device_id, entity_id, COUNT(1)
		 FROM history_events
		 WHERE created_at >= ?
		   AND (? = '' OR plugin_id = ?)
		   AND (? = '' OR device_id = ?)
		 GROUP BY plugin_id, device_id, entity_id`,
		cutoff, pluginID, pluginID, deviceID, deviceID,
	)
	if err != nil {
		return nil, err
	}
	for eventRows.Next() {
		var pID, dID, eID string
		var count int64
		if err := eventRows.Scan(&pID, &dID, &eID, &count); err != nil {
			eventRows.Close()
			return nil, err
		}
		k := key{pluginID: pID, deviceID: dID, entityID: eID}
		if rates[k] == nil {
			rates[k] = &entityRate{PluginID: pID, DeviceID: dID, EntityID: eID}
		}
		rates[k].EventCount = count
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return nil, err
	}
	eventRows.Close()

	commandRows, err := h.db.Query(
		`SELECT plugin_id, device_id, entity_id, COUNT(DISTINCT command_id)
		 FROM history_command_status
		 WHERE created_at >= ?
		   AND (? = '' OR plugin_id = ?)
		   AND (? = '' OR device_id = ?)
		 GROUP BY plugin_id, device_id, entity_id`,
		cutoff, pluginID, pluginID, deviceID, deviceID,
	)
	if err != nil {
		return nil, err
	}
	for commandRows.Next() {
		var pID, dID, eID string
		var count int64
		if err := commandRows.Scan(&pID, &dID, &eID, &count); err != nil {
			commandRows.Close()
			return nil, err
		}
		k := key{pluginID: pID, deviceID: dID, entityID: eID}
		if rates[k] == nil {
			rates[k] = &entityRate{PluginID: pID, DeviceID: dID, EntityID: eID}
		}
		rates[k].CommandCount = count
	}
	if err := commandRows.Err(); err != nil {
		commandRows.Close()
		return nil, err
	}
	commandRows.Close()

	out := make([]entityRate, 0, len(rates))
	windowF := float64(windowSeconds)
	for _, r := range rates {
		r.WindowSeconds = windowSeconds
		r.EventsPerSec = float64(r.EventCount) / windowF
		r.CommandsPerSec = float64(r.CommandCount) / windowF
		r.TotalPerSec = r.EventsPerSec + r.CommandsPerSec
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalPerSec == out[j].TotalPerSec {
			if out[i].PluginID == out[j].PluginID {
				if out[i].DeviceID == out[j].DeviceID {
					return out[i].EntityID < out[j].EntityID
				}
				return out[i].DeviceID < out[j].DeviceID
			}
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].TotalPerSec > out[j].TotalPerSec
	})
	return out, nil
}

// traceEntry is a unified event-or-command record returned by TraceSince.
// Name is EventRef.Type for events and Payload.type for commands — the exact
// strings needed to write Ctx:OnEvent / Ctx:SendCommand in Lua.
// EventKey is the Ctx:OnEvent subscription key: "{plugin_id}.{device_id}.{entity_id}".
type traceEntry struct {
	Kind     string          `json:"kind"`               // "event" or "command"
	Ts       time.Time       `json:"ts"`
	Name     string          `json:"name"`               // EventRef.Type / Payload.type
	EventKey string          `json:"event_key,omitempty"` // Ctx:OnEvent key (events only)
	State    string          `json:"state,omitempty"`    // command state
	Error    string          `json:"error,omitempty"`    // command error
	Data     json.RawMessage `json:"data,omitempty"`
}

// TraceSince returns events and commands for the given entity that occurred
// strictly after since, sorted chronologically. For commands, only the latest
// state per command_id is returned.
func (h *historyStore) TraceSince(pluginID, deviceID, entityID string, since time.Time) ([]traceEntry, error) {
	if h == nil {
		return []traceEntry{}, nil
	}
	sinceStr := since.UTC().Format(time.RFC3339Nano)

	var entries []traceEntry

	eventKey := pluginID + "." + deviceID + "." + entityID

	// ── Events ──────────────────────────────────────────────────────────────────
	eventRows, err := h.db.Query(
		`SELECT created_at, COALESCE(payload_json, '')
		 FROM history_events
		 WHERE plugin_id = ? AND device_id = ? AND entity_id = ?
		   AND created_at > ?
		 ORDER BY created_at ASC, id ASC
		 LIMIT 500`,
		pluginID, deviceID, entityID, sinceStr,
	)
	if err != nil {
		return nil, err
	}
	defer eventRows.Close()

	for eventRows.Next() {
		var createdAt, payload string
		if err := eventRows.Scan(&createdAt, &payload); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, createdAt)
		// Name = EventRef.Type (the payload "type" field — exactly what Lua sees).
		name := ""
		if payload != "" {
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(payload), &p) == nil {
				name = p.Type
			}
		}
		e := traceEntry{Kind: "event", Ts: t.UTC(), Name: name, EventKey: eventKey}
		if payload != "" {
			e.Data = json.RawMessage(payload)
		}
		entries = append(entries, e)
	}
	if err := eventRows.Err(); err != nil {
		return nil, err
	}

	// ── Commands ─────────────────────────────────────────────────────────────────
	// One row per command_id (latest state), joined with the original command payload.
	cmdRows, err := h.db.Query(
		`SELECT hcs.payload_json, hcs.created_at, COALESCE(hcp.payload_json, '')
		 FROM history_command_status hcs
		 LEFT JOIN history_command_payloads hcp ON hcs.command_id = hcp.command_id
		 WHERE hcs.plugin_id = ? AND hcs.device_id = ? AND hcs.entity_id = ?
		   AND hcs.created_at > ?
		 GROUP BY hcs.command_id
		 HAVING hcs.stream_seq = MAX(hcs.stream_seq)
		 ORDER BY hcs.created_at ASC`,
		pluginID, deviceID, entityID, sinceStr,
	)
	if err != nil {
		return nil, err
	}
	defer cmdRows.Close()

	for cmdRows.Next() {
		var statusJSON, createdAt, cmdPayload string
		if err := cmdRows.Scan(&statusJSON, &createdAt, &cmdPayload); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, createdAt)
		var status types.CommandStatus
		if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
			continue
		}
		// Name = Payload.type from the original command body (what Lua passes to SendCommand).
		name := ""
		if cmdPayload != "" {
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(cmdPayload), &p) == nil {
				name = p.Type
			}
		}
		e := traceEntry{
			Kind:  "command",
			Ts:    t.UTC(),
			Name:  name,
			State: string(status.State),
			Error: status.Error,
		}
		if cmdPayload != "" {
			e.Data = json.RawMessage(cmdPayload)
		}
		entries = append(entries, e)
	}
	if err := cmdRows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Ts.Before(entries[j].Ts)
	})

	if entries == nil {
		return []traceEntry{}, nil
	}
	return entries, nil
}
