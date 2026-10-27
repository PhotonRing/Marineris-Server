package api

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/store"
)

var update = flag.Bool("update-golden", false, "rewrite golden files")

// TestRouteTableGolden pins the list of endpoints and their access levels.
// Adding, removing or relaxing an endpoint fails this test until the golden
// file is updated (go test ./internal/api -run Golden -update-golden), which makes
// every change to the attack surface visible in review.
func TestRouteTableGolden(t *testing.T) {
	e := newEnv(t)
	var lines []string
	for _, r := range e.srv.Routes() {
		where := "both"
		if r.LocalOnly {
			where = "local"
		}
		lines = append(lines, fmt.Sprintf("%-6s %-42s %-7s %s", r.Method, r.Pattern, r.Access, where))
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"
	golden := filepath.Join("testdata", "routes.golden")
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("missing golden file (run with -update-golden): %v", err)
	}
	if got != string(want) {
		t.Fatalf("route table changed; review and run with -update-golden.\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// Every mutating route must require at least a session, except the few
// login/setup endpoints listed here.
func TestOnlyExpectedRoutesArePublic(t *testing.T) {
	allowed := map[string]bool{
		"GET /api/health": true, "GET /api/meta": true,
		"POST /api/auth/login": true, "POST /api/auth/login/mfa": true,
		"POST /api/auth/passkey/begin": true, "POST /api/auth/passkey/finish": true,
	}
	e := newEnv(t)
	for _, r := range e.srv.Routes() {
		key := r.Method + " " + r.Pattern
		if r.Access == Public && !allowed[key] {
			t.Errorf("%s is public but not in the allow-list", key)
		}
		if r.Access == SetupOnly && !r.LocalOnly {
			t.Errorf("%s: setup routes must be local-only", key)
		}
	}
}

func concrete(pattern string) string {
	re := regexp.MustCompile(`\{[a-z]+\}`)
	return re.ReplaceAllStringFunc(pattern, func(p string) string {
		switch p {
		case "{id}":
			return "1"
		case "{component}":
			return "caddy"
		default:
			return "x"
		}
	})
}

// TestAccessMatrix calls every protected route as: anonymous, with a stolen
// cookie but no CSRF token, from a foreign origin, and with a session whose
// authentication is too old for step-up routes.
func TestAccessMatrix(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	for _, rt := range e.srv.Routes() {
		if rt.Access == Public || rt.Access == SetupOnly {
			continue
		}
		path := concrete(rt.Pattern)
		name := rt.Method + " " + rt.Pattern
		for _, c := range []*client{e.localClient(), e.edgeClient()} {
			r := c.do(rt.Method, path, "{}")
			if isUnsafe(rt.Method) {
				if r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
					t.Errorf("%s anonymous: got %d", name, r.Status)
				}
			} else if r.Status != http.StatusUnauthorized {
				t.Errorf("%s anonymous: got %d, want 401", name, r.Status)
			}
		}
		if isUnsafe(rt.Method) {
			// Cookie without CSRF token.
			stolen := &client{t: t, base: e.local.URL, origin: e.local.URL, cookie: admin.cookie, header: map[string]string{}}
			if r := stolen.do(rt.Method, path, "{}"); r.Status != http.StatusForbidden || r.code() != "csrf" {
				t.Errorf("%s without CSRF: got %d %s", name, r.Status, r.code())
			}
			// Valid cookie and token, foreign origin.
			foreign := &client{t: t, base: e.local.URL, origin: "https://evil.example", cookie: admin.cookie, csrf: admin.csrf, header: map[string]string{}}
			if r := foreign.do(rt.Method, path, "{}"); r.Status != http.StatusForbidden || r.code() != "bad_origin" {
				t.Errorf("%s foreign origin: got %d %s", name, r.Status, r.code())
			}
			// No Origin header at all.
			noOrigin := &client{t: t, base: e.local.URL, cookie: admin.cookie, csrf: admin.csrf, header: map[string]string{}}
			if r := noOrigin.do(rt.Method, path, "{}"); r.Status != http.StatusForbidden {
				t.Errorf("%s without Origin: got %d", name, r.Status)
			}
			// Cross-site fetch metadata.
			xsite := &client{t: t, base: e.local.URL, origin: e.local.URL, cookie: admin.cookie, csrf: admin.csrf, header: map[string]string{"Sec-Fetch-Site": "cross-site"}}
			if r := xsite.do(rt.Method, path, "{}"); r.Status != http.StatusForbidden {
				t.Errorf("%s cross-site: got %d", name, r.Status)
			}
		}
	}

	// Step-up routes refuse a session whose authentication is old.
	stale := time.Now().Add(-StepUpWindow - time.Minute).UTC()
	se, _ := e.store.GetSession(context.Background(), auth.HashToken(admin.cookie))
	if err := e.store.MarkSessionAuth(context.Background(), se.IDHash, stale); err != nil {
		t.Fatal(err)
	}
	for _, rt := range e.srv.Routes() {
		if rt.Access != StepUp {
			continue
		}
		r := admin.do(rt.Method, concrete(rt.Pattern), "{}")
		if r.Status != http.StatusForbidden || r.code() != "reauth_required" {
			t.Errorf("%s with stale auth: got %d %s", rt.Method+" "+rt.Pattern, r.Status, r.code())
		}
	}
}

func TestLocalOnlyRoutesAbsentOnEdge(t *testing.T) {
	e := newEnv(t)
	for _, rt := range e.srv.Routes() {
		if !rt.LocalOnly {
			continue
		}
		r := e.edgeClient().do(rt.Method, concrete(rt.Pattern), "{}")
		if r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
			t.Errorf("%s reachable on edge: %d", rt.Pattern, r.Status)
		}
	}
}

func TestEdgeServesNothingBeforeSetup(t *testing.T) {
	e := newEnv(t)
	c := e.edgeClient()
	if r := c.do("GET", "/api/health", nil); r.Status != 200 {
		t.Fatal("health must stay public")
	}
	if r := c.do("GET", "/api/meta", nil); r.Status != 200 || r.Body["setup_complete"] != false {
		t.Fatal("meta must report setup pending")
	}
	for _, p := range []string{"/api/overview", "/api/routes", "/api/containers"} {
		if r := c.do("GET", p, nil); r.Status != http.StatusServiceUnavailable || r.code() != "setup_pending" {
			t.Errorf("%s before setup: %d", p, r.Status)
		}
	}
	if r := c.do("POST", "/api/auth/login", map[string]string{"username": "a", "password": "b"}); r.Status != http.StatusServiceUnavailable {
		t.Errorf("login before setup: %d", r.Status)
	}
}

func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.setup.EnsureToken(context.Background())
	c := e.localClient()
	if r := c.do("POST", "/api/setup/begin", map[string]string{"setup_token": "wrong"}); r.Status != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", r.Status)
	}
	admin, _ := e.completeSetup()
	if r := admin.do("GET", "/api/auth/session", nil); r.Status != 200 {
		t.Fatal("setup must log the admin in")
	}
	// Setup cannot be replayed, even with the old token.
	if r := c.do("POST", "/api/setup/begin", map[string]string{"setup_token": tok}); r.Status != http.StatusConflict {
		t.Fatalf("setup replay: %d", r.Status)
	}
	if _, err := os.Stat(e.srv.Config.SetupTokenPath()); !os.IsNotExist(err) {
		t.Fatal("setup token survived setup")
	}
}

