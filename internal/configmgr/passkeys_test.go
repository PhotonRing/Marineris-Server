package configmgr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/store"
)

// autheliaSchema is the part of Authelia's database Marineris uses (Authelia
// 4.39, sqlite).
const autheliaSchema = `
CREATE TABLE webauthn_credentials (
    id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME NULL DEFAULT NULL,
    rpid VARCHAR(512) NOT NULL,
    username VARCHAR(100) NOT NULL,
    description VARCHAR(64) NOT NULL,
    kid VARCHAR(512) NOT NULL,
    aaguid CHAR(36) NULL,
    attestation_type VARCHAR(32),
    attachment VARCHAR(64),
    transport VARCHAR(64) DEFAULT '',
    sign_count INTEGER DEFAULT 0,
    clone_warning BOOLEAN NOT NULL DEFAULT FALSE,
    legacy BOOLEAN NOT NULL DEFAULT FALSE,
    discoverable BOOLEAN NOT NULL,
    present BOOLEAN NOT NULL DEFAULT FALSE,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state BOOLEAN NOT NULL DEFAULT FALSE,
    public_key BLOB NOT NULL,
    attestation BLOB NULL DEFAULT NULL,
    attestation_format VARCHAR(32) NOT NULL DEFAULT ''
);
CREATE TABLE webauthn_users (
    id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    rpid VARCHAR(512) NOT NULL,
    username VARCHAR(100) NOT NULL,
    userid CHAR(64) NOT NULL
);
CREATE UNIQUE INDEX webauthn_users_lookup_key ON webauthn_users (rpid, username);`

func autheliaDB(t *testing.T, m *Manager) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(m.cfg.AutheliaDataDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+m.autheliaDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(autheliaSchema); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAutheliaPasskeysBeforeAutheliaStarted(t *testing.T) {
	m, _, _, _ := setup(t)
	ctx := context.Background()
	if list, err := m.AutheliaPasskeys(ctx, "admin"); err != nil || len(list) != 0 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := m.AutheliaUserHandle(ctx, "auth.example.com", "admin"); !errors.Is(err, ErrAutheliaNotReady) {
		t.Fatalf("handle: %v", err)
	}
}

func TestAddAutheliaPasskey(t *testing.T) {
	m, _, _, fd := setup(t)
	ctx := context.Background()
	db := autheliaDB(t, m)

	// A user without passkeys gets a new handle, like Authelia's.
	h, err := m.AutheliaUserHandle(ctx, "auth.example.com", "admin")
	if err != nil || len(h) != 64 {
		t.Fatalf("new handle: %q %v", h, err)
	}

	var imported map[string][]map[string]any
	fd.onRun = func(spec docker.RunSpec) {
		file := spec.Cmd[len(spec.Cmd)-3]
		b, err := os.ReadFile(filepath.Join(m.cfg.AutheliaDataDir(), strings.TrimPrefix(file, "/data/")))
		if err != nil {
			t.Fatalf("import file: %v", err)
		}
		if err := json.Unmarshal(b, &imported); err != nil {
			t.Fatal(err)
		}
	}
	fd.output = "imported"
	cred := &webauthn.Credential{
		ID: []byte{1, 2, 3}, PublicKey: []byte{4, 5}, AttestationType: "none", AttestationFormat: "none",
		Transport: []protocol.AuthenticatorTransport{"internal", "hybrid"},
		Flags:     webauthn.CredentialFlags{UserPresent: true, UserVerified: true, BackupEligible: true, BackupState: true},
		Authenticator: webauthn.Authenticator{AAGUID: []byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4},
			Attachment: "platform"},
	}
	if err := m.AddAutheliaPasskey(ctx, NewAutheliaPasskey{RPID: "auth.example.com", Username: "admin", UserHandle: h,
		Name: "Laptop", Credential: cred, Discoverable: true}); err != nil {
		t.Fatal(err)
	}
	run := fd.runs[len(fd.runs)-1]
	if strings.Join(run.Cmd[:5], " ") != "authelia storage user webauthn import" {
		t.Fatalf("command: %v", run.Cmd)
	}
	c := imported["webauthn_credentials"][0]
	want := map[string]any{"rpid": "auth.example.com", "username": "admin", "description": "Laptop", "kid": "AQID",
		"public_key": "BAU=", "discoverable": true, "verified": true, "backup_state": true, "attachment": "platform",
		"aaguid": "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4", "attestation_type": "none"}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %v, want %v", k, c[k], v)
		}
	}
	if entries, _ := os.ReadDir(m.cfg.AutheliaDataDir()); len(entries) != 1 {
		t.Fatalf("import file left behind: %v", entries)
	}

	// The handle is stored, and used from now on.
	if h2, err := m.AutheliaUserHandle(ctx, "auth.example.com", "admin"); err != nil || h2 != h {
		t.Fatalf("stored handle: %q %v", h2, err)
	}
	// A handle that changed meanwhile (first passkey added in the portal) is
	// refused.
	if err := m.AddAutheliaPasskey(ctx, NewAutheliaPasskey{RPID: "auth.example.com", Username: "admin", UserHandle: "other",
		Name: "Phone", Credential: cred}); err == nil {
		t.Fatal("changed handle accepted")
	}

	// Listing and removing read Authelia's own rows.
	if _, err := db.Exec(`INSERT INTO webauthn_credentials (created_at, last_used_at, rpid, username, description, kid, discoverable, public_key)
		VALUES ('2026-10-28 10:00:00.123456789+00:00', NULL, 'auth.example.com', 'admin', 'Laptop', 'AQID', 1, x'00'),
		       ('2026-10-28 11:00:00+00:00', '2026-10-28 12:00:00+00:00', 'auth.other.org', 'admin', 'Key', 'BAU=', 1, x'00'),
		       ('2026-10-28 11:00:00+00:00', NULL, 'auth.example.com', 'bob', 'Bob', 'Bg==', 1, x'00')`); err != nil {
		t.Fatal(err)
	}
	list, err := m.AutheliaPasskeys(ctx, "admin")
	if err != nil || len(list) != 2 || list[0].Name != "Laptop" || list[0].KID != "AQID" || list[0].LastUsedAt != nil ||
		list[1].Host != "auth.other.org" || list[1].LastUsedAt == nil || list[0].CreatedAt.Hour() != 10 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := m.DeleteAutheliaPasskey(ctx, "admin", 3); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleting another user's passkey: %v", err)
	}
	if err := m.DeleteAutheliaPasskey(ctx, "admin", list[0].ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.AutheliaPasskeys(ctx, "admin"); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
}

