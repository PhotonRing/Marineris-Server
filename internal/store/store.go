// Package store persists Marineris state in a local SQLite database.
//
// The database is the single source of truth: every generated configuration
// (Caddy, Authelia, …) is rendered from it. Credentials are stored sealed
// with the master key (see package secrets).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when a unique constraint would be violated.
var ErrConflict = errors.New("store: already exists")

// Store wraps the SQLite database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (and creates if needed) the database at path and applies
// pending migrations. The file is created with mode 0600.
func Open(ctx context.Context, path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path from process configuration
		if err != nil {
			return nil, err
		}
		f.Close()
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; serialising through one connection
	// avoids SQLITE_BUSY under concurrent requests.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens a private in-memory database, for tests.
func OpenMemory(ctx context.Context) (*Store, error) {
	return Open(ctx, ":memory:")
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// SetClock overrides the time source, for tests.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE users (
	id             INTEGER PRIMARY KEY,
	username       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash  TEXT NOT NULL,
	totp_secret    TEXT NOT NULL DEFAULT '',
	totp_last_step INTEGER NOT NULL DEFAULT 0,
	recovery_codes TEXT NOT NULL DEFAULT '[]',
	webauthn_id    BLOB NOT NULL,
	role           TEXT NOT NULL DEFAULT 'admin',
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL
);
CREATE TABLE webauthn_credentials (
	id           BLOB PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	data         TEXT NOT NULL,
	created_at   TIMESTAMP NOT NULL,
	last_used_at TIMESTAMP
);
CREATE TABLE sessions (
	id_hash      TEXT PRIMARY KEY,
	user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	csrf_token   TEXT NOT NULL,
	created_at   TIMESTAMP NOT NULL,
	last_seen_at TIMESTAMP NOT NULL,
	expires_at   TIMESTAMP NOT NULL,
	auth_at      TIMESTAMP NOT NULL,
	ip           TEXT NOT NULL DEFAULT '',
	user_agent   TEXT NOT NULL DEFAULT '',
	method       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE dns_profiles (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL UNIQUE COLLATE NOCASE,
	provider      TEXT NOT NULL,
	config        TEXT NOT NULL,
	created_at    TIMESTAMP NOT NULL,
	updated_at    TIMESTAMP NOT NULL,
	last_test_at  TIMESTAMP,
	last_test_ok  INTEGER NOT NULL DEFAULT 0,
	last_test_msg TEXT NOT NULL DEFAULT ''
);
CREATE TABLE domains (
	id             INTEGER PRIMARY KEY,
	name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
	dns_profile_id INTEGER REFERENCES dns_profiles(id) ON DELETE RESTRICT,
	wildcard       INTEGER NOT NULL DEFAULT 1,
	created_at     TIMESTAMP NOT NULL,
	updated_at     TIMESTAMP NOT NULL
);
CREATE TABLE routes (
	id          INTEGER PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
	host        TEXT NOT NULL COLLATE NOCASE,
	path_prefix TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	target      TEXT NOT NULL,
	auth        TEXT NOT NULL DEFAULT 'authelia',
	rate_limit  TEXT NOT NULL DEFAULT '{}',
	enabled     INTEGER NOT NULL DEFAULT 1,
	created_at  TIMESTAMP NOT NULL,
	updated_at  TIMESTAMP NOT NULL,
	UNIQUE(host, path_prefix)
);
CREATE TABLE stacks (
	name        TEXT PRIMARY KEY,
	compose     TEXT NOT NULL,
	env         TEXT NOT NULL DEFAULT '',
	unsafe_ack  INTEGER NOT NULL DEFAULT 0,
	created_at  TIMESTAMP NOT NULL,
	updated_at  TIMESTAMP NOT NULL,
	deployed_at TIMESTAMP
);
CREATE TABLE config_history (
	id         INTEGER PRIMARY KEY,
	component  TEXT NOT NULL,
	created_at TIMESTAMP NOT NULL,
	actor      TEXT NOT NULL,
	reason     TEXT NOT NULL,
	content    TEXT NOT NULL,
	status     TEXT NOT NULL,
	error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX config_history_component ON config_history(component, id);
CREATE TABLE audit_log (
	id      INTEGER PRIMARY KEY,
	at      TIMESTAMP NOT NULL,
	actor   TEXT NOT NULL,
	ip      TEXT NOT NULL,
	action  TEXT NOT NULL,
	target  TEXT NOT NULL,
	success INTEGER NOT NULL,
	details TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log(at);
`,
	// 2: hub (app launcher) metadata and icons
	`
ALTER TABLE routes ADD COLUMN hub_visible INTEGER NOT NULL DEFAULT 1;
ALTER TABLE routes ADD COLUMN hub_title TEXT NOT NULL DEFAULT '';
ALTER TABLE routes ADD COLUMN hub_subtitle TEXT NOT NULL DEFAULT '';
CREATE TABLE route_icons (
	route_id     INTEGER PRIMARY KEY REFERENCES routes(id) ON DELETE CASCADE,
	content_type TEXT NOT NULL,
	data         BLOB NOT NULL,
	updated_at   TIMESTAMP NOT NULL
);
`,
	// 3: uploaded images: branding assets and stack service icons
	`
CREATE TABLE images (
	kind         TEXT NOT NULL,
	key          TEXT NOT NULL,
	content_type TEXT NOT NULL,
	data         BLOB NOT NULL,
	updated_at   TIMESTAMP NOT NULL,
	PRIMARY KEY (kind, key)
);
`,
	// 4: a route's hub icon can link to a stack's or a service's icon
	`
ALTER TABLE routes ADD COLUMN hub_icon_link TEXT NOT NULL DEFAULT '';
`,
	// 5: a user's authenticator is registered in Authelia too
	`
ALTER TABLE users ADD COLUMN authelia_totp TEXT NOT NULL DEFAULT '';
`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than this binary supports (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		if err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// isUnique reports whether err is a SQLite unique-constraint violation.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}