func TestLoginFlowAndReplay(t *testing.T) {
	e := newEnv(t)
	_, secret := e.completeSetup()
	c := e.edgeClient()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword})
	if r.Status != 200 || r.Body["mfa_required"] != true || c.cookie != "" {
		t.Fatalf("password step must not create a session: %d %v", r.Status, r.Body)
	}
	tok := r.Body["mfa_token"].(string)
	code := e.freshCode(secret)
	r = c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": tok, "code": code})
	if r.Status != 200 || c.cookie == "" {
		t.Fatalf("mfa: %d %s", r.Status, r.Raw)
	}
	// MFA token is single-use.
	if r := e.edgeClient().do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": tok, "code": code}); r.Status != http.StatusUnauthorized {
		t.Fatalf("mfa token reuse: %d", r.Status)
	}
	// The same TOTP code cannot be used for a second login (replay).
	c2 := e.edgeClient()
	r = c2.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword})
	r = c2.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": code})
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("TOTP replay accepted: %d", r.Status)
	}
	// Logout invalidates the session server-side.
	old := c.cookie
	if r := c.do("POST", "/api/auth/logout", nil); r.Status != 200 {
		t.Fatal("logout")
	}
	replay := &client{t: t, base: e.edge.URL, origin: "https://" + uiHost, cookie: old, header: map[string]string{}}
	if r := replay.do("GET", "/api/auth/session", nil); r.Status != http.StatusUnauthorized {
		t.Fatal("session usable after logout")
	}
}

