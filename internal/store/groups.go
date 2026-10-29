package store

import (
	"context"
	"database/sql"
)

// CreateGroup creates a custom group. Names are unique ignoring case;
// validating them is the caller's job.
func (s *Store) CreateGroup(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO groups(name, builtin, created_at) VALUES (?, 0, ?)`, name, s.now())
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

// DeleteGroup deletes a group and its memberships. It does not protect the
// built-in groups: callers refuse that.
func (s *Store) DeleteGroup(ctx context.Context, name string) error {
	return s.execOne(ctx, `DELETE FROM groups WHERE name = ?`, name)
}

// AddGroupMember puts a user in a group (no-op when already a member). It
// fails with ErrNotFound when the group does not exist.
func (s *Store) AddGroupMember(ctx context.Context, userID int64, group string) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return addMember(ctx, tx, userID, group) })
}

func addMember(ctx context.Context, tx *sql.Tx, userID int64, group string) error {
	var gid int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = ?`, group).Scan(&gid); err != nil {
		return notFound(err)
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members(user_id, group_id) VALUES (?, ?)`, userID, gid)
	return err
}
