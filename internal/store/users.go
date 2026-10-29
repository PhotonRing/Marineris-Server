package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// User is a person who can sign in. What they may do depends only on their
// Groups (see IsAdministrator, IsProxyAdmin) and on whether they are the
// Owner (OwnerID).
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Disabled    bool   `json:"disabled"`
	// Groups are the names of the user's groups, sorted. Read-only: set
	// through AddGroupMember.
	Groups       []string `json:"groups"`
	PasswordHash string   `json:"-"`
	TOTPSecret   string `json:"-"` // sealed
	TOTPLastStep int64  `json:"-"`
	// AutheliaTOTP is the fingerprint of the TOTP secret last registered in
	// Authelia (empty: none yet).
	AutheliaTOTP  string    `json:"-"`
	RecoveryCodes []string  `json:"-"` // hashes of unused codes
	WebAuthnID    []byte    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// The built-in groups. Members of GroupAdmins are Administrators; members of
// both are Proxy & SSO Administrators.
const (
	GroupAdmins      = "admins"
	GroupProxyAdmins = "proxy-admins"
)

// IsAdministrator reports whether the user is a member of GroupAdmins.
func (u *User) IsAdministrator() bool { return slices.Contains(u.Groups, GroupAdmins) }

// IsProxyAdmin reports whether the user is a Proxy & SSO Administrator: an
// Administrator who is also in GroupProxyAdmins.
func (u *User) IsProxyAdmin() bool {
	return u.IsAdministrator() && slices.Contains(u.Groups, GroupProxyAdmins)
}

// userCols reads the user's groups in the same query, one name per line.
const userCols = `id, username, display_name, email, disabled, password_hash, totp_secret, totp_last_step, authelia_totp, recovery_codes, webauthn_id, created_at, updated_at,
	(SELECT COALESCE(group_concat(g.name, char(10)), '') FROM group_members m JOIN groups g ON g.id = m.group_id WHERE m.user_id = users.id)`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var codes, groups string
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Disabled, &u.PasswordHash, &u.TOTPSecret, &u.TOTPLastStep, &u.AutheliaTOTP, &codes, &u.WebAuthnID, &u.CreatedAt, &u.UpdatedAt, &groups); err != nil {
		return nil, notFound(err)
	}
	if err := json.Unmarshal([]byte(codes), &u.RecoveryCodes); err != nil {
		return nil, err
	}
	u.Groups = []string{}
	if groups != "" {
		u.Groups = strings.Split(groups, "\n")
		slices.Sort(u.Groups)
	}
	return &u, nil
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user, in no group, and sets its ID. The display name
// defaults to the username.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return s.insertUser(ctx, tx, u) })
}

func (s *Store) insertUser(ctx context.Context, tx *sql.Tx, u *User) error {
	codes, err := json.Marshal(nonNil(u.RecoveryCodes))
	if err != nil {
		return err
	}
	if u.DisplayName == "" {
		u.DisplayName = u.Username
	}
	now := s.now()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, disabled, password_hash, totp_secret, totp_last_step, recovery_codes, webauthn_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.DisplayName, u.Email, u.Disabled, u.PasswordHash, u.TOTPSecret, u.TOTPLastStep, string(codes), u.WebAuthnID, now, now)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	u.CreatedAt, u.UpdatedAt = now, now
	u.Groups = []string{}
	return err
}

// CreateOwner creates the first user, as a member of GroupAdmins and as the
// Owner. It fails with ErrConflict when any user already exists.
func (s *Store) CreateOwner(ctx context.Context, u *User) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return ErrConflict
		}
		if err := s.insertUser(ctx, tx, u); err != nil {
			return err
		}
		if err := addMember(ctx, tx, u.ID, GroupAdmins); err != nil {
			return err
		}
		u.Groups = []string{GroupAdmins}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)`, ownerKey, strconv.FormatInt(u.ID, 10))
		if isUnique(err) {
			return ErrConflict
		}
		return err
	})
}

// ownerKey is the settings row holding the Owner's user ID. It is kept out
// of Settings so that saving the settings can never change it.
const ownerKey = "owner_user_id"

// OwnerID returns the Owner's user ID, or 0 before first-run setup.
func (s *Store) OwnerID(ctx context.Context) (int64, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, ownerKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// DeleteUser removes a user with their sessions, passkeys and memberships.
// It does not check who the user is: callers refuse deleting the Owner.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	return s.execOne(ctx, `DELETE FROM users WHERE id = ?`, id)
}

// GetUser returns the user with the given ID.
func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetUserByName returns the user with the given username (case-insensitive).
func (s *Store) GetUserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, name))
}

// GetUserByWebAuthnID returns the user owning the given WebAuthn user handle.
func (s *Store) GetUserByWebAuthnID(ctx context.Context, handle []byte) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE webauthn_id = ?`, handle))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPasswordHash replaces a user's password hash.
func (s *Store) SetPasswordHash(ctx context.Context, id int64, hash string) error {
	return s.execOne(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, s.now(), id)
}

// SetTOTP replaces a user's sealed TOTP secret and resets the replay counter.
func (s *Store) SetTOTP(ctx context.Context, id int64, sealed string) error {
	return s.execOne(ctx, `UPDATE users SET totp_secret = ?, totp_last_step = 0, updated_at = ? WHERE id = ?`, sealed, s.now(), id)
}

// SetAutheliaTOTP records the fingerprint of the TOTP secret registered in
// Authelia for a user.
func (s *Store) SetAutheliaTOTP(ctx context.Context, id int64, fingerprint string) error {
	return s.execOne(ctx, `UPDATE users SET authelia_totp = ? WHERE id = ?`, fingerprint, id)
}

// AdvanceTOTPStep records that the TOTP code for step was used. It only
// succeeds when step is newer than the last accepted step, which makes every
// code single-use even under concurrent requests.
func (s *Store) AdvanceTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`, step, id, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SetRecoveryCodes replaces the hashes of the user's recovery codes.
func (s *Store) SetRecoveryCodes(ctx context.Context, id int64, hashes []string) error {
	b, err := json.Marshal(nonNil(hashes))
	if err != nil {
		return err
	}
	return s.execOne(ctx, `UPDATE users SET recovery_codes = ?, updated_at = ? WHERE id = ?`, string(b), s.now(), id)
}

// ConsumeRecoveryCode removes hash from the user's recovery codes. It reports
// false when the code was not present (already used or never issued).
func (s *Store) ConsumeRecoveryCode(ctx context.Context, id int64, match func(hash string) bool) (bool, error) {
	var ok bool
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT recovery_codes FROM users WHERE id = ?`, id).Scan(&raw); err != nil {
			return notFound(err)
		}
		var codes []string
		if err := json.Unmarshal([]byte(raw), &codes); err != nil {
			return err
		}
		for i, h := range codes {
			if match(h) {
				codes = append(codes[:i], codes[i+1:]...)
				ok = true
				break
			}
		}
		if !ok {
			return nil
		}
		b, _ := json.Marshal(nonNil(codes))
		_, err := tx.ExecContext(ctx, `UPDATE users SET recovery_codes = ? WHERE id = ?`, string(b), id)
		return err
	})
	return ok, err
}

func (s *Store) execOne(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