func TestMFAAttemptLimit(t *testing.T) {
	e := newEnv(t)
	_, secret := e.completeSetup()
	c := e.localClient()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword})
	tok := r.Body["mfa_token"].(string)
	for i := 0; i < mfaMaxAttempts; i++ {
		c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": tok, "code": "000000"})
	}
	r = c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": tok, "code": e.freshCode(secret)})
	if r.Status == 200 {
		t.Fatal("MFA token still valid after too many wrong codes")
	}
}

func TestRecoveryCodeLoginOnce(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.setup.EnsureToken(context.Background())
	c := e.localClient()
	r := c.do("POST", "/api/setup/begin", map[string]string{"setup_token": tok, "username": testUser})
	secret := r.Body["totp_secret"].(string)
	code, _ := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	r = c.do("POST", "/api/setup/finish", map[string]string{"setup_token": tok, "username": testUser, "password": testPassword, "totp_code": code, "acme_email": "ops@example.com", "ui_host": uiHost})
	codes := r.Body["recovery_codes"].([]any)
	rc := codes[0].(string)
	for i, want := range []int{200, 401} {
		cl := e.localClient()
		r := cl.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword})
		r = cl.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "recovery_code": rc})
		if r.Status != want {
			t.Fatalf("attempt %d: got %d want %d", i, r.Status, want)
		}
	}
}

func TestLoginThrottlingAndEnumeration(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	c := e.edgeClient()
	unknown := c.do("POST", "/api/auth/login", map[string]string{"username": "nobody", "password": "x"})
	wrong := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": "wrong-password"})
	if unknown.Status != wrong.Status || unknown.Body["error"] != wrong.Body["error"] {
		t.Fatal("responses reveal whether the account exists")
	}
	var last resp
	for i := 0; i < 6; i++ {
		last = c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": "wrong-password"})
	}
	if last.Status != http.StatusTooManyRequests {
		t.Fatalf("expected throttling, got %d", last.Status)
	}
	// Even the right password is refused while locked.
	if r := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword}); r.Status != http.StatusTooManyRequests {
		t.Fatalf("locked account accepted correct password: %d", r.Status)
	}
	// Break-glass: the local listener is not affected by the account lock.
	if r := e.localClient().do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": testPassword}); r.Status != 200 {
		t.Fatalf("break-glass login blocked by edge lockout: %d", r.Status)
	}
}

func TestSessionFixation(t *testing.T) {
	e := newEnv(t)
	_, secret := e.completeSetup()
	c := e.localClient()
	c.cookie = "attacker-chosen-session-value-0123456789abcdef"
	if r := e.login(c, secret); r.Status != 200 {
		t.Fatalf("login: %d", r.Status)
	}
	if c.cookie == "attacker-chosen-session-value-0123456789abcdef" {
		t.Fatal("session id not regenerated")
	}
}

