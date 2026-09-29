package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	last_seen     INTEGER NOT NULL,
	token         TEXT NOT NULL DEFAULT '',
	auth          TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_token ON agents(token) WHERE token != '';
CREATE TABLE IF NOT EXISTS tasks (
	id         TEXT PRIMARY KEY,
	type       TEXT NOT NULL,
	target     TEXT NOT NULL,
	params     TEXT NOT NULL DEFAULT '{}',
	agent_ids  TEXT NOT NULL DEFAULT '[]',
	created_at INTEGER NOT NULL,
	monitor_id TEXT NOT NULL DEFAULT '',
	owner      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_monitor ON tasks(monitor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_owner ON tasks(owner, created_at DESC);
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
	st := &Store{db: db}
	if err := st.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return st, nil
}

// migrate creates missing tables and adds columns introduced after the first
// release to databases created before them.
func (s *Store) migrate() error {
	// Columns added to tasks over time. The CREATE TABLE above already has
	// them for fresh databases; older files get them via ALTER TABLE, which
	// has to happen before the schema's indexes reference them.
	if _, err := s.ensureColumn("tasks", "monitor_id", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := s.ensureColumn("tasks", "owner", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// Per-node tokens. Every node recorded before them connected with the
	// shared token, so it is marked legacy: that is what lets it keep using
	// the shared token until it has been migrated.
	if _, err := s.ensureColumn("agents", "token", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	added, err := s.ensureColumn("agents", "auth", `TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		return err
	}
	if added {
		if _, err := s.db.Exec(`UPDATE agents SET auth='legacy'`); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if _, err := s.db.Exec(monitorSchema); err != nil {
		return fmt.Errorf("monitors: %w", err)
	}
	return s.pruneOrphanAgentRefs()
}

// ensureColumn adds column to table when the table exists without it and
// reports whether it did.
func (s *Store) ensureColumn(table, column, ddl string) (bool, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil // CREATE TABLE will include it
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	rows.Close()
	if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + ddl); err != nil {
		return false, err
	}
	return true, nil
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
// update (unless the node was created on the dashboard and is connecting for
// the first time); LastSeen is set to a.LastSeen (or now). The token is never
// touched here.
func (s *Store) UpsertAgent(a *protocol.AgentStatus) error {
	now := time.Now()
	if a.LastSeen.IsZero() {
		a.LastSeen = now
	}
	if a.FirstSeen.IsZero() {
		a.FirstSeen = now
	}
	_, err := s.db.Exec(`
INSERT INTO agents (id,name,location,isp,tags,os,arch,version,public_ip,geo_location,geo_isp,capabilities,first_seen,last_seen,auth)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	name=excluded.name, location=excluded.location, isp=excluded.isp, tags=excluded.tags,
	os=excluded.os, arch=excluded.arch, version=excluded.version, public_ip=excluded.public_ip,
	geo_location=CASE WHEN excluded.geo_location='' THEN agents.geo_location ELSE excluded.geo_location END,
	geo_isp=CASE WHEN excluded.geo_isp='' THEN agents.geo_isp ELSE excluded.geo_isp END,
	capabilities=excluded.capabilities, last_seen=excluded.last_seen,
	first_seen=CASE WHEN agents.first_seen=0 THEN excluded.first_seen ELSE agents.first_seen END,
	auth=CASE WHEN excluded.auth='' THEN agents.auth ELSE excluded.auth END`,
		a.ID, a.Name, a.Location, a.ISP, jsonStr(nonNil(a.Tags)), a.OS, a.Arch, a.Version, a.PublicIP,
		a.GeoLocation, a.GeoISP, jsonStr(nonNil(a.Capabilities)), unixMs(a.FirstSeen), unixMs(a.LastSeen), a.Auth)
	return err
}

// ErrAgentExists is returned by CreateAgent when the id is taken.
var ErrAgentExists = errors.New("agent exists")

// CreateAgent records a node created on the dashboard before it has ever
// connected, together with its token.
func (s *Store) CreateAgent(a *protocol.AgentStatus, token string) error {
	res, err := s.db.Exec(`
INSERT INTO agents (id,name,location,isp,first_seen,last_seen,token,auth) VALUES (?,?,?,?,0,0,?,'')
ON CONFLICT(id) DO NOTHING`, a.ID, a.Name, a.Location, a.ISP, token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAgentExists
	}
	return nil
}

// AgentByToken returns the node that owns token, or ErrNotFound.
func (s *Store) AgentByToken(token string) (*protocol.AgentStatus, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	a, err := scanAgent(s.db.QueryRow(`SELECT `+agentCols+` FROM agents WHERE token=?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// AgentToken returns a node's token, generating one first if the node has
// none yet (nodes that predate per-node tokens).
func (s *Store) AgentToken(id string) (string, error) {
	var tok string
	err := s.db.QueryRow(`SELECT token FROM agents WHERE id=?`, id).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil || tok != "" {
		return tok, err
	}
	tok = newAgentToken()
	// Only fill an empty token: a concurrent caller may have just set one.
	if _, err := s.db.Exec(`UPDATE agents SET token=? WHERE id=? AND token=''`, tok, id); err != nil {
		return "", err
	}
	err = s.db.QueryRow(`SELECT token FROM agents WHERE id=?`, id).Scan(&tok)
	return tok, err
}

// ResetAgentToken replaces a node's token; the old one stops working at once.
func (s *Store) ResetAgentToken(id string) (string, error) {
	tok := newAgentToken()
	res, err := s.db.Exec(`UPDATE agents SET token=? WHERE id=?`, tok, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return tok, nil
}

func newAgentToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// SetAgentGeo stores the location looked up from a node's public IP.
func (s *Store) SetAgentGeo(id, location, isp string) error {
	_, err := s.db.Exec(`UPDATE agents SET geo_location=?, geo_isp=? WHERE id=?`, location, isp, id)
	return err
}

// TouchAgent updates last_seen.
func (s *Store) TouchAgent(id string, t time.Time) error {
	_, err := s.db.Exec(`UPDATE agents SET last_seen=? WHERE id=?`, unixMs(t), id)
	return err
}

const agentCols = `id,name,location,isp,tags,os,arch,version,public_ip,geo_location,geo_isp,capabilities,first_seen,last_seen,auth`

func scanAgent(row interface{ Scan(...any) error }) (*protocol.AgentStatus, error) {
	var a protocol.AgentStatus
	var tags, caps string
	var first, last int64
	if err := row.Scan(&a.ID, &a.Name, &a.Location, &a.ISP, &tags, &a.OS, &a.Arch, &a.Version, &a.PublicIP,
		&a.GeoLocation, &a.GeoISP, &caps, &first, &last, &a.Auth); err != nil {
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

// DeleteAgent removes an agent record together with what monitoring holds for
// it (alert state, samples, its place in monitors' node lists), so a deleted
// node cannot keep a monitor in the alerting state.
func (s *Store) DeleteAgent(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM agents WHERE id=?`, id); err != nil {
		return err
	}
	if err := purgeAgentRefs(tx, map[string]bool{id: true}); err != nil {
		return err
	}
	return tx.Commit()
}

// --- tasks ------------------------------------------------------------------

// InsertTask stores a new task.
func (s *Store) InsertTask(t *protocol.Task) error {
	_, err := s.db.Exec(`INSERT INTO tasks (id,type,target,params,agent_ids,created_at,monitor_id,owner) VALUES (?,?,?,?,?,?,?,?)`,
		t.ID, string(t.Type), t.Target, jsonStr(t.Params), jsonStr(nonNil(t.AgentIDs)), unixMs(t.CreatedAt), t.MonitorID, t.Owner)
	return err
}

const taskCols = `id,type,target,params,agent_ids,created_at,monitor_id,owner`

func scanTask(row interface{ Scan(...any) error }) (*protocol.Task, error) {
	var t protocol.Task
	var typ, params, agents string
	var created int64
	if err := row.Scan(&t.ID, &typ, &t.Target, &params, &agents, &created, &t.MonitorID, &t.Owner); err != nil {
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
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// TaskFilter narrows ListTasks. MonitorID "" lists ad-hoc tasks only, "*"
// lists everything and any other value lists that monitor's runs. Owner ""
// means any owner; otherwise only that owner's tasks are returned.
type TaskFilter struct {
	MonitorID string
	Owner     string
}

// ListTasks returns tasks newest first.
func (s *Store) ListTasks(limit, offset int, f TaskFilter) ([]*protocol.Task, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	args := []any{}
	switch f.MonitorID {
	case "*":
	case "":
		q += ` AND monitor_id=''`
	default:
		q += ` AND monitor_id=?`
		args = append(args, f.MonitorID)
	}
	if f.Owner != "" {
		q += ` AND owner=?`
		args = append(args, f.Owner)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(q, args...)
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
