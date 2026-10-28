package configmgr

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/photonring/marineris-server/internal/authelia"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/store"
)

// Passkeys that sign in through Authelia are Authelia's: they belong to its
// relying party (the portal's host) and live in its database. Marineris adds
// them for the panel: it runs the WebAuthn ceremony itself, on a page served
// on the portal's host, then imports the credential with Authelia's own
// command ("storage user webauthn import", which encrypts the public key with
// Authelia's storage key). Marineris only reads Authelia's database, except
// for two rows Authelia's command cannot write: the user handle of a user's
// first passkey, and deleting a passkey.

// ErrAutheliaNotReady: Authelia has not created its database yet.
var ErrAutheliaNotReady = errors.New("authelia has not started yet")

// AutheliaPasskey is a passkey registered in Authelia.
type AutheliaPasskey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Host       string     `json:"host"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// KID is the credential ID, base64 (standard encoding), as Authelia
	// stores it.
	KID string `json:"-"`
}

// NewAutheliaPasskey is a verified credential to register in Authelia.
type NewAutheliaPasskey struct {
	RPID       string
	Username   string
	UserHandle string // the user's WebAuthn ID in Authelia
	Name       string
	Credential *webauthn.Credential
	// Discoverable: the authenticator keeps the credential (passkey
	// sign-in, without a username, only uses these).
	Discoverable bool
}

func (m *Manager) autheliaDBPath() string {
	return filepath.Join(m.cfg.AutheliaDataDir(), "db.sqlite3")
}

func (m *Manager) autheliaDB() (*sql.DB, error) {
	p := m.autheliaDBPath()
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		return nil, ErrAutheliaNotReady
	}
	// Authelia writes the same file: wait for its locks rather than fail.
	return sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(5000)")
}

// AutheliaPasskeys lists the passkeys of username, on every portal host.
func (m *Manager) AutheliaPasskeys(ctx context.Context, username string) ([]AutheliaPasskey, error) {
	db, err := m.autheliaDB()
	if errors.Is(err, ErrAutheliaNotReady) {
		return []AutheliaPasskey{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT id, description, rpid, kid, created_at, last_used_at
		FROM webauthn_credentials WHERE username = ? ORDER BY created_at, id`, username)
	if err != nil {
		return nil, fmt.Errorf("reading Authelia's passkeys: %w", err)
	}
	defer rows.Close()
	out := []AutheliaPasskey{}
	for rows.Next() {
		var p AutheliaPasskey
		var created, used sqlTime
		if err := rows.Scan(&p.ID, &p.Name, &p.Host, &p.KID, &created, &used); err != nil {
			return nil, err
		}
		p.CreatedAt = created.t
		if used.ok {
			t := used.t
			p.LastUsedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AutheliaUserHandle returns the WebAuthn ID Authelia gave username on rpid,
// or a new one when the user has none yet there (it is stored with the
// user's first passkey, by AddAutheliaPasskey).
func (m *Manager) AutheliaUserHandle(ctx context.Context, rpid, username string) (string, error) {
	db, err := m.autheliaDB()
	if err != nil {
		return "", err
	}
	defer db.Close()
	var id string
	err = db.QueryRowContext(ctx, `SELECT userid FROM webauthn_users WHERE rpid = ? AND username = ?`, rpid, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return newUserHandle()
	}
	if err != nil {
		return "", fmt.Errorf("reading Authelia's passkey users: %w", err)
	}
	return id, nil
}

// newUserHandle returns 64 random characters, like Authelia's own.
func newUserHandle() (string, error) {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b), nil
}

// AddAutheliaPasskey registers a verified passkey in Authelia.
func (m *Manager) AddAutheliaPasskey(ctx context.Context, p NewAutheliaPasskey) error {
	db, err := m.autheliaDB()
	if err != nil {
		return err
	}
	defer db.Close()
	// Passkey sign-in finds the user by the handle the passkey returns.
	if _, err := db.ExecContext(ctx, `INSERT INTO webauthn_users (rpid, username, userid) VALUES (?, ?, ?)
		ON CONFLICT (rpid, username) DO NOTHING`, p.RPID, p.Username, p.UserHandle); err != nil {
		return fmt.Errorf("registering the passkey user in Authelia: %w", err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT userid FROM webauthn_users WHERE rpid = ? AND username = ?`,
		p.RPID, p.Username).Scan(&stored); err != nil {
		return err
	}
	if stored != p.UserHandle {
		// A first passkey was added in the portal meanwhile.
		return errors.New("the passkey user changed in Authelia meanwhile: add the passkey again")
	}

	export, err := json.Marshal(map[string]any{"webauthn_credentials": []any{importCredential(p, time.Now().UTC())}})
	if err != nil {
		return err
	}
	// JSON is YAML: the file is read by "storage user webauthn import".
	name := "marineris-import-" + randomHex(8) + ".yml"
	path := filepath.Join(m.cfg.AutheliaDataDir(), name)
	if err := os.WriteFile(path, export, 0o600); err != nil {
		return err
	}
	defer os.Remove(path)

	m.mu.Lock()
	defer m.mu.Unlock()
	code, out, err := m.docker.Run(ctx, docker.RunSpec{
		Image: m.cfg.AutheliaImage,
		User:  helperUser(),
		Cmd: []string{"authelia", "storage", "user", "webauthn", "import", authelia.DataDir + "/" + name,
			"--config", "/config/" + autheliaConfigFile},
		Env: authelia.Env("/secrets"),
		Binds: []string{
			m.cfg.AutheliaDir() + ":/config:ro",
			m.cfg.AutheliaSecrets() + ":/secrets:ro",
			m.cfg.AutheliaDataDir() + ":" + authelia.DataDir,
		},
	})
	if err == nil && code != 0 {
		err = errors.New(lastLines(out, 3))
	}
	if err != nil {
		return fmt.Errorf("importing the passkey into Authelia: %w", err)
	}
	m.log.Info("passkey registered in Authelia", "user", p.Username, "rpid", p.RPID)
	return nil
}

// importCredential is the credential in the format of Authelia's export
// files.
func importCredential(p NewAutheliaPasskey, now time.Time) map[string]any {
	c := p.Credential
	transports := []string{}
	for _, t := range c.Transport {
		transports = append(transports, string(t))
	}
	out := map[string]any{
		"created_at":         now.Format(time.RFC3339Nano),
		"rpid":               p.RPID,
		"username":           p.Username,
		"description":        p.Name,
		"kid":                base64.StdEncoding.EncodeToString(c.ID),
		"attestation_type":   c.AttestationType,
		"attestation_format": c.AttestationFormat,
		"attachment":         string(c.Authenticator.Attachment),
		"transports":         transports,
		"sign_count":         c.Authenticator.SignCount,
		"clone_warning":      c.Authenticator.CloneWarning,
		"legacy":             false,
		"discoverable":       p.Discoverable,
		"present":            c.Flags.UserPresent,
		"verified":           c.Flags.UserVerified,
		"backup_eligible":    c.Flags.BackupEligible,
		"backup_state":       c.Flags.BackupState,
		"public_key":         base64.StdEncoding.EncodeToString(c.PublicKey),
	}
	if len(c.Authenticator.AAGUID) == 16 {
		h := hex.EncodeToString(c.Authenticator.AAGUID)
		out["aaguid"] = h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	}
	return out
}

// DeleteAutheliaPasskey removes one of username's passkeys from Authelia.
func (m *Manager) DeleteAutheliaPasskey(ctx context.Context, username string, id int64) error {
	db, err := m.autheliaDB()
	if err != nil {
		return err
	}
	defer db.Close()
	res, err := db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND username = ?`, id, username)
	if err != nil {
		return fmt.Errorf("removing the passkey from Authelia: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sqlTime reads Authelia's timestamps, whatever form the driver returns.
type sqlTime struct {
	t  time.Time
	ok bool
}

func (s *sqlTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		s.t, s.ok = x.UTC(), true
		return nil
	case []byte:
		return s.parse(string(x))
	case string:
		return s.parse(x)
	case int64:
		s.t, s.ok = time.Unix(x, 0).UTC(), true
		return nil
	}
	return fmt.Errorf("unexpected time value %T", v)
}

func (s *sqlTime) parse(v string) error {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, v); err == nil {
			s.t, s.ok = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("unexpected time value %q", v)
}