func TestXFFIgnoredOnLocalListener(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	c := e.localClient()
	c.header["X-Forwarded-For"] = "203.0.113.9"
	for i := 0; i < 7; i++ {
		c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": "wrong-password"})
	}
	// Rotating the spoofed header must not reset throttling.
	c.header["X-Forwarded-For"] = "198.51.100.1"
	if r := c.do("POST", "/api/auth/login", map[string]string{"username": testUser, "password": "wrong-password"}); r.Status != http.StatusTooManyRequests {
		t.Fatalf("X-Forwarded-For spoofing bypassed throttling: %d", r.Status)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	for _, base := range []string{e.edge.URL, e.local.URL} {
		for _, p := range []string{"/", "/api/health", "/api/nope"} {
			res, err := http.Get(base + p)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
				if res.Header.Get(h) == "" {
					t.Errorf("%s%s missing %s", base, p, h)
				}
			}
			if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Error("CSP must forbid framing")
			}
		}
	}
	res, _ := http.Get(e.edge.URL + "/api/health")
	if res.Header.Get("Strict-Transport-Security") == "" {
		t.Error("edge must send HSTS")
	}
	res, _ = http.Get(e.local.URL + "/api/meta")
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Error("API responses must not be cached")
	}
}

func TestStrictJSONAndBodyLimit(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	if r := admin.do("POST", "/api/domains", `{"name":"example.com","evil":true}`); r.Status != 400 {
		t.Fatalf("unknown field accepted: %d", r.Status)
	}
	if r := admin.do("POST", "/api/domains", `{"name":"example.com"}{"name":"x.com"}`); r.Status != 400 {
		t.Fatalf("trailing JSON accepted: %d", r.Status)
	}
	big := `{"name":"` + strings.Repeat("a", MaxBody) + `"}`
	if r := admin.do("POST", "/api/domains", big); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", r.Status)
	}
}

func TestInvalidRouteNeverReachesDBOrCaddy(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	if r := admin.do("POST", "/api/domains", map[string]any{"name": "example.com"}); r.Status != 200 {
		t.Fatalf("domain: %d %s", r.Status, r.Raw)
	}
	loads := e.settledLoads()
	for name, body := range map[string]map[string]any{
		"outside domain": {"name": "x", "host": "x.evil.org", "kind": "container", "target": map[string]any{"container": "web", "port": 80}, "auth": "none", "enabled": true},
		"ui host":        {"name": "x", "host": uiHost, "kind": "container", "target": map[string]any{"container": "web", "port": 80}, "auth": "none", "enabled": true},
		"loopback":       {"name": "x", "host": "a.example.com", "kind": "external", "target": map[string]any{"url": "http://127.0.0.1:2019"}, "auth": "none", "enabled": true},
		"traversal":      {"name": "x", "host": "a.example.com", "kind": "static", "target": map[string]any{"folder": "../../etc"}, "auth": "none", "enabled": true},
		"injection":      {"name": "x", "host": `a.example.com"}]`, "kind": "container", "target": map[string]any{"container": "web", "port": 80}, "auth": "none", "enabled": true},
	} {
		if r := admin.do("POST", "/api/routes", body); r.Status != 400 {
			t.Errorf("%s: got %d %s", name, r.Status, r.Raw)
		}
	}
	rs, _ := e.store.ListRoutes(context.Background())
	if len(rs) != 0 || e.settledLoads() != loads {
		t.Fatal("invalid route persisted or sent to Caddy")
	}
}

func TestFailedApplyRevertsDatabase(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	if r := admin.do("POST", "/api/domains", map[string]any{"name": "example.com"}); r.Status != 200 {
		t.Fatalf("domain: %d %s", r.Status, r.Raw)
	}
	e.caddy.fail = fmt.Errorf("simulated Caddy failure")
	r := admin.do("POST", "/api/routes", map[string]any{"name": "web", "host": "web.example.com", "kind": "container",
		"target": map[string]any{"container": "web", "port": 80}, "auth": "none", "enabled": true})
	if r.Status != http.StatusBadGateway || r.code() != "apply_failed" {
		t.Fatalf("expected apply failure, got %d %s", r.Status, r.Raw)
	}
	rs, _ := e.store.ListRoutes(context.Background())
	if len(rs) != 0 {
		t.Fatal("route kept although Caddy rejected the configuration")
	}
	vs, _ := e.store.ListConfigVersions(context.Background(), "caddy", 5)
	if len(vs) == 0 || vs[0].Status != store.ConfigFailed {
		t.Fatal("failed attempt not recorded in history")
	}
}

