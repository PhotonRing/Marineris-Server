package configmgr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/config"
	"github.com/photonring/marineris-server/internal/dnsprofiles"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

type fakeCaddy struct {
	mu        sync.Mutex
	loaded    [][]byte
	running   []byte
	failLoad  func(cfg []byte) error
	unhealthy func(running []byte) bool
}

func (f *fakeCaddy) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loaded = append(f.loaded, cfg)
	if f.failLoad != nil {
		if err := f.failLoad(cfg); err != nil {
			return err
		}
	}
	f.running = cfg
	return nil
}

func (f *fakeCaddy) Healthy(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unhealthy != nil && f.unhealthy(f.running) {
		return errors.New("probe failed")
	}
	return nil
}

type fakeDocker struct {
	validateCode int64
	output       string // of helper runs; "validation output" when empty
	restarts     int
	runs         []docker.RunSpec
	onRun        func(docker.RunSpec)
}

func (f *fakeDocker) Run(_ context.Context, spec docker.RunSpec) (int64, string, error) {
	f.runs = append(f.runs, spec)
	if f.onRun != nil {
		f.onRun(spec)
	}
	if f.output != "" {
		return f.validateCode, f.output, nil
	}
	return f.validateCode, "validation output", nil
}

func (f *fakeDocker) Action(_ context.Context, ref, action string) error {
	if action == "restart" {
		f.restarts++
	}
	return nil
}

