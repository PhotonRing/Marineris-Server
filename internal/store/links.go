package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Links are Invites and Reset links: one-time, expiring links whose token is
// the credential. Only the token's hash is stored.

// LinkTTL is how long a link stays usable.
const LinkTTL = 72 * time.Hour

// Link kinds.
const (
	LinkInvite = "invite"
	LinkReset  = "reset"
)

// Link is a pending, used or revoked Invite or Reset link.
type Link struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	// Invite: the reserved Username, the display name, e-mail and custom
	// Groups (IDs, so a renamed Group follows and a deleted one is dropped)
	// the User gets.
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email"`
	GroupIDs    []int64 `json:"-"`
	// Groups are the names of the Groups in GroupIDs that still exist.
	Groups []string `json:"groups"`
	// Reset: the User and what the link resets.
	UserID    int64     `json:"user_id,omitempty"`
	Resets    string    `json:"resets,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ErrUsernameTaken: a User already has the Username (ignoring case).
var ErrUsernameTaken = errors.New("store: username taken")

// ErrUsernameReserved: a pending Invite holds the Username.
var ErrUsernameReserved = errors.New("store: username reserved by a pending invite")

// CreateInvite stores an Invite with its token's hash. The Username must be
// free: neither a User's nor reserved by another pending Invite.
func (s *Store) CreateInvite(ctx context.Context, l *Link, tokenHash string) error {
	groups, err := json.Marshal(nonNil(l.GroupIDs))
	if err != nil {
		return err
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE username = ?`, l.Username).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrUsernameTaken
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_links
			WHERE kind = 'invite' AND username = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`,
			l.Username, now).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrUsernameReserved
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO user_links(kind, token_hash, username, display_name, email, groups, created_by, created_at, expires_at)
			VALUES ('invite', ?, ?, ?, ?, ?, ?, ?, ?)`,
			tokenHash, l.Username, l.DisplayName, l.Email, string(groups), l.CreatedBy, now, now.Add(LinkTTL))
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		l.ID, err = res.LastInsertId()
		l.Kind, l.CreatedAt, l.ExpiresAt = LinkInvite, now, now.Add(LinkTTL)
		return err
	})
}

// PendingLink returns the usable link whose token hashes to tokenHash:
// ErrNotFound when it is unknown, expired, used or revoked alike.
func (s *Store) PendingLink(ctx context.Context, tokenHash string) (*Link, error) {
	return s.onePendingLink(ctx, tokenHash, 0)
}

// GetPendingLink returns a usable link by ID (ErrNotFound otherwise).
func (s *Store) GetPendingLink(ctx context.Context, id int64) (*Link, error) {
	return s.onePendingLink(ctx, "", id)
}

func (s *Store) onePendingLink(ctx context.Context, tokenHash string, id int64) (*Link, error) {
	if tokenHash == "" && id == 0 {
		return nil, ErrNotFound
	}
	links, err := s.pendingLinks(ctx, tokenHash, id)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, ErrNotFound
	}
	return links[0], nil
}

// PendingLinks returns every usable link, oldest first.
func (s *Store) PendingLinks(ctx context.Context) ([]*Link, error) {
	return s.pendingLinks(ctx, "", 0)
}

// pendingLinks returns the usable links, filtered by token hash and by ID
// when they are set; each with the names of its Groups that still exist.
func (s *Store) pendingLinks(ctx context.Context, tokenHash string, id int64) ([]*Link, error) {
	links, err := s.scanPendingLinks(ctx, tokenHash, id)
	if err != nil {
		return nil, err
	}
	names, err := s.groupNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		l.Groups = []string{}
		for _, gid := range l.GroupIDs {
			if n, found := names[gid]; found {
				l.Groups = append(l.Groups, n)
			}
		}
	}
	return links, nil
}

// scanPendingLinks reads the links; the rows are closed on return, before
// the caller runs further queries on the single connection.
func (s *Store) scanPendingLinks(ctx context.Context, tokenHash string, id int64) ([]*Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, username, display_name, email, groups, COALESCE(user_id, 0), resets, created_by, created_at, expires_at
		FROM user_links
		WHERE (? = '' OR token_hash = ?) AND (? = 0 OR id = ?)
			AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?
		ORDER BY id`, tokenHash, tokenHash, id, id, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Link
	for rows.Next() {
		var l Link
		var groups string
		if err := rows.Scan(&l.ID, &l.Kind, &l.Username, &l.DisplayName, &l.Email, &groups, &l.UserID, &l.Resets, &l.CreatedBy, &l.CreatedAt, &l.ExpiresAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(groups), &l.GroupIDs); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// groupNames maps every Group's ID to its name.
func (s *Store) groupNames(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// GroupIDs returns the IDs of the named Groups (ErrNotFound when one does
// not exist) and whether any of them is built-in.
func (s *Store) GroupIDs(ctx context.Context, names []string) (ids []int64, builtin bool, err error) {
	for _, n := range names {
		var id int64
		var b bool
		if err := s.db.QueryRowContext(ctx, `SELECT id, builtin FROM groups WHERE name = ?`, n).Scan(&id, &b); err != nil {
			return nil, false, notFound(err)
		}
		ids = append(ids, id)
		builtin = builtin || b
	}
	return ids, builtin, nil
}

// RevokeLink ends a pending link (ErrNotFound when it is not pending).
func (s *Store) RevokeLink(ctx context.Context, id int64) error {
	now := s.now()
	return s.execOne(ctx, `UPDATE user_links SET revoked_at = ?
		WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`, now, id, now)
}

// useLink marks a pending link used, in tx: ErrNotFound when it was used,
// revoked or expired meanwhile, so a link works once even under races.
func (s *Store) useLink(ctx context.Context, tx *sql.Tx, id int64) error {
	now := s.now()
	res, err := tx.ExecContext(ctx, `UPDATE user_links SET used_at = ?
		WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`, now, id, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcceptInvite uses the pending Invite l and creates its User, with the
// Invite's names and the Groups that still exist, in one transaction. It
// fails with ErrNotFound when the Invite is no longer pending and with
// ErrUsernameTaken when a User got the Username meanwhile.
func (s *Store) AcceptInvite(ctx context.Context, l *Link, u *User) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := s.useLink(ctx, tx, l.ID); err != nil {
			return err
		}
		u.Username, u.DisplayName, u.Email = l.Username, l.DisplayName, l.Email
		if err := s.insertUser(ctx, tx, u); errors.Is(err, ErrConflict) {
			return ErrUsernameTaken
		} else if err != nil {
			return err
		}
		for _, gid := range l.GroupIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_members(user_id, group_id) SELECT ?, id FROM groups WHERE id = ?`, u.ID, gid); err != nil {
				return err
			}
		}
		return nil
	})
}
