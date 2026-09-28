package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go sqlite driver

	"probe-platform/internal/protocol"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store persists agents, tasks and results in SQLite.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS agents (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	location      TEXT NOT NULL DEFAULT '',
	isp           TEXT NOT NULL DEFAULT '',
	tags          TEXT NOT NULL DEFAULT '[]',
	os            TEXT NOT NULL DEFAULT '',
	arch          TEXT NOT NULL DEFAULT '',
	version       TEXT NOT NULL DEFAULT '',
	public_ip     TEXT NOT NULL DEFAULT '',
	geo_location  TEXT NOT NULL DEFAULT '',
	geo_isp       TEXT NOT NULL DEFAULT '',
	capabilities  TEXT NOT NULL DEFAULT '[]',
	first_seen    INTEGER NOT NULL,
	last_seen     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tasks (
	id         TEXT PRIMARY KEY,
	type       TEXT NOT NULL,
	target     TEXT NOT NULL,
	params     TEXT NOT NULL DEFAULT '{}',
	agent_ids  TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at DESC);
CREATE TABLE IF NOT EXISTS results (
	task_id     TEXT NOT NULL,
	agent_id    TEXT NOT NULL,
	agent_name  TEXT NOT NULL DEFAULT '',
	location    TEXT NOT NULL DEFAULT '',
	isp         TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	error       TEXT NOT NULL DEFAULT '',
	duration_ms REAL NOT NULL DEFAULT 0,
	data        TEXT,
	started_at  INTEGER,
	finished_at INTEGER,
	PRIMARY KEY (task_id, agent_id)
);
`

// OpenStore opens (and migrates) the database at path.
func OpenStore(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func unixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// --- agents -----------------------------------------------------------------

// UpsertAgent inserts or updates an agent record. FirstSeen is preserved on
// update; LastSeen is set to a.LastSeen (or now).
func (s *Store) UpsertAgent(a *protocol.AgentStatus) error {
	now := time.Now()
	if a.LastSeen.IsZero() {
		a.LastSeen = now
	}
	if a.FirstSeen.IsZero() {
		a.FirstSeen = now
	}
	_, err := s.db.Exec(`
INSERT INTO agents (id,name,location,isp,tags,os,arch,version,public_ip,geo_location,geo_isp,capabilities,first_seen,last_seen)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	name=excluded.name, location=excluded.location, isp=excluded.isp, tags=excluded.tags,
	os=excluded.os, arch=excluded.arch, version=excluded.version, public_ip=excluded.public_ip,
	geo_location=CASE WHEN excluded.geo_location='' THEN agents.geo_location ELSE excluded.geo_location END,
	geo_isp=CASE WHEN excluded.geo_isp='' THEN agents.geo_isp ELSE excluded.geo_isp END,
	capabilities=excluded.capabilities, last_seen=excluded.last_seen`,
		a.ID, a.Name, a.Location, a.ISP, jsonStr(nonNil(a.Tags)), a.OS, a.Arch, a.Version, a.PublicIP,
		a.GeoLocation, a.GeoISP, jsonStr(nonNil(a.Capabilities)), unixMs(a.FirstSeen), unixMs(a.LastSeen))
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TouchAgent updates last_seen.
func (s *Store) TouchAgent(id string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE agents SET last_seen=? WHERE id=?`, unixMs(t), id)
	return err
}

const agentCols = `id,name,location,isp,tags,os,arch,version,public_ip,geo_location,geo_isp,capabilities,first_seen,last_seen`

func scanAgent(row interface{ Scan(...any) error }) (*protocol.AgentStatus, error) {
	var a protocol.AgentStatus
	var tags, caps string
	var first, last int64
	if err := row.Scan(&a.ID, &a.Name, &a.Location, &a.ISP, &tags, &a.OS, &a.Arch, &a.Version, &a.PublicIP,
		&a.GeoLocation, &a.GeoISP, &caps, &first, &last); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &a.Tags)
	_ = json.Unmarshal([]byte(caps), &a.Capabilities)
	a.FirstSeen, a.LastSeen = fromMs(first), fromMs(last)
	return &a, nil
}

