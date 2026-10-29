package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/libdns/libdns"

	"github.com/photonring/marineris-server/internal/dnsprofiles"
)

type okDNS struct{ recs []libdns.Record }

func (o *okDNS) AppendRecords(_ context.Context, _ string, r []libdns.Record) ([]libdns.Record, error) {
	o.recs = append(o.recs, r...)
	return r, nil
}
func (o *okDNS) DeleteRecords(_ context.Context, _ string, r []libdns.Record) ([]libdns.Record, error) {
	o.recs = nil
	return r, nil
}
func (o *okDNS) GetRecords(context.Context, string) ([]libdns.Record, error) { return o.recs, nil }

// TestWalkthrough drives every endpoint through its main path, the way the
// UI does, and checks the resulting state.
func TestWalkthrough(t *testing.T) {
	e := newEnv(t)
	admin, secret := e.completeSetup()
	must := func(method, path string, body any) resp {
		t.Helper()
		r := admin.do(method, path, body)
		if r.Status != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Raw)
		}
		return r
	}

	// Meta, overview, session.
	if r := must("GET", "/api/meta", nil); r.Body["setup_complete"] != true {
		t.Fatal("meta")
	}
	if r := must("GET", "/api/overview", nil); len(r.Body["alerts"].([]any)) == 0 {
		t.Fatal("a fresh install must show alerts (staging, no domain)")
	}
	must("GET", "/api/auth/session", nil)

	// DNS profiles: providers, create, list (redacted), update keeps secrets, test.
	if r := admin.do("GET", "/api/dns/providers", nil); !strings.Contains(string(r.Raw), "infomaniak") {
		t.Fatal("providers")
	}
	must("POST", "/api/dns/profiles", map[string]any{"name": "CF", "provider": "cloudflare", "config": map[string]string{"api_token": "cf-token-secret"}})
	if r := admin.do("POST", "/api/dns/profiles", map[string]any{"name": "CF", "provider": "cloudflare", "config": map[string]string{"api_token": "x-token-xx"}}); r.Status != http.StatusConflict {
		t.Fatalf("duplicate profile name: %d", r.Status)
	}
	if r := admin.do("POST", "/api/dns/profiles", map[string]any{"name": "Bad", "provider": "nope", "config": map[string]string{}}); r.Status != 400 {
		t.Fatalf("unknown provider: %d", r.Status)
	}
	list := admin.do("GET", "/api/dns/profiles", nil)
	if strings.Contains(string(list.Raw), "cf-token-secret") || !strings.Contains(string(list.Raw), `"set":true`) {
		t.Fatalf("profile list: %s", list.Raw)
	}
	must("PUT", "/api/dns/profiles/1", map[string]any{"name": "Cloudflare", "config": map[string]string{"api_token": ""}})
	p, _ := e.store.GetDNSProfile(context.Background(), 1)
	cfg, _ := dnsprofiles.Open(e.srv.Box, 1, p.Config)
	if p.Name != "Cloudflare" || cfg["api_token"] != "cf-token-secret" {
		t.Fatal("update must keep the secret when left blank")
	}
	fake := &okDNS{}
	old := dnsprofiles.NewClient
	dnsprofiles.NewClient = func(string, dnsprofiles.Config) (dnsprofiles.Client, error) { return fake, nil }
	t.Cleanup(func() { dnsprofiles.NewClient = old })
	if r := must("POST", "/api/dns/profiles/1/test", map[string]string{"zone": "example.com"}); r.Body["ok"] != true {
		t.Fatalf("dns test: %s", r.Raw)
	}
	if r := admin.do("POST", "/api/dns/profiles/1/test", map[string]string{"zone": "not a zone"}); r.Status != 400 {
		t.Fatal("invalid zone accepted")
	}

	// Domains: one with the DNS profile (wildcard), one with HTTP-01.
	pid := 1
	must("POST", "/api/domains", map[string]any{"name": "example.com", "dns_profile_id": pid, "wildcard": true})
	must("POST", "/api/domains", map[string]any{"name": "friend.org"})
	if r := admin.do("POST", "/api/domains", map[string]any{"name": "sub.example.com"}); r.Status != 400 {
		t.Fatalf("nested domain accepted: %d", r.Status)
	}
	if r := admin.do("POST", "/api/domains", map[string]any{"name": "x.com", "dns_profile_id": 99}); r.Status != 400 {
		t.Fatalf("dangling profile accepted: %d", r.Status)
	}
	must("PUT", "/api/domains/2", map[string]any{"name": "friend.org", "dns_profile_id": pid, "wildcard": true})
	if r := admin.do("DELETE", "/api/dns/profiles/1", nil); r.Status != http.StatusConflict {
		t.Fatalf("profile in use deleted: %d", r.Status)
	}
	if !strings.Contains(string(e.caddy.running), "*.friend.org") {
		t.Fatal("wildcard for friend.org not applied")
	}

	// Settings: UI host inside a managed domain; then a bad one.
	st := must("GET", "/api/settings", nil).Body
	delete(st, "setup_complete")
	st["ui_host"] = "marineris.example.com"
	must("PUT", "/api/settings", st)
	st["ui_host"] = "marineris.elsewhere.net"
	if r := admin.do("PUT", "/api/settings", st); r.Status != 400 || r.code() != "invalid" {
		t.Fatalf("UI host outside domains: %d %s", r.Status, r.Raw)
	}
	st["ui_host"] = "marineris.example.com"
	st["acme_email"] = "not an email"
	if r := admin.do("PUT", "/api/settings", st); r.Status != 400 {
		t.Fatal("bad e-mail accepted")
	}

	// Routes.
	route := map[string]any{"name": "web", "host": "web.example.com", "kind": "container",
		"target": map[string]any{"container": "web", "port": 80}, "auth": "authelia", "enabled": true, "connect_network": true}
	must("POST", "/api/routes", route)
	route["auth"] = "none"
	route["rate_limit"] = map[string]any{"events": 50, "window": "1m"}
	must("PUT", "/api/routes/1", route)
	if !strings.Contains(string(e.caddy.running), `"route_1"`) {
		t.Fatal("per-route rate limit not applied")
	}
	must("POST", "/api/routes", map[string]any{"name": "site", "host": "friend.org", "kind": "spa",
		"target": map[string]any{"folder": "site"}, "auth": "none", "enabled": true})
	if r := admin.do("DELETE", "/api/domains/1", nil); r.Status != 400 {
		t.Fatalf("domain with routes deleted: %d %s", r.Status, r.Raw)
	}
	// Hub: tiles, custom title/subtitle, hidden routes, icons, widgets.
	route["hub"] = map[string]any{"visible": true, "title": "Jellyfin", "subtitle": "Films & séries"}
	must("PUT", "/api/routes/1", route)
	hub := must("GET", "/api/hub", nil)
	tiles := hub.Body["tiles"].([]any)
	if len(tiles) != 2 {
		t.Fatalf("expected 2 tiles, got %s", hub.Raw)
	}
	tile := tiles[1].(map[string]any)
	if tiles[0].(map[string]any)["title"] == "Jellyfin" {
		tile = tiles[0].(map[string]any)
	}
	if tile["title"] != "Jellyfin" || tile["subtitle"] != "Films & séries" || tile["url"] != "https://web.example.com/" {
		t.Fatalf("tile %v", tile)
	}
	if r := admin.do("PUT", "/api/routes/1/icon", map[string]string{"data": "PGh0bWw+PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg=="}); r.Status != 400 {
		t.Fatalf("HTML accepted as icon: %d", r.Status)
	}
	svg := "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4=" // <svg xmlns=…/>
	must("PUT", "/api/routes/1/icon", map[string]string{"data": svg})
	if r := admin.do("PUT", "/api/routes/1/icon", map[string]string{"url": "http://example.com/x.png"}); r.Status == 200 {
		t.Fatal("plain-http icon URL accepted")
	}
	if r := admin.do("PUT", "/api/routes/1/icon", map[string]string{"name": "../etc"}); r.Status != 400 {
		t.Fatal("bad icon name accepted")
	}
	hub = must("GET", "/api/hub", nil)
	if !strings.Contains(string(hub.Raw), "/api/hub/icons/1?v=") {
		t.Fatalf("icon URL missing: %s", hub.Raw)
	}
	icon := admin.do("GET", "/api/hub/icons/1", nil)
	if icon.Status != 200 || icon.Header.Get("Content-Type") != "image/svg+xml" || !strings.Contains(icon.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("icon served unsafely: %d %v", icon.Status, icon.Header)
	}
	must("DELETE", "/api/routes/1/icon", nil)
	if r := admin.do("GET", "/api/hub/icons/1", nil); r.Status != 404 {
		t.Fatal("icon not deleted")
	}
	// Updating a route without hub fields keeps them.
	delete(route, "hub")
	must("PUT", "/api/routes/1", route)
	if got, _ := e.store.GetRoute(context.Background(), 1); got.Hub.Title != "Jellyfin" {
		t.Fatal("hub fields lost on update")
	}
	route["hub"] = map[string]any{"visible": false}
	must("PUT", "/api/routes/1", route)
	if r := must("GET", "/api/hub", nil); len(r.Body["tiles"].([]any)) != 1 {
		t.Fatal("hidden route shown on the hub")
	}
	st = must("GET", "/api/settings", nil).Body
	delete(st, "setup_complete")
	st["home_page"] = "dashboard"
	st["hub_widgets"] = []string{"clock", "cpu"}
	must("PUT", "/api/settings", st)
	st["hub_widgets"] = []string{"clock", "shell"}
	if r := admin.do("PUT", "/api/settings", st); r.Status != 400 {
		t.Fatal("unknown widget accepted")
	}
	st["hub_widgets"] = []string{"clock", "cpu"}
	if r := admin.do("GET", "/api/system/stats", nil); r.Status != http.StatusServiceUnavailable {
		t.Fatalf("stats without sampler: %d", r.Status)
	}

	must("DELETE", "/api/routes/2", nil)
	if rs := must("GET", "/api/routes", nil); !strings.Contains(string(rs.Raw), "web.example.com") {
		t.Fatal("routes list")
	}
	must("GET", "/api/certificates", nil)

	// Containers, volumes, networks.
	must("GET", "/api/containers", nil)
	must("GET", "/api/containers/web", nil)
	if r := must("GET", "/api/containers/web/logs?tail=10", nil); r.Body["logs"] != "log line" {
		t.Fatal("logs")
	}
	must("POST", "/api/containers/web/start", nil)
	must("DELETE", "/api/containers/web", nil)
	must("GET", "/api/volumes", nil)
	must("DELETE", "/api/volumes/data", nil)
	must("GET", "/api/networks", nil)

	// Stacks.
	compose := "services:\n  app:\n    image: nginx:1.27-alpine\n"
	must("POST", "/api/stacks", map[string]any{"name": "demo", "compose": compose, "env": "TZ=UTC"})
	if r := admin.do("POST", "/api/stacks", map[string]any{"name": "../x", "compose": compose}); r.Status != 400 {
		t.Fatal("bad stack name accepted")
	}
	if r := must("GET", "/api/stacks/demo", nil); r.Body["env"] != "TZ=UTC" {
		t.Fatal("stack env not returned")
	}
	raw, _ := e.store.GetStack(context.Background(), "demo")
	if strings.Contains(raw.Env, "TZ=UTC") {
		t.Fatal("stack .env stored in clear text")
	}
	must("PUT", "/api/stacks/demo", map[string]any{"compose": compose, "env": "TZ=Europe/Paris"})
	if r := must("POST", "/api/stacks/check", map[string]any{"compose": "include: [/etc/x]\nservices: {a: {image: x}}"}); len(r.Body["blocking"].([]any)) == 0 {
		t.Fatal("check did not block include")
	}
	must("POST", "/api/stacks/demo/deploy", nil)
	must("POST", "/api/stacks/demo/down", nil)
	if r := must("GET", "/api/stacks", nil); !strings.Contains(string(r.Raw), "demo") {
		t.Fatal("stack list")
	}
	must("DELETE", "/api/stacks/demo", nil)

	// Configuration history: list, view (redacted), rollback, apply.
	h := admin.do("GET", "/api/config/components/caddy/history", nil)
	if h.Status != 200 || !strings.Contains(string(h.Raw), "superseded") {
		t.Fatalf("history: %s", h.Raw)
	}
	if r := admin.do("GET", "/api/config/components/nope/history", nil); r.Status != 404 {
		t.Fatal("unknown component")
	}
	v := must("GET", "/api/config/versions/1", nil)
	if strings.Contains(string(v.Raw), "cf-token-secret") {
		t.Fatal("history view leaks the DNS secret")
	}
	cur := must("GET", "/api/config/components/caddy/current", nil)
	if strings.Contains(string(cur.Raw), "cf-token-secret") || !strings.Contains(string(cur.Raw), "redacted") {
		t.Fatal("current config not redacted")
	}
	must("GET", "/api/config/components/authelia/current", nil)
	must("POST", "/api/config/versions/1/rollback", nil)
	must("POST", "/api/config/apply", nil)

	// Account.
	must("GET", "/api/account/passkeys", nil)
	if r := admin.do("DELETE", "/api/account/passkeys/!!", nil); r.Status != 400 {
		t.Fatal("bad passkey id")
	}
	codes := must("POST", "/api/account/recovery-codes", nil)
	if len(codes.Body["recovery_codes"].([]any)) != 10 {
		t.Fatal("recovery codes")
	}
	tb := must("POST", "/api/account/totp/begin", nil)
	if r := admin.do("POST", "/api/account/totp/finish", map[string]string{"code": "000000"}); r.Status != 400 {
		t.Fatal("wrong enrolment code accepted")
	}
	newSecret := tb.Body["totp_secret"].(string)
	if !strings.HasPrefix(tb.Body["qr"].(string), "data:image/png;base64,") {
		t.Fatal("QR code missing")
	}
	code := e.freshCodeFor(newSecret)
	must("POST", "/api/account/totp/finish", map[string]string{"code": code})
	_ = secret
	must("POST", "/api/account/sessions/revoke-others", nil)

	// Audit trail covers what happened.
	audit := must("GET", "/api/audit", nil)
	for _, a := range []string{"dns_profile.create", "dns_profile.test", "domain.create", "route.update", "stack.deploy", "config.rollback", "totp.enrol", "settings.update"} {
		if !strings.Contains(string(audit.Raw), a) {
			t.Errorf("audit misses %s", a)
		}
	}

	// Finally, the profile can be deleted once no domain uses it.
	must("DELETE", "/api/routes/1", nil)
	st["ui_host"] = ""
	st["acme_email"] = "ops@example.com"
	must("PUT", "/api/settings", st)
	must("DELETE", "/api/domains/1", nil)
	must("PUT", "/api/domains/2", map[string]any{"name": "friend.org"})
	must("DELETE", "/api/dns/profiles/1", nil)
}

// freshCodeFor returns the current code of a secret that has not been used
// yet (the new authenticator's step counter was reset by SetTOTP).
func (e *env) freshCodeFor(secret string) string {
	code, err := totpNow(secret)
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}
