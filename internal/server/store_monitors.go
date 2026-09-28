package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"probe-platform/internal/protocol"
)

const monitorSchema = `
CREATE TABLE IF NOT EXISTS monitors (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	type         TEXT NOT NULL,
	target       TEXT NOT NULL,
	params       TEXT NOT NULL DEFAULT '{}',
	agent_ids    TEXT NOT NULL DEFAULT '[]',
	interval_sec INTEGER NOT NULL,
	enabled      INTEGER NOT NULL DEFAULT 1,
	alert        TEXT NOT NULL DEFAULT '{}',
	notify_ids   TEXT NOT NULL DEFAULT '[]',
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	last_run_at  INTEGER,
	next_run_at  INTEGER
);
CREATE TABLE IF NOT EXISTS samples (
	monitor_id      TEXT NOT NULL,
	agent_id        TEXT NOT NULL,
	at              INTEGER NOT NULL,
	task_id         TEXT NOT NULL DEFAULT '',
	ok              INTEGER NOT NULL,
	error           TEXT NOT NULL DEFAULT '',
	latency_ms      REAL NOT NULL DEFAULT -1,
	loss_pct        REAL NOT NULL DEFAULT 0,
	status_code     INTEGER NOT NULL DEFAULT 0,
	reached         INTEGER NOT NULL DEFAULT 0,
	throughput_mbps REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, agent_id, at)
);
CREATE INDEX IF NOT EXISTS idx_samples_at ON samples(at);
CREATE TABLE IF NOT EXISTS monitor_state (
	monitor_id TEXT NOT NULL,
	agent_id   TEXT NOT NULL,
	failing    INTEGER NOT NULL DEFAULT 0,
	alerting   INTEGER NOT NULL DEFAULT 0,
	since      INTEGER,
	last_error TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (monitor_id, agent_id)
);
CREATE TABLE IF NOT EXISTS alert_events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id TEXT NOT NULL,
	agent_id   TEXT NOT NULL,
	kind       TEXT NOT NULL,
	message    TEXT NOT NULL,
	at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alerts_monitor ON alert_events(monitor_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_at ON alert_events(at DESC);
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notify_channels (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	type       TEXT NOT NULL,
	config     TEXT NOT NULL DEFAULT '{}',
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL
);
`

// migrateMonitors creates the monitoring tables and adds tasks.monitor_id.
func (s *Store) migrateMonitors() error {
	if _, err := s.db.Exec(monitorSchema); err != nil {
		return err
	}
	rows, err := s.db.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	has := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "monitor_id" {
			has = true
		}
	}
	if !has {
		if _, err := s.db.Exec(`ALTER TABLE tasks ADD COLUMN monitor_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_monitor ON tasks(monitor_id, created_at DESC)`); err != nil {
			return err
		}
	}
	return nil
}

