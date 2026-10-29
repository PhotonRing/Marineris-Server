package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
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

// DeleteGroup deletes a custom group and its memberships; its users stay. A
// built-in group is never deleted (ErrNotFound): callers refuse it first.
func (s *Store) DeleteGroup(ctx context.Context, name string) error {
	return s.execOne(ctx, `DELETE FROM groups WHERE name = ? AND builtin = 0`, name)
}

// RenameGroup renames a custom group: ErrNotFound when there is no such
// custom group (built-ins are never renamed), ErrConflict when the new name
// is taken, ignoring case.
func (s *Store) RenameGroup(ctx context.Context, name, newName string) error {
	return s.execOne(ctx, `UPDATE groups SET name = ? WHERE name = ? AND builtin = 0`, newName, name)
}

// Group is a group with its members.
type Group struct {
	Name      string        `json:"name"`
	Builtin   bool          `json:"builtin"`
	CreatedAt time.Time     `json:"created_at"`
	Members   []GroupMember `json:"members"`
}

// GroupMember is a user as a group lists them.
type GroupMember struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// ListGroups returns every group, built-ins first then by name, each with
// its members sorted by username.
func (s *Store) ListGroups(ctx context.Context) ([]*Group, error) {
	return s.groups(ctx, "")
}

// GetGroup returns one group with its members (ErrNotFound).
func (s *Store) GetGroup(ctx context.Context, name string) (*Group, error) {
	gs, err := s.groups(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(gs) == 0 {
		return nil, ErrNotFound
	}
	return gs[0], nil
}

// groups returns the group named name, or every group when name is empty.
func (s *Store) groups(ctx context.Context, name string) ([]*Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.name, g.builtin, g.created_at, u.id, u.username, u.display_name
		FROM groups g LEFT JOIN group_members m ON m.group_id = g.id LEFT JOIN users u ON u.id = m.user_id
		WHERE ? = '' OR g.name = ?
		ORDER BY g.builtin DESC, g.name, u.username`, name, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		var g Group
		var id sql.NullInt64
		var username, display sql.NullString
		if err := rows.Scan(&g.Name, &g.Builtin, &g.CreatedAt, &id, &username, &display); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != g.Name {
			g.Members = []GroupMember{}
			out = append(out, &g)
		}
		if id.Valid {
			last := out[len(out)-1]
			last.Members = append(last.Members, GroupMember{ID: id.Int64, Username: username.String, DisplayName: display.String})
		}
	}
	return out, rows.Err()
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

// The rules of the built-in groups, kept here so that no caller can break
// them: proxy-admins holds only members of admins, admins is never empty and
// always holds the Owner.
var (
	// ErrNotAdministrator: the user is not in GroupAdmins.
	ErrNotAdministrator = errors.New("store: not an administrator")
	// ErrLastAdministrator: the change would leave GroupAdmins empty.
	ErrLastAdministrator = errors.New("store: last administrator")
	// ErrOwnerAdministrator: the Owner always stays in GroupAdmins.
	ErrOwnerAdministrator = errors.New("store: the owner stays an administrator")
)

// AddBuiltinMember puts a user in GroupAdmins or GroupProxyAdmins (no-op
// when already a member). Only members of GroupAdmins join GroupProxyAdmins
// (ErrNotAdministrator).
func (s *Store) AddBuiltinMember(ctx context.Context, userID int64, group string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := userExists(ctx, tx, userID); err != nil {
			return err
		}
		if group == GroupProxyAdmins {
			in, err := isMember(ctx, tx, userID, GroupAdmins)
			if err != nil {
				return err
			}
			if !in {
				return ErrNotAdministrator
			}
		}
		return addMember(ctx, tx, userID, group)
	})
}

// RemoveBuiltinMember takes a user out of GroupAdmins or GroupProxyAdmins
// (no-op when not a member). Leaving GroupAdmins also leaves
// GroupProxyAdmins, in the same transaction. The last member of GroupAdmins
// stays (ErrLastAdministrator), and so does the Owner
// (ErrOwnerAdministrator).
func (s *Store) RemoveBuiltinMember(ctx context.Context, userID int64, group string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := userExists(ctx, tx, userID); err != nil {
			return err
		}
		if group != GroupAdmins {
			return removeMember(ctx, tx, userID, group)
		}
		in, err := isMember(ctx, tx, userID, GroupAdmins)
		if err != nil || !in {
			return err
		}
		var others int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_members m JOIN groups g ON g.id = m.group_id
			WHERE g.name = ? AND m.user_id <> ?`, GroupAdmins, userID).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdministrator
		}
		owner, err := ownerIn(ctx, tx)
		if err != nil {
			return err
		}
		if owner == userID {
			return ErrOwnerAdministrator
		}
		if err := removeMember(ctx, tx, userID, GroupProxyAdmins); err != nil {
			return err
		}
		return removeMember(ctx, tx, userID, GroupAdmins)
	})
}

func userExists(ctx context.Context, tx *sql.Tx, userID int64) error {
	var id int64
	return notFound(tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ?`, userID).Scan(&id))
}

func isMember(ctx context.Context, tx *sql.Tx, userID int64, group string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_members m JOIN groups g ON g.id = m.group_id
		WHERE g.name = ? AND m.user_id = ?`, group, userID).Scan(&n)
	return n > 0, err
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
		current, err := customGroupsOf(ctx, tx, userID)
		if err != nil {
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

// customGroupsOf returns the names of the custom groups a user belongs to.
// The rows are closed on return, before the caller runs further statements
// in the same transaction.
func customGroupsOf(ctx context.Context, tx *sql.Tx, userID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT g.name FROM group_members m JOIN groups g ON g.id = m.group_id WHERE m.user_id = ? AND g.builtin = 0`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
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
