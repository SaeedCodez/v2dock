package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Session struct {
	IDHash    string
	CreatedAt int64
	ExpiresAt int64
	LastSeen  int64
	IP        string
	UserAgent string
}

func (db *DB) CreateSession(ctx context.Context, s Session) error {
	_, err := db.ExecContext(ctx, `INSERT INTO sessions (id_hash, created_at, expires_at, last_seen, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?)`,
		s.IDHash, s.CreatedAt, s.ExpiresAt, s.LastSeen, s.IP, s.UserAgent)
	return err
}

func (db *DB) GetSession(ctx context.Context, idHash string) (Session, error) {
	var s Session
	err := db.QueryRowContext(ctx, `SELECT id_hash, created_at, expires_at, last_seen, ip, user_agent FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&s.IDHash, &s.CreatedAt, &s.ExpiresAt, &s.LastSeen, &s.IP, &s.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func (db *DB) TouchSession(ctx context.Context, idHash string, t time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET last_seen = ? WHERE id_hash = ?`, t.Unix(), idHash)
	return err
}

func (db *DB) DeleteSession(ctx context.Context, idHash string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

func (db *DB) DeleteAllSessions(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

func (db *DB) CountSessions(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE expires_at > ?`, time.Now().Unix()).Scan(&n)
	return n, err
}

type APIToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Hash       string `json:"-"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

func (db *DB) CreateToken(ctx context.Context, t APIToken) error {
	_, err := db.ExecContext(ctx, `INSERT INTO api_tokens (id, name, hash, scope, created_at) VALUES (?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Hash, t.Scope, time.Now().Unix())
	return err
}

func (db *DB) ListTokens(ctx context.Context) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, scope, created_at, last_used_at FROM api_tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Scope, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (db *DB) TokenByHash(ctx context.Context, hash string) (APIToken, error) {
	var t APIToken
	err := db.QueryRowContext(ctx, `SELECT id, name, hash, scope, created_at, last_used_at FROM api_tokens WHERE hash = ?`, hash).
		Scan(&t.ID, &t.Name, &t.Hash, &t.Scope, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (db *DB) TouchToken(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func (db *DB) DeleteToken(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
