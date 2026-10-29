package store

import (
	"context"
	"database/sql"
	"errors"
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
	gid, err := groupID(ctx, tx, group)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members(user_id, group_id) VALUES (?, ?)`, userID, gid)
	return err
}

// RemoveGroupMember takes a user out of a group (no-op when not a member).
// It fails with ErrNotFound when the group does not exist.
func (s *Store) RemoveGroupMember(ctx context.Context, userID int64, group string) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return removeMember(ctx, tx, userID, group) })
}

func removeMember(ctx context.Context, tx *sql.Tx, userID int64, group string) error {
	gid, err := groupID(ctx, tx, group)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM group_members WHERE user_id = ? AND group_id = ?`, userID, gid)
	return err
}

// SetUserCustomGroups makes the user's custom-group memberships exactly
// names, in one transaction; built-in memberships are left as they are. It
// fails with ErrNotFound when a group does not exist, and with
// ErrBuiltinGroup when names holds a built-in group.
func (s *Store) SetUserCustomGroups(ctx context.Context, userID int64, names []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		want := map[string]bool{}
		for _, n := range names {
			var builtin bool
			if err := tx.QueryRowContext(ctx, `SELECT name, builtin FROM groups WHERE name = ?`, n).Scan(&n, &builtin); err != nil {
				return notFound(err)
			}
			if builtin {
				return ErrBuiltinGroup
			}
			want[n] = true
		}
		rows, err := tx.QueryContext(ctx, `SELECT g.name FROM group_members m JOIN groups g ON g.id = m.group_id WHERE m.user_id = ? AND g.builtin = 0`, userID)
		if err != nil {
			return err
		}
		var current []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			current = append(current, n)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, n := range current {
			if !want[n] {
				if err := removeMember(ctx, tx, userID, n); err != nil {
					return err
				}
			}
		}
		for n := range want {
			if err := addMember(ctx, tx, userID, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// ErrBuiltinGroup: the change concerns a built-in group, which this path
// does not manage.
var ErrBuiltinGroup = errors.New("store: built-in group")

func groupID(ctx context.Context, tx *sql.Tx, group string) (int64, error) {
	var gid int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = ?`, group).Scan(&gid); err != nil {
		return 0, notFound(err)
	}
	return gid, nil
}
