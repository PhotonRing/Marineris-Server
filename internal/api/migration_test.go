package api

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
)

// seed01x writes a Marineris 0.1.x database: two administrators (the oldest,
// testUser, with an authenticator, a passkey and an open session) and
// returns testUser's TOTP secret and session cookie.
func seed01x(t *testing.T, path string) (secret, cookie string) {
	t.Helper()
	ctx := context.Background()
	schema, err := os.ReadFile("testdata/schema-0.1.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(ctx, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	secret = auth.NewTOTPSecret()
	handle := []byte("admin-webauthn-handle")
	sealed, err := testBox().Seal([]byte(secret), setup.TOTPContext(handle))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The later administrator is inserted first: the Owner is the lowest
	// ID, not the first row.
	exec(`INSERT INTO users(id, username, password_hash, totp_secret, webauthn_id, role, created_at, updated_at) VALUES (7, 'bob', ?, '', ?, 'admin', ?, ?)`,
		hash, []byte("bob-handle"), now, now)
	exec(`INSERT INTO users(id, username, password_hash, totp_secret, webauthn_id, role, authelia_totp, created_at, updated_at) VALUES (3, ?, ?, ?, ?, 'admin', 'fp', ?, ?)`,
		testUser, hash, sealed, handle, now.Add(-time.Hour), now)
	exec(`INSERT INTO webauthn_credentials(id, user_id, name, data, created_at) VALUES (?, 3, 'Laptop', '{}', ?)`, []byte("cred-1"), now)
	cookie = strings.Repeat("s", 43)
	exec(`INSERT INTO sessions(id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, auth_at, method) VALUES (?, 3, 'csrf', ?, ?, ?, ?, 'totp')`,
		auth.HashToken(cookie), now, now, now.Add(time.Hour), now)
	return secret, cookie
}

func TestUpgradeFrom01KeepsEveryoneAndTheirCredentials(t *testing.T) {
	var secret, cookie string
	e := newEnvWith(t, func(path string) { secret, cookie = seed01x(t, path) })
	if _, err := e.store.UpdateSettings(context.Background(), func(s *store.Settings) error {
		s.SetupComplete, s.ACMEEmail, s.UIHost, s.UIProtectWithAuthelia = true, "ops@example.com", uiHost, false
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The session opened before the upgrade still works, and its User is
	// now the Owner and an Administrator.
	old := e.edgeClient()
	old.cookie = cookie
	if got, want := rightsOf(t, old.do("GET", "/api/auth/session", nil)), "admin admin groups=[admins] admin=true proxy=false owner=true"; got != want {
		t.Fatalf("old session:\n got  %s\n want %s", got, want)
	}

	// Password and authenticator still sign in; the passkey is still there.
	c := e.edgeClient()
	if r := e.login(c, secret); r.Status != 200 {
		t.Fatalf("login after upgrade: %d %s", r.Status, r.Raw)
	}
	keys := c.do("GET", "/api/account/passkeys", nil)
	if keys.Status != 200 || !strings.Contains(string(keys.Raw), "Laptop") {
		t.Fatalf("passkeys after upgrade: %d %s", keys.Status, keys.Raw)
	}

	// Every other User is an Administrator too, but not the Owner.
	e.setAutheliaProtection(true)
	if got, want := rightsOf(t, e.autheliaClient("bob").do("GET", "/api/auth/session", nil)), "bob bob groups=[admins] admin=true proxy=false owner=false"; got != want {
		t.Fatalf("bob:\n got  %s\n want %s", got, want)
	}
}