// Deleting a User removes everything Authelia keeps for their Username, so a
// later User with the same Username inherits no passkey or authenticator.
func TestDeleteAutheliaUser(t *testing.T) {
	m, _, _, _ := setup(t)
	ctx := context.Background()
	if err := m.DeleteAutheliaUser(ctx, "carol"); !errors.Is(err, ErrAutheliaNotReady) {
		t.Fatalf("before Authelia started: %v", err)
	}
	db := autheliaDB(t, m)
	if _, err := db.Exec(`
CREATE TABLE totp_configurations (id INTEGER PRIMARY KEY, username VARCHAR(100) NOT NULL, secret BLOB NOT NULL);
INSERT INTO totp_configurations (username, secret) VALUES ('carol', x'01'), ('bob', x'02');
INSERT INTO webauthn_users (rpid, username, userid) VALUES ('auth.example.com', 'carol', 'h1'), ('auth.example.com', 'bob', 'h2');
INSERT INTO webauthn_credentials (rpid, username, description, kid, discoverable, public_key)
	VALUES ('auth.example.com', 'carol', 'Phone', 'AQ==', 1, x'00'), ('auth.example.com', 'bob', 'Key', 'Ag==', 1, x'00');`); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAutheliaUser(ctx, "carol"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"totp_configurations", "webauthn_users", "webauthn_credentials"} {
		var carol, bob int
		_ = db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE username = 'carol'`).Scan(&carol)
		_ = db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE username = 'bob'`).Scan(&bob)
		if carol != 0 || bob != 1 {
			t.Errorf("%s: carol=%d bob=%d", table, carol, bob)
		}
	}
}
