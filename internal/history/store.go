package history

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"modernc.org/sqlite"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

// History is the top-level handle for the history subsystem: SQLite store,
// JetStream consumers, SSE broker, and HTTP routes.
type History struct {
	db     *sql.DB
	broker *sseBroker
}

type Stats struct {
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

// traceEntry is a unified event-or-command record returned by TraceSince.
type traceEntry struct {
	Kind     string          `json:"kind"` // "event" or "command"
	Ts       time.Time       `json:"ts"`
	Name     string          `json:"name"`
	EventKey string          `json:"event_key,omitempty"`
	State    string          `json:"state,omitempty"`
	Error    string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type observedEvent struct {
	Name      string    `json:"name"`
	PluginID  string    `json:"plugin_id"`
	DeviceID  string    `json:"device_id"`
	EntityID  string    `json:"entity_id"`
	EventID   string    `json:"event_id"`
	CreatedAt time.Time `json:"created_at"`
}

// sqliteConnector opens SQLite connections with PRAGMAs applied per-connection.
type sqliteConnector struct{ path string }

func (c *sqliteConnector) Connect(_ context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(c.path)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=10000",
	} {
		st, err := conn.Prepare(p)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("sqlite pragma %q: %w", p, err)
		}
		rows, err := st.Query(nil)
		if err == nil {
			if cerr := rows.Close(); cerr != nil {
				log.Printf("history: rows.Close error in sqliteConnector.Connect: %v", cerr)
			}
		}
		_ = st.Close()
	}
	return conn, nil
}

func (c *sqliteConnector) Driver() driver.Driver { return &sqlite.Driver{} }

// Open opens the SQLite history store at the given path.
func Open(path string) (*History, error) {
	db := sql.OpenDB(&sqliteConnector{path: path})
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(0)

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
		`ALTER TABLE history_events ADD COLUMN payload_json TEXT`,
		`ALTER TABLE history_events ADD COLUMN entity_type TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS history_command_payloads (
			command_id   TEXT PRIMARY KEY,
			payload_json TEXT NOT NULL
		)`,
	}
	for _, stmt := range migrations {
		_, _ = db.Exec(stmt)
	}

	return &History{db: db, broker: newSSEBroker()}, nil
}

// Start subscribes to NATS entity events and launches JetStream consumers.
func (h *History) Start(ctx context.Context, nc *nats.Conn, js nats.JetStreamContext) {
	h.subscribeEntityEvents(nc)
	go h.consumeEvents(ctx, js)
	go h.consumeCommands(ctx, js)
}

func (h *History) Close() error {
	if h == nil || h.db == nil {
		return nil
	}
	return h.db.Close()
}

func (h *History) Prune() error {
	if h == nil || h.db == nil {
		return nil
	}
	tables := []string{"history_events", "history_command_status", "history_command_payloads"}
	for _, table := range tables {
		if _, err := h.db.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	_, err := h.db.Exec("VACUUM")
	return err
}

func (h *History) insertEvent(streamSeq uint64, ts time.Time, env types.EntityEventEnvelope) error {
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
		classifyEventName(),
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

func (h *History) insertCommandStatus(streamSeq uint64, status types.CommandStatus) error {
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

func (h *History) latestCommandStatus(commandID string) (types.CommandStatus, bool, error) {
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

func (h *History) listEvents(pluginID, deviceID, entityID string, limit int) ([]observedEvent, error) {
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
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("history: rows.Close error in listEvents: %v", cerr)
		}
	}()

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

func (h *History) stats() (Stats, error) {
	var s Stats
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM history_events`).Scan(&s.EventCount); err != nil {
		return s, err
	}
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM history_command_status`).Scan(&s.CommandCount); err != nil {
		return s, err
	}
	return s, nil
}

func (h *History) pluginRates(windowSeconds int) ([]pluginRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)
	rates := make(map[string]*pluginRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, COUNT(1) FROM history_events WHERE created_at >= ? GROUP BY plugin_id`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	for eventRows.Next() {
		var pluginID string
		var count int64
		if err := eventRows.Scan(&pluginID, &count); err != nil {
			if cerr := eventRows.Close(); cerr != nil {
				log.Printf("history: eventRows.Close error in pluginRates: %v", cerr)
			}
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
		`SELECT plugin_id, COUNT(DISTINCT command_id) FROM history_command_status WHERE created_at >= ? GROUP BY plugin_id`,
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

func (h *History) deviceRates(pluginID string, windowSeconds int) ([]deviceRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)

	type key struct{ pluginID, deviceID string }
	rates := make(map[key]*deviceRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, device_id, COUNT(1) FROM history_events
		 WHERE created_at >= ? AND (? = '' OR plugin_id = ?) GROUP BY plugin_id, device_id`,
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
		k := key{pID, dID}
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
		`SELECT plugin_id, device_id, COUNT(DISTINCT command_id) FROM history_command_status
		 WHERE created_at >= ? AND (? = '' OR plugin_id = ?) GROUP BY plugin_id, device_id`,
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
		k := key{pID, dID}
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

func (h *History) entityRates(pluginID, deviceID string, windowSeconds int) ([]entityRate, error) {
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	cutoff := time.Now().UTC().Add(-time.Duration(windowSeconds) * time.Second).Format(time.RFC3339Nano)

	type key struct{ pluginID, deviceID, entityID string }
	rates := make(map[key]*entityRate)

	eventRows, err := h.db.Query(
		`SELECT plugin_id, device_id, entity_id, COUNT(1) FROM history_events
		 WHERE created_at >= ? AND (? = '' OR plugin_id = ?) AND (? = '' OR device_id = ?)
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
		k := key{pID, dID, eID}
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
		`SELECT plugin_id, device_id, entity_id, COUNT(DISTINCT command_id) FROM history_command_status
		 WHERE created_at >= ? AND (? = '' OR plugin_id = ?) AND (? = '' OR device_id = ?)
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
		k := key{pID, dID, eID}
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

func (h *History) traceSince(pluginID, deviceID, entityID string, since time.Time) ([]traceEntry, error) {
	if h == nil {
		return []traceEntry{}, nil
	}
	sinceStr := since.UTC().Format(time.RFC3339Nano)
	eventKey := pluginID + "." + deviceID + "." + entityID

	var entries []traceEntry

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
		name := ""
		if cmdPayload != "" {
			var p struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(cmdPayload), &p) == nil {
				name = p.Type
			}
		}
		e := traceEntry{Kind: "command", Ts: t.UTC(), Name: name, State: string(status.State), Error: status.Error}
		if cmdPayload != "" {
			e.Data = json.RawMessage(cmdPayload)
		}
		entries = append(entries, e)
	}
	if err := cmdRows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Ts.Before(entries[j].Ts) })

	if entries == nil {
		return []traceEntry{}, nil
	}
	return entries, nil
}