func setup(t *testing.T) (*Manager, *store.Store, *fakeCaddy, *fakeDocker) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.LoadFrom(func(k string) string {
		if k == "MARINERIS_DATA_DIR" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(bytes.Repeat([]byte{5}, 32))
	if _, err := st.UpdateSettings(ctx, func(s *store.Settings) error {
		s.SetupComplete = true
		s.ACMEEmail = "ops@example.com"
		s.UIHost = "marineris.example.com"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p := &store.DNSProfile{Name: "OVH", Provider: "ovh"}
	pcfg := dnsprofiles.Config{"endpoint": "ovh-eu", "application_key": "ak", "application_secret": "super-secret-value", "consumer_key": "consumer-secret"}
	if err := st.CreateDNSProfile(ctx, p, func(id int64) (string, error) { return dnsprofiles.Seal(box, id, pcfg) }); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateDomain(ctx, &store.Domain{Name: "example.com", DNSProfileID: &p.ID, Wildcard: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(ctx, &store.User{Username: "admin", PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", WebAuthnID: []byte("x"), Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	fc, fd := &fakeCaddy{}, &fakeDocker{}
	m := New(cfg, st, box, fc, fd, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.HealthTimeout = 2 * time.Second
	m.AutheliaHealth = func(context.Context) error { return nil }
	return m, st, fc, fd
}

func TestApplyCaddyRecordsHistory(t *testing.T) {
	m, st, fc, _ := setup(t)
	ctx := context.Background()
	if err := m.ApplyCaddy(ctx, "admin", "first"); err != nil {
		t.Fatal(err)
	}
	if len(fc.loaded) != 1 || !strings.Contains(string(fc.running), "*.example.com") {
		t.Fatal("config not loaded")
	}
	vs, _ := st.ListConfigVersions(ctx, Caddy, 10)
	if len(vs) != 1 || vs[0].Status != store.ConfigApplied {
		t.Fatalf("%+v", vs)
	}
	if strings.Contains(vs[0].Content, "super-secret-value") {
		t.Fatal("history stored in clear text")
	}
	if err := m.ApplyCaddy(ctx, "admin", "second"); err != nil {
		t.Fatal(err)
	}
	vs, _ = st.ListConfigVersions(ctx, Caddy, 10)
	if vs[0].Status != store.ConfigApplied || vs[1].Status != store.ConfigSuperseded {
		t.Fatalf("statuses %s %s", vs[0].Status, vs[1].Status)
	}
}

func TestApplyCaddyRejectedKeepsOldConfig(t *testing.T) {
	m, st, fc, _ := setup(t)
	ctx := context.Background()
	_ = m.ApplyCaddy(ctx, "admin", "good")
	good := fc.running
	fc.failLoad = func([]byte) error { return errors.New("bad module") }
	err := m.ApplyCaddy(ctx, "admin", "bad")
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Stage != "apply" {
		t.Fatalf("expected apply error, got %v", err)
	}
	if !bytes.Equal(fc.running, good) {
		t.Fatal("running config changed")
	}
	last, _ := st.LastAppliedConfig(ctx, Caddy)
	vs, _ := st.ListConfigVersions(ctx, Caddy, 10)
	if vs[0].Status != store.ConfigFailed || last.ID != vs[1].ID {
		t.Fatal("failed attempt not recorded or last applied changed")
	}
}

func TestApplyCaddyUnhealthyRollsBack(t *testing.T) {
	m, st, fc, _ := setup(t)
	ctx := context.Background()
	if err := m.ApplyCaddy(ctx, "admin", "good"); err != nil {
		t.Fatal(err)
	}
	good := fc.running
	if _, err := st.UpdateSettings(ctx, func(s *store.Settings) error { s.ACMEStaging = false; return nil }); err != nil {
		t.Fatal(err)
	}
	// The new config "breaks" Caddy: the probe fails while it runs.
	fc.unhealthy = func(running []byte) bool { return !bytes.Equal(running, good) }
	err := m.ApplyCaddy(ctx, "admin", "breaks")
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Stage != "health" || !ae.RolledBack {
		t.Fatalf("expected rolled-back health error, got %v", err)
	}
	if !bytes.Equal(fc.running, good) {
		t.Fatal("previous config not restored")
	}
	vs, _ := st.ListConfigVersions(ctx, Caddy, 10)
	if vs[0].Status != store.ConfigRolledBack || vs[1].Status != store.ConfigApplied {
		t.Fatalf("statuses %s %s", vs[0].Status, vs[1].Status)
	}
}

func TestInvalidStateIsRefusedBeforeCaddy(t *testing.T) {
	m, st, fc, _ := setup(t)
	ctx := context.Background()
	if err := st.CreateRoute(ctx, &store.Route{Name: "x", Host: "x.other.org", Kind: store.RouteContainer, Target: store.RouteTarget{Container: "x", Port: 80}, Auth: store.AuthNone, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	err := m.ApplyCaddy(ctx, "admin", "bad route")
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Stage != "validate" {
		t.Fatalf("expected validate error, got %v", err)
	}
	if len(fc.loaded) != 0 {
		t.Fatal("invalid config sent to Caddy")
	}
}

func TestRollbackCaddy(t *testing.T) {
	m, st, fc, _ := setup(t)
	ctx := context.Background()
	_ = m.ApplyCaddy(ctx, "admin", "v1")
	v1 := fc.running
	_, _ = st.UpdateSettings(ctx, func(s *store.Settings) error { s.ACMEStaging = false; return nil })
	_ = m.ApplyCaddy(ctx, "admin", "v2")
	vs, _ := st.ListConfigVersions(ctx, Caddy, 10)
	if err := m.RollbackCaddy(ctx, "admin", vs[1].ID); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fc.running, v1) {
		t.Fatal("rollback did not restore v1")
	}
}

func TestApplyAutheliaValidatesAndRestarts(t *testing.T) {
	m, st, _, fd := setup(t)
	ctx := context.Background()
	if err := m.EnsureAutheliaFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyAuthelia(ctx, "admin", "first"); err != nil {
		t.Fatal(err)
	}
	if len(fd.runs) != 1 || fd.restarts != 1 {
		t.Fatalf("runs=%d restarts=%d", len(fd.runs), fd.restarts)
	}
	run := fd.runs[0]
	if run.Image == "" || !strings.Contains(strings.Join(run.Cmd, " "), "validate-config") {
		t.Fatalf("unexpected validator %+v", run)
	}
	if run.User == "" || strings.HasPrefix(run.User, "0:") {
		t.Fatalf("validator must not run as root: %q", run.User)
	}
	for _, b := range run.Binds {
		if !strings.HasSuffix(b, ":ro") {
			t.Fatalf("validator mount %q is writable", b)
		}
	}
	// Unchanged state: nothing to do.
	if err := m.ApplyAuthelia(ctx, "admin", "noop"); err != nil {
		t.Fatal(err)
	}
	if len(fd.runs) != 1 || fd.restarts != 1 {
		t.Fatal("unchanged config re-applied")
	}
	// Users-only change: validated, but no restart (Authelia watches it).
	if err := st.SetPasswordHash(ctx, 1, "$argon2id$v=19$m=65536,t=3,p=4$b3RoZXJzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyAuthelia(ctx, "admin", "password"); err != nil {
		t.Fatal(err)
	}
	if len(fd.runs) != 2 || fd.restarts != 1 {
		t.Fatalf("users change: runs=%d restarts=%d", len(fd.runs), fd.restarts)
	}
}

func TestApplyAutheliaInvalidKeepsFiles(t *testing.T) {
	m, _, _, fd := setup(t)
	ctx := context.Background()
	if err := m.EnsureAutheliaFiles(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.cfg.AutheliaDir(), "configuration.yml"))
	fd.validateCode = 1
	err := m.ApplyAuthelia(ctx, "admin", "bad")
	var ae *ApplyError
	if !errors.As(err, &ae) || ae.Stage != "validate" {
		t.Fatalf("expected validate error, got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(m.cfg.AutheliaDir(), "configuration.yml"))
	if !bytes.Equal(before, after) || fd.restarts != 0 {
		t.Fatal("invalid config installed")
	}
	// The directory stays (Docker Desktop cannot mount it again once
	// deleted), its files do not.
	cand := filepath.Join(m.cfg.AutheliaDir(), ".candidate")
	if st, err := os.Stat(cand); err != nil || !st.IsDir() {
		t.Fatalf("candidate directory deleted: %v", err)
	}
	for _, e := range mustReadDir(t, cand) {
		if !e.IsDir() {
			t.Fatalf("candidate file left behind: %s", e.Name())
		}
	}
}

func TestApplyAutheliaUnhealthyRestores(t *testing.T) {
	m, _, _, fd := setup(t)
	ctx := context.Background()
	if err := m.EnsureAutheliaFiles(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.cfg.AutheliaDir(), "configuration.yml"))
	calls := 0
	m.AutheliaHealth = func(context.Context) error {
		calls++
		// Unhealthy until the old config is restored (second restart).
		if fd.restarts < 2 {
			return errors.New("down")
		}
		return nil
	}
	err := m.ApplyAuthelia(ctx, "admin", "breaks")
	var ae *ApplyError
	if !errors.As(err, &ae) || !ae.RolledBack {
		t.Fatalf("expected rollback, got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(m.cfg.AutheliaDir(), "configuration.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("previous Authelia config not restored")
	}
}

func TestSecretFilePermissions(t *testing.T) {
	m, _, _, _ := setup(t)
	if err := m.EnsureAutheliaFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(m.cfg.AutheliaSecrets())
	// JWT, session and storage keys, and Valkey's password.
	if len(entries) != 4 {
		t.Fatalf("expected 4 secrets, got %d", len(entries))
	}
	for _, e := range entries {
		fi, _ := e.Info()
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s perms %o", e.Name(), fi.Mode().Perm())
		}
	}
}

func TestRedact(t *testing.T) {
	m, _, _, _ := setup(t)
	ctx := context.Background()
	cfg, err := m.RenderCaddy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "super-secret-value") {
		t.Fatal("precondition: secret should be in rendered config")
	}
	red := m.Redact(ctx, cfg)
	if strings.Contains(string(red), "super-secret-value") || strings.Contains(string(red), "consumer-secret") {
		t.Fatal("secret survived redaction")
	}
	users := []byte("users:\n  admin:\n    password: $argon2id$v=19$m=1,t=1,p=1$abc$def\n")
	if strings.Contains(string(m.Redact(ctx, users)), "argon2id") {
		t.Fatal("hash survived redaction")
	}
}

func TestCaddyListen(t *testing.T) {
	if got := caddyListen("unix:///run/caddy-admin/admin.sock"); got != "unix//run/caddy-admin/admin.sock" {
		t.Fatal(got)
	}
	if got := caddyListen("http://caddy:2019"); got != "caddy:2019" {
		t.Fatal(got)
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
