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
	// Expired: a Reset link that can no longer be used but still blocks its
	// User until it is revoked or replaced. Invites are gone once expired.
	Expired bool `json:"expired"`
}

// What a Reset link resets.
const (
	ResetPassword     = "password"
	ResetSecondFactor = "second_factor"
	ResetBoth         = "both"
)

// ValidResets reports whether v says what a Reset link resets.
func ValidResets(v string) bool {
	return v == ResetPassword || v == ResetSecondFactor || v == ResetBoth
}

// ResetsPassword reports whether the Reset link sets a new password.
func (l *Link) ResetsPassword() bool { return l.Resets == ResetPassword || l.Resets == ResetBoth }

// ResetsSecondFactor reports whether the Reset link sets a new second factor.
func (l *Link) ResetsSecondFactor() bool {
	return l.Resets == ResetSecondFactor || l.Resets == ResetBoth
}

// unfinishedReset is the condition, on a users row, of a Reset link that
// blocks the User: neither used nor revoked, expired or not.
const unfinishedReset = `EXISTS (SELECT 1 FROM user_links r WHERE r.user_id = users.id AND r.kind = 'reset' AND r.used_at IS NULL AND r.revoked_at IS NULL)`

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
	if tokenHash == "" {
		return nil, ErrNotFound
	}
	return s.onePendingLink(ctx, linkFilter{tokenHash: tokenHash})
}

// GetPendingLink returns a pending link by ID, a usable one or a Reset link
// still blocking its User once expired (ErrNotFound otherwise).
func (s *Store) GetPendingLink(ctx context.Context, id int64) (*Link, error) {
	if id == 0 {
		return nil, ErrNotFound
	}
	return s.onePendingLink(ctx, linkFilter{id: id, blocking: true})
}

// UserReset returns the Reset link blocking the User, expired or not
// (ErrNotFound when there is none).
func (s *Store) UserReset(ctx context.Context, userID int64) (*Link, error) {
	if userID == 0 {
		return nil, ErrNotFound
	}
	return s.onePendingLink(ctx, linkFilter{userID: userID, blocking: true})
}

func (s *Store) onePendingLink(ctx context.Context, f linkFilter) (*Link, error) {
	links, err := s.pendingLinks(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, ErrNotFound
	}
	return links[0], nil
}

// PendingLinks returns every usable link, and the expired Reset links still
// blocking their User, oldest first.
func (s *Store) PendingLinks(ctx context.Context) ([]*Link, error) {
	return s.pendingLinks(ctx, linkFilter{blocking: true})
}

// linkFilter selects pending links: by token hash, ID and User when they are
// set; blocking adds the expired Reset links that still block their User.
type linkFilter struct {
	tokenHash string
	id        int64
	userID    int64
	blocking  bool
}

// pendingLinks returns the pending links matching f, each with the names of
// its Groups that still exist.
func (s *Store) pendingLinks(ctx context.Context, f linkFilter) ([]*Link, error) {
	links, err := s.scanPendingLinks(ctx, f)
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
func (s *Store) scanPendingLinks(ctx context.Context, f linkFilter) ([]*Link, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, username, display_name, email, groups, COALESCE(user_id, 0), resets, created_by, created_at, expires_at, expires_at <= ?
		FROM user_links
		WHERE (? = '' OR token_hash = ?) AND (? = 0 OR id = ?) AND (? = 0 OR user_id = ?)
			AND used_at IS NULL AND revoked_at IS NULL AND (expires_at > ? OR (? AND kind = 'reset'))
		ORDER BY id`, now, f.tokenHash, f.tokenHash, f.id, f.id, f.userID, f.userID, now, f.blocking)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Link
	for rows.Next() {
		var l Link
		var groups string
		if err := rows.Scan(&l.ID, &l.Kind, &l.Username, &l.DisplayName, &l.Email, &groups, &l.UserID, &l.Resets, &l.CreatedBy, &l.CreatedAt, &l.ExpiresAt, &l.Expired); err != nil {
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

// RevokeLink ends a pending link, or an expired Reset link still blocking
// its User (ErrNotFound otherwise).
func (s *Store) RevokeLink(ctx context.Context, id int64) error {
	now := s.now()
	return s.execOne(ctx, `UPDATE user_links SET revoked_at = ?
		WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL AND (expires_at > ? OR kind = 'reset')`, now, id, now)
}

// CreateReset stores a Reset link for the User l.UserID with its token's
// hash. In the same transaction it revokes the User's previous unfinished
// Reset link, which the new one replaces, and ends the User's sessions: from
// now on they cannot sign in until the link is used or revoked.
func (s *Store) CreateReset(ctx context.Context, l *Link, tokenHash string) error {
	if !ValidResets(l.Resets) {
		return errors.New("store: invalid reset kind")
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT username, display_name FROM users WHERE id = ?`, l.UserID).Scan(&l.Username, &l.DisplayName); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE user_links SET revoked_at = ?
			WHERE kind = 'reset' AND user_id = ? AND used_at IS NULL AND revoked_at IS NULL`, now, l.UserID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO user_links(kind, token_hash, username, display_name, user_id, resets, created_by, created_at, expires_at)
			VALUES ('reset', ?, ?, ?, ?, ?, ?, ?, ?)`,
			tokenHash, l.Username, l.DisplayName, l.UserID, l.Resets, l.CreatedBy, now, now.Add(LinkTTL))
		if isUnique(err) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if l.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		l.Kind, l.CreatedAt, l.ExpiresAt, l.Groups = LinkReset, now, now.Add(LinkTTL), []string{}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, l.UserID)
		return err
	})
}

// ResetChange is what using a Reset link sets: the new password hash when it
// resets the password, the new sealed authenticator secret (with the step of
// the code that confirmed it) when it resets the second factor. A second
// factor reset to a passkey (registered in Authelia, not here) sets Passkey
// and no authenticator: the old one is removed.
type ResetChange struct {
	PasswordHash string
	TOTPSecret   string
	TOTPLastStep int64
	Passkey      bool
}

// resetsSecondFactor reports whether the change sets a new second factor.
func (c ResetChange) resetsSecondFactor() bool {
	return c.TOTPSecret != "" || c.Passkey
}

// UseReset uses the pending Reset link l and sets what it resets, in one
// transaction (ErrNotFound when the link is no longer usable). A new second
// factor replaces every old one: the recovery codes and the Marineris
// passkeys go, and the authenticator must be registered in Authelia again.
func (s *Store) UseReset(ctx context.Context, l *Link, c ResetChange) error {
	if l.Kind != LinkReset || (l.ResetsPassword() != (c.PasswordHash != "")) || (l.ResetsSecondFactor() != c.resetsSecondFactor()) || (c.Passkey && c.TOTPSecret != "") {
		return errors.New("store: the change does not match the Reset link")
	}
	now := s.now()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := s.useLink(ctx, tx, l.ID); err != nil {
			return err
		}
		if c.PasswordHash != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, c.PasswordHash, now, l.UserID); err != nil {
				return err
			}
		}
		if !c.resetsSecondFactor() {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret = ?, totp_last_step = ?, authelia_totp = '', recovery_codes = '[]', updated_at = ? WHERE id = ?`,
			c.TOTPSecret, c.TOTPLastStep, now, l.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE user_id = ?`, l.UserID)
		return err
	})
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
