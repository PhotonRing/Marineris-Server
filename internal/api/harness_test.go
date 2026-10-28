package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/config"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
)

const (
	testUser     = "admin"
	testPassword = "Valles-Marineris-2026!"
	uiHost       = "marineris.example.com"
)

type fakeCaddy struct {
	mu      sync.Mutex
	fail    error
	loads   int
	running []byte
}

func (f *fakeCaddy) Load(_ context.Context, cfg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if f.fail != nil {
		return f.fail
	}
	f.running = cfg
	return nil
}
func (f *fakeCaddy) Healthy(context.Context) error { return nil }

// settledLoads waits for the server's background applies, then returns how
// many configurations were sent to Caddy.
func (e *env) settledLoads() int {
	e.srv.Wait()
	e.caddy.mu.Lock()
	defer e.caddy.mu.Unlock()
	return e.caddy.loads
}

type fakeDocker struct {
	mu      sync.Mutex
	actions []string
}

func (f *fakeDocker) Run(context.Context, docker.RunSpec) (int64, string, error) { return 0, "", nil }
func (f *fakeDocker) Action(_ context.Context, ref, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action+":"+ref)
	return nil
}
func (f *fakeDocker) Ping(context.Context) error { return nil }
func (f *fakeDocker) List(context.Context) ([]docker.Container, error) {
	return []docker.Container{
		{ID: "abc", Name: "web", State: "running", Ports: []int{80}, Networks: []string{}},
		{ID: "def", Name: "media-jellyfin-1", Stack: "media", Service: "jellyfin", State: "running", Health: "healthy", Ports: []int{8096}, Networks: []string{}},
		{ID: "ghi", Name: "media-helper-1", Stack: "media", Service: "helper", State: "exited", Ports: []int{}, Networks: []string{}},
	}, nil
}
func (f *fakeDocker) Inspect(_ context.Context, ref string) (*docker.Detail, error) {
	return &docker.Detail{Container: docker.Container{ID: "abc", Name: ref}}, nil
}
func (f *fakeDocker) Remove(context.Context, string) error              { return nil }
func (f *fakeDocker) Logs(context.Context, string, int) (string, error) { return "log line", nil }
func (f *fakeDocker) ConnectToApps(context.Context, string) error       { return nil }
func (f *fakeDocker) Networks(context.Context) ([]docker.Network, error) {
	return []docker.Network{}, nil
}
func (f *fakeDocker) Volumes(context.Context) ([]docker.Volume, error) { return []docker.Volume{}, nil }
func (f *fakeDocker) RemoveVolume(context.Context, string) error       { return nil }

type fakeStacks struct{}

// Check runs the real loader and policy (no Docker involved).
func (fakeStacks) Check(ctx context.Context, name string, c, e []byte) ([]stacks.Violation, error) {
	return stacks.NewManager("/srv/marineris-test/stacks", "", []string{"/srv"}, []string{"marineris"}).Check(ctx, name, c, e)
}
func (fakeStacks) Deploy(context.Context, string, []byte, []byte, bool) error { return nil }
func (fakeStacks) Recreate(context.Context, string, []byte, []byte, bool, []string, bool) error {
	return nil
}
func (fakeStacks) Down(context.Context, string, bool) error { return nil }
func (fakeStacks) Remove(string) error                      { return nil }

type env struct {
	t      *testing.T
	srv    *Server
	store  *store.Store
	caddy  *fakeCaddy
	docker *fakeDocker
	edge   *httptest.Server
	local  *httptest.Server
	setup  *setup.Service
}

