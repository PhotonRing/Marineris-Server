package store

import (
	"context"
	"time"
)

// Config history statuses.
const (
	ConfigApplied    = "applied"
	ConfigFailed     = "failed"
	ConfigRolledBack = "rolled_back"
	ConfigSuperseded = "superseded"
)

// ConfigVersion is one generated configuration of a component. Content is
// sealed because generated configs embed credentials.
type ConfigVersion struct {
	ID        int64     `json:"id"`
	Component string    `json:"component"`
	CreatedAt time.Time `json:"created_at"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Content   string    `json:"-"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
}

const historyCols = `id, component, created_at, actor, reason, content, status, error`

func scanVersion(row interface{ Scan(...any) error }) (*ConfigVersion, error) {
	var v ConfigVersion
	if err := row.Scan(&v.ID, &v.Component, &v.CreatedAt, &v.Actor, &v.Reason, &v.Content, &v.Status, &v.Error); err != nil {
		return nil, notFound(err)
	}
	return &v, nil
}

// AddConfigVersion appends a configuration version. The sealed content is
// produced by seal once the row ID is known.
func (s *Store) AddConfigVersion(ctx context.Context, v *ConfigVersion) error {
	v.CreatedAt = s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO config_history(component, created_at, actor, reason, content, status, error) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.Component, v.CreatedAt, v.Actor, v.Reason, v.Content, v.Status, v.Error)
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	return err
}

// SetConfigStatus updates the status of a version.
func (s *Store) SetConfigStatus(ctx context.Context, id int64, status, errMsg string) error {
	return s.execOne(ctx, `UPDATE config_history SET status = ?, error = ? WHERE id = ?`, status, errMsg, id)
}

// GetConfigVersion returns one version.
func (s *Store) GetConfigVersion(ctx context.Context, id int64) (*ConfigVersion, error) {
	return scanVersion(s.db.QueryRowContext(ctx, `SELECT `+historyCols+` FROM config_history WHERE id = ?`, id))
}

// LastAppliedConfig returns the most recent applied version of a component.
func (s *Store) LastAppliedConfig(ctx context.Context, component string) (*ConfigVersion, error) {
	return scanVersion(s.db.QueryRowContext(ctx,
		`SELECT `+historyCols+` FROM config_history WHERE component = ? AND status = ? ORDER BY id DESC LIMIT 1`,
		component, ConfigApplied))
}

// ListConfigVersions returns the newest versions of a component first.
func (s *Store) ListConfigVersions(ctx context.Context, component string, limit int) ([]*ConfigVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+historyCols+` FROM config_history WHERE component = ? ORDER BY id DESC LIMIT ?`, component, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConfigVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PruneConfigHistory keeps the newest keep versions of a component.
func (s *Store) PruneConfigHistory(ctx context.Context, component string, keep int) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM config_history WHERE component = ? AND id NOT IN (
			SELECT id FROM config_history WHERE component = ? ORDER BY id DESC LIMIT ?)`,
		component, component, keep)
	return err
}

// AuditEntry records a security-relevant action.
type AuditEntry struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	IP      string    `json:"ip"`
	Action  string    `json:"action"`
	Target  string    `json:"target"`
	Success bool      `json:"success"`
	Details string    `json:"details"`
}

// Audit appends an entry to the audit log.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	if e.At.IsZero() {
		e.At = s.now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log(at, actor, ip, action, target, success, details) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.At, e.Actor, e.IP, e.Action, e.Target, e.Success, e.Details)
	return err
}

// ListAudit returns the newest audit entries first.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]*AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, actor, ip, action, target, success, details FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.IP, &e.Action, &e.Target, &e.Success, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