// GetAgent returns one agent or ErrNotFound.
func (s *Store) GetAgent(id string) (*protocol.AgentStatus, error) {
	a, err := scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// ListAgents returns every known agent ordered by name.
func (s *Store) ListAgents() ([]*protocol.AgentStatus, error) {
	rows, err := s.db.Query(`SELECT ` + agentCols + ` FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*protocol.AgentStatus
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAgent removes an agent record.
func (s *Store) DeleteAgent(id string) error {
	_, err := s.db.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}

// --- tasks ------------------------------------------------------------------

// InsertTask stores a new task.
func (s *Store) InsertTask(t *protocol.Task) error {
	_, err := s.db.Exec(`INSERT INTO tasks (id,type,target,params,agent_ids,created_at) VALUES (?,?,?,?,?,?)`,
		t.ID, string(t.Type), t.Target, jsonStr(t.Params), jsonStr(nonNil(t.AgentIDs)), unixMs(t.CreatedAt))
	return err
}

func scanTask(row interface{ Scan(...any) error }) (*protocol.Task, error) {
	var t protocol.Task
	var typ, params, agents string
	var created int64
	if err := row.Scan(&t.ID, &typ, &t.Target, &params, &agents, &created); err != nil {
		return nil, err
	}
	t.Type = protocol.TaskType(typ)
	_ = json.Unmarshal([]byte(params), &t.Params)
	_ = json.Unmarshal([]byte(agents), &t.AgentIDs)
	t.CreatedAt = fromMs(created)
	return &t, nil
}

// GetTask returns one task or ErrNotFound.
func (s *Store) GetTask(id string) (*protocol.Task, error) {
	t, err := scanTask(s.db.QueryRow(`SELECT id,type,target,params,agent_ids,created_at FROM tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListTasks returns tasks newest first.
func (s *Store) ListTasks(limit, offset int) ([]*protocol.Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,type,target,params,agent_ids,created_at FROM tasks ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*protocol.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PruneTasks deletes tasks (and their results) created before cutoff.
func (s *Store) PruneTasks(cutoff time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM results WHERE task_id IN (SELECT id FROM tasks WHERE created_at < ?)`, unixMs(cutoff)); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM tasks WHERE created_at < ?`, unixMs(cutoff))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

// --- results ----------------------------------------------------------------

// UpsertResult stores the final (or failed) result of one agent for a task.
func (s *Store) UpsertResult(r *protocol.AgentResult) error {
	var data any
	if len(r.Data) > 0 {
		data = string(r.Data)
	}
	var started, finished any
	if r.StartedAt != nil {
		started = unixMs(*r.StartedAt)
	}
	if r.FinishedAt != nil {
		finished = unixMs(*r.FinishedAt)
	}
	_, err := s.db.Exec(`
INSERT INTO results (task_id,agent_id,agent_name,location,isp,status,error,duration_ms,data,started_at,finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(task_id,agent_id) DO UPDATE SET
	status=excluded.status, error=excluded.error, duration_ms=excluded.duration_ms, data=excluded.data,
	started_at=COALESCE(excluded.started_at, results.started_at), finished_at=excluded.finished_at`,
		r.TaskID, r.AgentID, r.AgentName, r.Location, r.ISP, string(r.Status), r.Error, r.DurationMs, data, started, finished)
	return err
}

// ListResults returns every stored result for a task.
func (s *Store) ListResults(taskID string) ([]*protocol.AgentResult, error) {
	rows, err := s.db.Query(`SELECT task_id,agent_id,agent_name,location,isp,status,error,duration_ms,data,started_at,finished_at
		FROM results WHERE task_id=? ORDER BY agent_name`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*protocol.AgentResult{}
	for rows.Next() {
		var r protocol.AgentResult
		var status string
		var data sql.NullString
		var started, finished sql.NullInt64
		if err := rows.Scan(&r.TaskID, &r.AgentID, &r.AgentName, &r.Location, &r.ISP, &status, &r.Error, &r.DurationMs, &data, &started, &finished); err != nil {
			return nil, err
		}
		r.Status = protocol.ResultStatus(status)
		if data.Valid && data.String != "" {
			r.Data = json.RawMessage(data.String)
		}
		if started.Valid {
			t := fromMs(started.Int64)
			r.StartedAt = &t
		}
		if finished.Valid {
			t := fromMs(finished.Int64)
			r.FinishedAt = &t
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}
