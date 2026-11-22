package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Share is a public, revocable link to one task's result. What it reveals is
// fixed when it is created (the Show* flags); see share.go for the scrubbing.
type Share struct {
	Token  string `json:"token"`
	TaskID string `json:"task_id"`
	// Mode "follow" tracks the task until it finishes and then freezes;
	// "snapshot" is frozen at creation.
	Mode       string `json:"mode"`
	ShowTarget bool   `json:"show_target"`
	ShowTime   bool   `json:"show_time"`
	ShowIPs    bool   `json:"show_ips"`
	ShowNodes  bool   `json:"show_nodes"`
	// Snapshot is the already scrubbed result once frozen, nil while a
	// "follow" share's task is still running.
	Snapshot  json.RawMessage `json:"-"`
	CreatedBy string          `json:"-"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	RevokedAt *time.Time      `json:"revoked_at,omitempty"`
	Visits    int             `json:"visits"`
}

// Active reports whether the link still opens.
func (s *Share) Active(now time.Time) bool { return s.RevokedAt == nil && now.Before(s.ExpiresAt) }

const shareSchema = `
CREATE TABLE IF NOT EXISTS shares (
	token       TEXT PRIMARY KEY,
	task_id     TEXT NOT NULL,
	mode        TEXT NOT NULL,
	show_target INTEGER NOT NULL DEFAULT 0,
	show_time   INTEGER NOT NULL DEFAULT 1,
	show_ips    INTEGER NOT NULL DEFAULT 0,
	show_nodes  INTEGER NOT NULL DEFAULT 0,
	snapshot    TEXT,
	created_by  TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	revoked_at  INTEGER,
	visits      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_shares_task ON shares(task_id);
`

const shareCols = `token,task_id,mode,show_target,show_time,show_ips,show_nodes,snapshot,created_by,created_at,expires_at,revoked_at,visits`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	var s Share
	var snap sql.NullString
	var created, expires int64
	var revoked sql.NullInt64
	if err := row.Scan(&s.Token, &s.TaskID, &s.Mode, &s.ShowTarget, &s.ShowTime, &s.ShowIPs, &s.ShowNodes, &snap, &s.CreatedBy, &created, &expires, &revoked, &s.Visits); err != nil {
		return nil, err
	}
	if snap.Valid && snap.String != "" {
		s.Snapshot = json.RawMessage(snap.String)
	}
	s.CreatedAt, s.ExpiresAt = fromMs(created), fromMs(expires)
	if revoked.Valid {
		t := fromMs(revoked.Int64)
		s.RevokedAt = &t
	}
	return &s, nil
}

func (s *Store) InsertShare(sh *Share) error {
	var snap any
	if len(sh.Snapshot) > 0 {
		snap = string(sh.Snapshot)
	}
	_, err := s.db.Exec(`INSERT INTO shares (`+shareCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,NULL,0)`,
		sh.Token, sh.TaskID, sh.Mode, sh.ShowTarget, sh.ShowTime, sh.ShowIPs, sh.ShowNodes, snap, sh.CreatedBy, unixMs(sh.CreatedAt), unixMs(sh.ExpiresAt))
	return err
}

// GetShare returns one share or ErrNotFound.
func (s *Store) GetShare(token string) (*Share, error) {
	sh, err := scanShare(s.db.QueryRow(`SELECT `+shareCols+` FROM shares WHERE token=?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sh, err
}

// ListShares returns a task's shares, newest first, at most 50.
func (s *Store) ListShares(taskID string) ([]*Share, error) {
	rows, err := s.db.Query(`SELECT `+shareCols+` FROM shares WHERE task_id=? ORDER BY created_at DESC LIMIT 50`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// RevokeShare closes a link for good. ErrNotFound if there is no such share.
func (s *Store) RevokeShare(token string, at time.Time) error {
	res, err := s.db.Exec(`UPDATE shares SET revoked_at=COALESCE(revoked_at, ?) WHERE token=?`, unixMs(at), token)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FreezeShare stores the scrubbed result of a share that has none yet.
func (s *Store) FreezeShare(token string, snapshot json.RawMessage) error {
	_, err := s.db.Exec(`UPDATE shares SET snapshot=? WHERE token=? AND snapshot IS NULL`, string(snapshot), token)
	return err
}

func (s *Store) AddShareVisit(token string) error {
	_, err := s.db.Exec(`UPDATE shares SET visits=visits+1 WHERE token=?`, token)
	return err
}

// PruneShares deletes shares that stopped working before cutoff, whether
// they expired or were revoked.
func (s *Store) PruneShares(cutoff time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM shares WHERE expires_at<? OR (revoked_at IS NOT NULL AND revoked_at<?)`, unixMs(cutoff), unixMs(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