func nullableMs(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func msPtr(v sql.NullInt64) *time.Time {
	if !v.Valid || v.Int64 == 0 {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

// --- monitors ---------------------------------------------------------------

const monitorCols = `id,name,type,target,params,agent_ids,interval_sec,enabled,alert,notify_ids,created_at,updated_at,last_run_at,next_run_at`

func scanMonitor(row interface{ Scan(...any) error }) (*protocol.Monitor, error) {
	var m protocol.Monitor
	var typ, params, agents, alert, notify string
	var enabled int
	var created, updated int64
	var last, next sql.NullInt64
	if err := row.Scan(&m.ID, &m.Name, &typ, &m.Target, &params, &agents, &m.IntervalSec, &enabled, &alert, &notify, &created, &updated, &last, &next); err != nil {
		return nil, err
	}
	m.Type = protocol.TaskType(typ)
	m.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(params), &m.Params)
	_ = json.Unmarshal([]byte(agents), &m.AgentIDs)
	_ = json.Unmarshal([]byte(alert), &m.Alert)
	_ = json.Unmarshal([]byte(notify), &m.NotifyIDs)
	if m.AgentIDs == nil {
		m.AgentIDs = []string{}
	}
	if m.NotifyIDs == nil {
		m.NotifyIDs = []string{}
	}
	m.CreatedAt, m.UpdatedAt = fromMs(created), fromMs(updated)
	m.LastRunAt, m.NextRunAt = msPtr(last), msPtr(next)
	return &m, nil
}

// UpsertMonitor inserts or fully updates a monitor.
func (s *Store) UpsertMonitor(m *protocol.Monitor) error {
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	_, err := s.db.Exec(`
INSERT INTO monitors (`+monitorCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	name=excluded.name, type=excluded.type, target=excluded.target, params=excluded.params,
	agent_ids=excluded.agent_ids, interval_sec=excluded.interval_sec, enabled=excluded.enabled,
	alert=excluded.alert, notify_ids=excluded.notify_ids, updated_at=excluded.updated_at,
	next_run_at=excluded.next_run_at`,
		m.ID, m.Name, string(m.Type), m.Target, jsonStr(m.Params), jsonStr(nonNil(m.AgentIDs)), m.IntervalSec, boolInt(m.Enabled),
		jsonStr(m.Alert), jsonStr(nonNil(m.NotifyIDs)), unixMs(m.CreatedAt), unixMs(m.UpdatedAt), nullableMs(m.LastRunAt), nullableMs(m.NextRunAt))
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetMonitor returns one monitor or ErrNotFound.
func (s *Store) GetMonitor(id string) (*protocol.Monitor, error) {
	m, err := scanMonitor(s.db.QueryRow(`SELECT `+monitorCols+` FROM monitors WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// ListMonitors returns every monitor ordered by name.
func (s *Store) ListMonitors() ([]*protocol.Monitor, error) {
	rows, err := s.db.Query(`SELECT ` + monitorCols + ` FROM monitors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*protocol.Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DueMonitors returns enabled monitors whose next run is at or before now.
func (s *Store) DueMonitors(now time.Time) ([]*protocol.Monitor, error) {
	rows, err := s.db.Query(`SELECT `+monitorCols+` FROM monitors WHERE enabled=1 AND (next_run_at IS NULL OR next_run_at<=?) ORDER BY next_run_at`, unixMs(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*protocol.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkMonitorRun records a run and schedules the next one.
func (s *Store) MarkMonitorRun(id string, ranAt, next time.Time) error {
	_, err := s.db.Exec(`UPDATE monitors SET last_run_at=?, next_run_at=? WHERE id=?`, unixMs(ranAt), unixMs(next), id)
	return err
}

// DeleteMonitor removes a monitor and everything derived from it.
func (s *Store) DeleteMonitor(id string) error {
	for _, q := range []string{
		`DELETE FROM samples WHERE monitor_id=?`,
		`DELETE FROM monitor_state WHERE monitor_id=?`,
		`DELETE FROM alert_events WHERE monitor_id=?`,
		`DELETE FROM results WHERE task_id IN (SELECT id FROM tasks WHERE monitor_id=?)`,
		`DELETE FROM tasks WHERE monitor_id=?`,
		`DELETE FROM monitors WHERE id=?`,
	} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// --- samples ----------------------------------------------------------------

// InsertSample stores one summarised run.
func (s *Store) InsertSample(sm *protocol.Sample) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO samples (monitor_id,agent_id,at,task_id,ok,error,latency_ms,loss_pct,status_code,reached,throughput_mbps)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		sm.MonitorID, sm.AgentID, unixMs(sm.At), sm.TaskID, boolInt(sm.OK), sm.Error, sm.LatencyMs, sm.LossPct, sm.StatusCode, boolInt(sm.Reached), sm.ThroughputMbps)
	return err
}

func scanSample(row interface{ Scan(...any) error }) (*protocol.Sample, error) {
	var sm protocol.Sample
	var at int64
	var ok, reached int
	if err := row.Scan(&sm.MonitorID, &sm.AgentID, &at, &sm.TaskID, &ok, &sm.Error, &sm.LatencyMs, &sm.LossPct, &sm.StatusCode, &reached, &sm.ThroughputMbps); err != nil {
		return nil, err
	}
	sm.At, sm.OK, sm.Reached = fromMs(at), ok != 0, reached != 0
	return &sm, nil
}

const sampleCols = `monitor_id,agent_id,at,task_id,ok,error,latency_ms,loss_pct,status_code,reached,throughput_mbps`

// LatestSamples returns the most recent sample per agent for a monitor.
func (s *Store) LatestSamples(monitorID string) (map[string]*protocol.Sample, error) {
	rows, err := s.db.Query(`SELECT `+sampleCols+` FROM samples WHERE monitor_id=? AND at IN (SELECT MAX(at) FROM samples WHERE monitor_id=? GROUP BY agent_id)`, monitorID, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*protocol.Sample{}
	for rows.Next() {
		sm, err := scanSample(rows)
		if err != nil {
			return nil, err
		}
		out[sm.AgentID] = sm
	}
	return out, rows.Err()
}

// Series returns per-agent time series between from and to. bucket <= 0
// returns raw samples; otherwise samples are averaged per bucket.
func (s *Store) Series(monitorID string, from, to time.Time, bucket time.Duration) ([]*protocol.Series, error) {
	var rows *sql.Rows
	var err error
	if bucket <= 0 {
		rows, err = s.db.Query(`SELECT agent_id, at, latency_ms, loss_pct, ok, 1 FROM samples
			WHERE monitor_id=? AND at>=? AND at<=? ORDER BY agent_id, at`, monitorID, unixMs(from), unixMs(to))
	} else {
		b := bucket.Milliseconds()
		rows, err = s.db.Query(`SELECT agent_id, (at/?)*? AS b,
			COALESCE(AVG(CASE WHEN latency_ms>=0 THEN latency_ms END), -1), AVG(loss_pct), AVG(ok), COUNT(*)
			FROM samples WHERE monitor_id=? AND at>=? AND at<=? GROUP BY agent_id, b ORDER BY agent_id, b`,
			b, b, monitorID, unixMs(from), unixMs(to))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*protocol.Series
	var cur *protocol.Series
	for rows.Next() {
		var agent string
		var t int64
		var lat, loss, okRatio float64
		var n int
		if err := rows.Scan(&agent, &t, &lat, &loss, &okRatio, &n); err != nil {
			return nil, err
		}
		if cur == nil || cur.AgentID != agent {
			cur = &protocol.Series{AgentID: agent, Points: []protocol.SeriesPoint{}}
			out = append(out, cur)
		}
		cur.Points = append(cur.Points, protocol.SeriesPoint{T: t, LatencyMs: round3(lat), LossPct: round3(loss), OKRatio: okRatio, Count: n})
	}
	return out, rows.Err()
}

func round3(f float64) float64 { return float64(int64(f*1000+0.5)) / 1000 }

// PruneSamples deletes samples older than cutoff.
func (s *Store) PruneSamples(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM samples WHERE at<?`, unixMs(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneMonitorTasks deletes monitor-generated tasks older than cutoff (their
// samples are kept; only the bulky per-run details go).
func (s *Store) PruneMonitorTasks(cutoff time.Time) (int64, error) {
	if _, err := s.db.Exec(`DELETE FROM results WHERE task_id IN (SELECT id FROM tasks WHERE monitor_id!='' AND created_at<?)`, unixMs(cutoff)); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM tasks WHERE monitor_id!='' AND created_at<?`, unixMs(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- state & alerts ---------------------------------------------------------

// GetState loads the alert state for (monitor, agent); zero value when absent.
func (s *Store) GetState(monitorID, agentID string) (*protocol.MonitorAgentState, error) {
	st := &protocol.MonitorAgentState{MonitorID: monitorID, AgentID: agentID}
	var alerting int
	var since sql.NullInt64
	err := s.db.QueryRow(`SELECT failing, alerting, since, last_error FROM monitor_state WHERE monitor_id=? AND agent_id=?`, monitorID, agentID).
		Scan(&st.Failing, &alerting, &since, &st.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	st.Alerting = alerting != 0
	st.Since = msPtr(since)
	return st, nil
}

// SaveState persists the alert state.
func (s *Store) SaveState(st *protocol.MonitorAgentState) error {
	_, err := s.db.Exec(`INSERT INTO monitor_state (monitor_id,agent_id,failing,alerting,since,last_error) VALUES (?,?,?,?,?,?)
		ON CONFLICT(monitor_id,agent_id) DO UPDATE SET failing=excluded.failing, alerting=excluded.alerting, since=excluded.since, last_error=excluded.last_error`,
		st.MonitorID, st.AgentID, st.Failing, boolInt(st.Alerting), nullableMs(st.Since), st.LastError)
	return err
}

// ListStates returns every agent state for a monitor.
func (s *Store) ListStates(monitorID string) ([]*protocol.MonitorAgentState, error) {
	rows, err := s.db.Query(`SELECT monitor_id, agent_id, failing, alerting, since, last_error FROM monitor_state WHERE monitor_id=?`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*protocol.MonitorAgentState
	for rows.Next() {
		st := &protocol.MonitorAgentState{}
		var alerting int
		var since sql.NullInt64
		if err := rows.Scan(&st.MonitorID, &st.AgentID, &st.Failing, &alerting, &since, &st.LastError); err != nil {
			return nil, err
		}
		st.Alerting = alerting != 0
		st.Since = msPtr(since)
		out = append(out, st)
	}
	return out, rows.Err()
}

// AlertingCount returns how many (monitor, agent) pairs are currently alerting.
func (s *Store) AlertingCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM monitor_state WHERE alerting=1`).Scan(&n)
	return n, err
}

// InsertAlertEvent records a down/up transition.
func (s *Store) InsertAlertEvent(ev *protocol.AlertEvent) error {
	res, err := s.db.Exec(`INSERT INTO alert_events (monitor_id,agent_id,kind,message,at) VALUES (?,?,?,?,?)`,
		ev.MonitorID, ev.AgentID, ev.Kind, ev.Message, unixMs(ev.At))
	if err != nil {
		return err
	}
	ev.ID, _ = res.LastInsertId()
	return nil
}

// ListAlertEvents returns recent events, optionally for one monitor.
func (s *Store) ListAlertEvents(monitorID string, limit int) ([]*protocol.AlertEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT e.id, e.monitor_id, COALESCE(m.name,''), e.agent_id, COALESCE(a.name, e.agent_id), e.kind, e.message, e.at
		FROM alert_events e LEFT JOIN monitors m ON m.id=e.monitor_id LEFT JOIN agents a ON a.id=e.agent_id`
	args := []any{}
	if monitorID != "" {
		q += ` WHERE e.monitor_id=?`
		args = append(args, monitorID)
	}
	q += ` ORDER BY e.at DESC, e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*protocol.AlertEvent{}
	for rows.Next() {
		ev := &protocol.AlertEvent{}
		var at int64
		if err := rows.Scan(&ev.ID, &ev.MonitorID, &ev.Monitor, &ev.AgentID, &ev.Agent, &ev.Kind, &ev.Message, &at); err != nil {
			return nil, err
		}
		ev.At = fromMs(at)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// --- notify channels --------------------------------------------------------

func scanChannel(row interface{ Scan(...any) error }) (*protocol.NotifyChannel, error) {
	var c protocol.NotifyChannel
	var cfg string
	var enabled int
	var created int64
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &cfg, &enabled, &created); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(cfg), &c.Config)
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	c.Enabled = enabled != 0
	c.CreatedAt = fromMs(created)
	return &c, nil
}

// UpsertChannel inserts or updates a notification channel.
func (s *Store) UpsertChannel(c *protocol.NotifyChannel) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO notify_channels (id,name,type,config,enabled,created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, config=excluded.config, enabled=excluded.enabled`,
		c.ID, c.Name, c.Type, jsonStr(c.Config), boolInt(c.Enabled), unixMs(c.CreatedAt))
	return err
}

// GetChannel returns one channel or ErrNotFound.
func (s *Store) GetChannel(id string) (*protocol.NotifyChannel, error) {
	c, err := scanChannel(s.db.QueryRow(`SELECT id,name,type,config,enabled,created_at FROM notify_channels WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListChannels returns every channel.
func (s *Store) ListChannels() ([]*protocol.NotifyChannel, error) {
	rows, err := s.db.Query(`SELECT id,name,type,config,enabled,created_at FROM notify_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*protocol.NotifyChannel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteChannel removes a channel and detaches it from monitors.
func (s *Store) DeleteChannel(id string) error {
	if _, err := s.db.Exec(`DELETE FROM notify_channels WHERE id=?`, id); err != nil {
		return err
	}
	monitors, err := s.ListMonitors()
	if err != nil {
		return err
	}
	for _, m := range monitors {
		kept := m.NotifyIDs[:0]
		changed := false
		for _, nid := range m.NotifyIDs {
			if nid == id {
				changed = true
				continue
			}
			kept = append(kept, nid)
		}
		if changed {
			m.NotifyIDs = kept
			if err := s.UpsertMonitor(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// AgentNames maps agent ids to display names for the given ids.
func (s *Store) AgentNames(ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(fmt.Sprintf(`SELECT id, name FROM agents WHERE id IN (%s)`, placeholders), args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			out[id] = name
		}
	}
	return out
}