func TestSecretsNeverReturned(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	r := admin.do("POST", "/api/dns/profiles", map[string]any{"name": "OVH main", "provider": "ovh",
		"config": map[string]string{"application_key": "ak-visible", "application_secret": "TOP-SECRET-1", "consumer_key": "TOP-SECRET-2"}})
	if r.Status != 200 {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	for _, p := range []string{"/api/dns/profiles", "/api/config/components/caddy/current", "/api/audit", "/api/settings"} {
		res := admin.do("GET", p, nil)
		if strings.Contains(string(res.Raw), "TOP-SECRET") {
			t.Errorf("%s leaks a DNS secret", p)
		}
	}
	res := admin.do("GET", "/api/auth/session", nil)
	for _, leak := range []string{"argon2", "password_hash", "totp_secret", "webauthn"} {
		if strings.Contains(strings.ToLower(string(res.Raw)), leak) {
			t.Errorf("session endpoint leaks %s", leak)
		}
	}
}

func TestStepUpFlow(t *testing.T) {
	e := newEnv(t)
	admin, secret := e.completeSetup()
	stale := time.Now().Add(-time.Hour).UTC()
	se, _ := e.store.GetSession(context.Background(), auth.HashToken(admin.cookie))
	_ = e.store.MarkSessionAuth(context.Background(), se.IDHash, stale)
	if r := admin.do("POST", "/api/account/recovery-codes", nil); r.code() != "reauth_required" {
		t.Fatalf("expected reauth_required, got %d", r.Status)
	}
	if r := admin.do("POST", "/api/auth/reauth", map[string]string{"password": "wrong", "code": e.freshCode(secret)}); r.Status != 401 {
		t.Fatalf("reauth with wrong password: %d", r.Status)
	}
	if r := admin.do("POST", "/api/auth/reauth", map[string]string{"password": testPassword, "code": e.freshCode(secret)}); r.Status != 200 {
		t.Fatalf("reauth: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("POST", "/api/account/recovery-codes", nil); r.Status != 200 {
		t.Fatalf("after reauth: %d", r.Status)
	}
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	e := newEnv(t)
	admin, secret := e.completeSetup()
	other := e.localClient()
	if r := e.login(other, secret); r.Status != 200 {
		t.Fatal("second login")
	}
	r := admin.do("POST", "/api/account/password", map[string]string{"current_password": testPassword, "new_password": "Olympus-Mons-Rises-21"})
	if r.Status != 200 {
		t.Fatalf("password change: %d %s", r.Status, r.Raw)
	}
	if r := other.do("GET", "/api/auth/session", nil); r.Status != 401 {
		t.Fatal("other session survived password change")
	}
	if r := admin.do("GET", "/api/auth/session", nil); r.Status != 200 {
		t.Fatal("current session should survive")
	}
}

func TestPasskeyRequiresLocalhostName(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	c := e.localClient() // origin http://127.0.0.1:port
	if r := c.do("POST", "/api/auth/passkey/begin", nil); r.Status != 400 || r.code() != "passkey_localhost" {
		t.Fatalf("got %d %s", r.Status, r.code())
	}
	edge := e.edgeClient()
	r := edge.do("POST", "/api/auth/passkey/begin", nil)
	if r.Status != 200 {
		t.Fatalf("edge passkey begin: %d %s", r.Status, r.Raw)
	}
	opts := r.Body["options"].(map[string]any)["publicKey"].(map[string]any)
	if opts["rpId"] != uiHost || opts["userVerification"] != "required" {
		t.Fatalf("unexpected options %v", opts)
	}
}

func TestSystemContainersProtected(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	for _, p := range []string{"/api/containers/web/stop", "/api/containers/web/restart"} {
		if r := admin.do("POST", p, nil); r.Status != 200 {
			t.Errorf("%s: %d", p, r.Status)
		}
	}
	if r := admin.do("POST", "/api/containers/a;rm -rf/stop", nil); r.Status != 400 && r.Status != 404 {
		t.Errorf("malformed container ref accepted: %d", r.Status)
	}
}
