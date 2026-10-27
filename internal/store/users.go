package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// User is an administrator of Marineris.
type User struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	PasswordHash  string    `json:"-"`
	TOTPSecret    string    `json:"-"` // sealed
	TOTPLastStep  int64     `json:"-"`
	RecoveryCodes []string  `json:"-"` // hashes of unused codes
	WebAuthnID    []byte    `json:"-"`
	Role          string    `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const userCols = `id, username, password_hash, totp_secret, totp_last_step, recovery_codes, webauthn_id, role, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var codes string
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.TOTPSecret, &u.TOTPLastStep, &codes, &u.WebAuthnID, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if err := json.Unmarshal([]byte(codes), &u.RecoveryCodes); err != nil {
		return nil, err
	}
	return &u, nil
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user and returns its ID.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	codes, err := json.Marshal(nonNil(u.RecoveryCodes))
	if err != nil {
		return err
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, totp_secret, totp_last_step, recovery_codes, webauthn_id, role, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.TOTPSecret, u.TOTPLastStep, string(codes), u.WebAuthnID, u.Role, now, now)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	u.CreatedAt, u.UpdatedAt = now, now
	return err
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
