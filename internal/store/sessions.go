package store

import (
	"context"
	"database/sql"
	"time"
)

// Session is a logged-in admin session. Only the SHA-256 hash of the session
// token is stored, so a database leak does not leak usable sessions.
type Session struct {
	IDHash     string
	UserID     int64
	CSRFToken  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	// AuthAt is when the user last proved their credentials (login or
	// step-up). Sensitive actions require a recent AuthAt.
	AuthAt    time.Time
	IP        string
	UserAgent string
	Method    string
}

// CreateSession stores a new session.
func (s *Store) CreateSession(ctx context.Context, se *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, auth_at, ip, user_agent, method)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		se.IDHash, se.UserID, se.CSRFToken, se.CreatedAt, se.LastSeenAt, se.ExpiresAt, se.AuthAt, se.IP, se.UserAgent, se.Method)
	return err
}

// GetSession returns the session with the given token hash.
func (s *Store) GetSession(ctx context.Context, idHash string) (*Session, error) {
	var se Session
	err := s.db.QueryRowContext(ctx,
		`SELECT id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, auth_at, ip, user_agent, method
		 FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&se.IDHash, &se.UserID, &se.CSRFToken, &se.CreatedAt, &se.LastSeenAt, &se.ExpiresAt, &se.AuthAt, &se.IP, &se.UserAgent, &se.Method)
	if err != nil {
		return nil, notFound(err)
	}
	return &se, nil
}

// TouchSession updates the last-seen time of a session.
func (s *Store) TouchSession(ctx context.Context, idHash string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, at, idHash)
	return err
}

// MarkSessionAuth records a successful step-up authentication.
func (s *Store) MarkSessionAuth(ctx context.Context, idHash string, at time.Time) error {
	return s.execOne(ctx, `UPDATE sessions SET auth_at = ? WHERE id_hash = ?`, at, idHash)
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteUserSessions removes every session of a user except keep (may be "").
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keep string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash != ?`, userID, keep)
	return err
}

// PurgeExpiredSessions deletes sessions that expired or idled out.
func (s *Store) PurgeExpiredSessions(ctx context.Context, now time.Time, idle time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`, now, now.Add(-idle))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// WebAuthnCredential is a registered passkey. Data holds the JSON-encoded
// credential as produced by the WebAuthn library.
type WebAuthnCredential struct {
	ID         []byte     `json:"-"`
	UserID     int64      `json:"-"`
	Name       string     `json:"name"`
	Data       string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// AddWebAuthnCredential stores a passkey.
func (s *Store) AddWebAuthnCredential(ctx context.Context, c *WebAuthnCredential) error {
	c.CreatedAt = s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webauthn_credentials(id, user_id, name, data, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.UserID, c.Name, c.Data, c.CreatedAt)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

// ListWebAuthnCredentials returns the passkeys of a user.
func (s *Store) ListWebAuthnCredentials(ctx context.Context, userID int64) ([]*WebAuthnCredential, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, data, created_at, last_used_at FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebAuthnCredential
	for rows.Next() {
		var c WebAuthnCredential
		var last sql.NullTime
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.Data, &c.CreatedAt, &last); err != nil {
			return nil, err
		}
		c.LastUsedAt = nullTime(last)
		out = append(out, &c)
	}
	return out, rows.Err()
}

// UpdateWebAuthnCredential stores the updated credential data (sign count,
// flags) after a successful login.
func (s *Store) UpdateWebAuthnCredential(ctx context.Context, id []byte, data string) error {
	return s.execOne(ctx, `UPDATE webauthn_credentials SET data = ?, last_used_at = ? WHERE id = ?`, data, s.now(), id)
}

// DeleteWebAuthnCredential removes a passkey of the given user.
func (s *Store) DeleteWebAuthnCredential(ctx context.Context, userID int64, id []byte) error {
	return s.execOne(ctx, `DELETE FROM webauthn_credentials WHERE user_id = ? AND id = ?`, userID, id)
}