func newEnv(t *testing.T) *env {
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
	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(bytes.Repeat([]byte{4}, 32))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fc, fd := &fakeCaddy{}, &fakeDocker{}
	cm := configmgr.New(cfg, st, box, fc, fd, log)
	cm.HealthTimeout = time.Second
	cm.AutheliaHealth = func(context.Context) error { return nil }
	sv := setup.New(st, box, cfg.SetupTokenPath())
	srv := New(Deps{
		Config: cfg, Store: st, Box: box, Setup: sv,
		Sessions: auth.NewSessions(st, 30*time.Minute, 12*time.Hour),
		Passkeys: auth.NewPasskeys(st, "Marineris"),
		Throttle: auth.NewThrottle(),
		Configs:  cm, Docker: fd, Stacks: fakeStacks{},
		Log: log, Version: "test",
		UI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>ui</html>")) }),
	})
	e := &env{t: t, srv: srv, store: st, caddy: fc, docker: fd, setup: sv}
	// Runs before the TempDir cleanup (cleanups run last-in, first-out).
	t.Cleanup(srv.Wait)
	e.edge = httptest.NewServer(srv.Handler(Edge))
	e.local = httptest.NewServer(srv.Handler(Local))
	t.Cleanup(e.edge.Close)
	t.Cleanup(e.local.Close)
	return e
}

// client is a minimal browser: it keeps the session cookie (even though the
// test servers are plain HTTP), the CSRF token and sends a fixed Origin.
type client struct {
	t      *testing.T
	base   string
	origin string
	cookie string
	csrf   string
	header map[string]string
}

func (e *env) localClient() *client {
	return &client{t: e.t, base: e.local.URL, origin: e.local.URL, header: map[string]string{}}
}

func (e *env) edgeClient() *client {
	return &client{t: e.t, base: e.edge.URL, origin: "https://" + uiHost, header: map[string]string{}}
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

func (c *client) do(method, path string, body any) resp {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		case []byte:
			rd = bytes.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", auth.SessionCookie+"="+c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, ck := range res.Cookies() {
		if ck.Name == auth.SessionCookie {
			if ck.MaxAge < 0 {
				c.cookie = ""
			} else {
				c.cookie = ck.Value
			}
		}
	}
	out := resp{Status: res.StatusCode, Raw: raw, Header: res.Header}
	_ = json.Unmarshal(raw, &out.Body)
	if tok, ok := out.Body["csrf_token"].(string); ok {
		c.csrf = tok
	}
	return out
}

func (r resp) code() string {
	s, _ := r.Body["code"].(string)
	return s
}

// completeSetup runs first-run setup through the local listener and returns
// a logged-in local client and the TOTP secret.
func (e *env) completeSetup() (*client, string) {
	e.t.Helper()
	tok, err := e.setup.EnsureToken(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	c := e.localClient()
	r := c.do("POST", "/api/setup/begin", map[string]string{"setup_token": tok, "username": testUser})
	if r.Status != 200 {
		e.t.Fatalf("setup begin: %d %s", r.Status, r.Raw)
	}
	secret := r.Body["totp_secret"].(string)
	code, _ := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	r = c.do("POST", "/api/setup/finish", map[string]string{
		"setup_token": tok, "username": testUser, "password": testPassword,
		"totp_code": code, "acme_email": "ops@example.com", "ui_host": uiHost,
	})
	if r.Status != 200 {
		e.t.Fatalf("setup finish: %d %s", r.Status, r.Raw)
	}
	return c, secret
}

// login performs password + TOTP login. It waits for the next TOTP step when
// needed so codes are never reused (replay protection).
func (e *env) login(c *client, secret string) resp {
	e.t.Helper()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword})
	if r.Status != 200 {
		return r
	}
	return c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": e.freshCode(secret)})
}

func (e *env) freshCode(secret string) string {
	e.t.Helper()
	ctx := context.Background()
	u, err := e.store.GetUserByName(ctx, testUser)
	if err != nil {
		e.t.Fatal(err)
	}
	step := auth.TOTPStep(time.Now())
	if step <= u.TOTPLastStep {
		// Pretend the last code was used long ago, so tests do not have
		// to wait 30 s for the next window.
		db, err := sql.Open("sqlite", "file:"+e.srv.Config.DBPath()+"?_pragma=busy_timeout(5000)")
		if err != nil {
			e.t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx, "UPDATE users SET totp_last_step = ? WHERE id = ?", step-2, u.ID); err != nil {
			e.t.Fatal(err)
		}
	}
	code, _ := auth.TOTPCode(secret, step)
	return code
}

func totpNow(secret string) (string, error) {
	return auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
}
