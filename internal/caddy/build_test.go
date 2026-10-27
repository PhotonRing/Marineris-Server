package caddy

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/photonring/marineris-server/internal/dnsprofiles"
	"github.com/photonring/marineris-server/internal/store"
)

func baseInput() Input {
	st := store.DefaultSettings()
	st.SetupComplete = true
	st.ACMEEmail = "ops@example.com"
	st.UIHost = "marineris.example.com"
	return Input{
		Env: Env{
			AdminListen:       "unix//run/caddy-admin/admin.sock",
			ProbeListen:       ":2020",
			AccessLog:         "/var/log/caddy/access.log",
			MarinerisUpstream: "marineris:8080",
			AutheliaUpstream:  "authelia:9091",
			CrowdSecURL:       "http://crowdsec:8080",
			BouncerKey:        "bouncer-key",
			StaticRoot:        "/srv/static",
		},
		Settings: st,
		Domains: []Domain{
			{Name: "example.com", Wildcard: true, DNS: &DNS{Provider: "ovh", Config: dnsprofiles.Config{
				"endpoint": "ovh-eu", "application_key": "ak", "application_secret": "as", "consumer_key": "ck"}}},
			{Name: "friend.org", Wildcard: true, DNS: &DNS{Provider: "ovh", Config: dnsprofiles.Config{
				"endpoint": "ovh-eu", "application_key": "ak2", "application_secret": "as2", "consumer_key": "ck2"}}},
			{Name: "plain.net"},
		},
		Routes: []*store.Route{
			{ID: 1, Name: "jellyfin", Host: "media.example.com", Kind: store.RouteContainer, Target: store.RouteTarget{Container: "jellyfin", Port: 8096}, Auth: store.AuthAuthelia, Enabled: true},
			{ID: 2, Name: "api", Host: "media.example.com", PathPrefix: "/api", Kind: store.RouteContainer, Target: store.RouteTarget{Container: "api", Port: 3000}, Auth: store.AuthNone, Enabled: true, RateLimit: &store.RateLimit{Events: 100, Window: "1m"}},
			{ID: 3, Name: "nas", Host: "nas.friend.org", Kind: store.RouteExternal, Target: store.RouteTarget{URL: "https://192.168.1.20:5001", TLSInsecureSkipVerify: true}, Auth: store.AuthAuthelia, Enabled: true},
			{ID: 4, Name: "site", Host: "www.plain.net", Kind: store.RouteSPA, Target: store.RouteTarget{Folder: "site"}, Auth: store.AuthNone, Enabled: true},
			{ID: 5, Name: "disabled", Host: "off.example.com", Kind: store.RouteStatic, Target: store.RouteTarget{Folder: "x"}, Auth: store.AuthNone, Enabled: false},
		},
	}
}

func build(t *testing.T, in Input) map[string]any {
	t.Helper()
	b, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBuildDeterministic(t *testing.T) {
	a, err := Build(baseInput())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		b, _ := Build(baseInput())
		if !bytes.Equal(a, b) {
			t.Fatal("output differs between runs")
		}
	}
}

func TestBuildStructure(t *testing.T) {
	b, _ := Build(baseInput())
	s := string(b)
	for _, want := range []string{
		`"listen": "unix//run/caddy-admin/admin.sock"`,
		`"ca": "` + LetsEncryptStaging + `"`,
		`"*.example.com"`,
		`"*.friend.org"`,
		`"skip_certificates"`,
		`"handler": "crowdsec"`,
		`"dial": "jellyfin:8096"`,
		`"dial": "192.168.1.20:5001"`,
		`"insecure_skip_verify": true`,
		`"uri": "/api/authz/forward-auth"`,
		`"auth.example.com"`, `"auth.friend.org"`, `"auth.plain.net"`,
		`"root": "/srv/static/site"`,
		`"route_2"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(s, "off.example.com") {
		t.Error("disabled route rendered")
	}
	m := build(t, baseInput())
	skip, _ := json.Marshal(m["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["edge"].(map[string]any)["automatic_https"])
	for _, h := range []string{"media.example.com", "auth.example.com", "marineris.example.com", "nas.friend.org"} {
		if !strings.Contains(string(skip), h) {
			t.Errorf("%s covered by wildcard but not skipped", h)
		}
	}
	for _, h := range []string{"www.plain.net", "auth.plain.net"} {
		if strings.Contains(string(skip), h) {
			t.Errorf("%s needs its own certificate but is skipped", h)
		}
	}
	if strings.Contains(s, "*.plain.net") {
		t.Error("wildcard requested for a domain without DNS profile")
	}
	if strings.Contains(s, "zerossl") {
		t.Error("unexpected issuer")
	}
}

func TestProductionCA(t *testing.T) {
	in := baseInput()
	in.Settings.ACMEStaging = false
	b, _ := Build(in)
	if !strings.Contains(string(b), LetsEncryptProduction) || strings.Contains(string(b), "staging") {
		t.Fatal("production CA not used")
	}
}

func TestMorePreciseRoutesFirst(t *testing.T) {
	b, _ := Build(baseInput())
	s := string(b)
	if strings.Index(s, `"dial": "api:3000"`) > strings.Index(s, `"dial": "jellyfin:8096"`) {
		t.Fatal("/api route must precede the catch-all route of the same host")
	}
	if strings.LastIndex(s, `"status_code": 404`) < strings.Index(s, `"dial": "api:3000"`) {
		t.Fatal("404 fallback must be last")
	}
}

// The forward-auth block must strip client-supplied identity headers before
// copying Authelia's.
func TestForwardAuthStripsSpoofedHeaders(t *testing.T) {
	fa := forwardAuth("authelia:9091")
	b, _ := json.Marshal(fa)
	s := string(b)
	del := strings.Index(s, `"delete":["Remote-User","Remote-Groups","Remote-Email","Remote-Name"]`)
	set := strings.Index(s, `"set":{"Remote-User"`)
	if del < 0 || set < 0 || del > set {
		t.Fatalf("identity headers not deleted before being set: %s", s)
	}
	if !strings.Contains(s, `"X-Forwarded-Uri":["{http.request.uri}"]`) {
		t.Fatal("X-Forwarded-Uri must be overwritten from the real request")
	}
}

func TestUIProtectedByAutheliaByDefault(t *testing.T) {
	m := build(t, baseInput())
	routes := m["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)["edge"].(map[string]any)["routes"].([]any)
	first, _ := json.Marshal(routes[0])
	if !strings.Contains(string(first), "marineris.example.com") || !strings.Contains(string(first), "forward-auth") {
		t.Fatal("UI route must be first and protected by Authelia")
	}
	if !strings.Contains(string(first), "marineris_login") {
		t.Fatal("login endpoints must be rate limited")
	}
}

func TestUIHostOutsideDomainsIsNotServed(t *testing.T) {
	in := baseInput()
	in.Settings.UIHost = "ui.other.com"
	b, err := Build(in)
	if err != nil {
		t.Fatalf("a UI host set before its domain exists must not block the configuration: %v", err)
	}
	if strings.Contains(string(b), "ui.other.com") || strings.Contains(string(b), "marineris:8080") {
		t.Fatal("UI served for a host outside the managed domains")
	}
}

func TestNoUIRouteBeforeHostIsSet(t *testing.T) {
	in := baseInput()
	in.Settings.UIHost = ""
	b, _ := Build(in)
	if strings.Contains(string(b), "marineris:8080") {
		t.Fatal("UI exposed without a host")
	}
}

func TestCrowdSecToggle(t *testing.T) {
	in := baseInput()
	in.Settings.CrowdSecEnabled = false
	b, _ := Build(in)
	if strings.Contains(string(b), "crowdsec") {
		t.Fatal("crowdsec rendered while disabled")
	}
	in = baseInput()
	in.Env.BouncerKey = ""
	b, _ = Build(in)
	if strings.Contains(string(b), `"handler": "crowdsec"`) {
		t.Fatal("crowdsec handler without bouncer key would fail every request")
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]func(*Input){
		"route outside zones":  func(in *Input) { in.Routes[0].Host = "media.evil.com" },
		"route on UI host":     func(in *Input) { in.Routes[0].Host = "marineris.example.com" },
		"route on portal":      func(in *Input) { in.Routes[0].Host = "auth.example.com" },
		"duplicate host+path":  func(in *Input) { in.Routes[1].PathPrefix = "" },
		"bad container":        func(in *Input) { in.Routes[0].Target.Container = "a b" },
		"bad port":             func(in *Input) { in.Routes[0].Target.Port = 0 },
		"mixed target":         func(in *Input) { in.Routes[0].Target.URL = "http://x" },
		"loopback upstream":    func(in *Input) { in.Routes[2].Target.URL = "http://127.0.0.1:80" },
		"traversal folder":     func(in *Input) { in.Routes[3].Target.Folder = ".." },
		"bad kind":             func(in *Input) { in.Routes[0].Kind = "exec" },
		"bad auth":             func(in *Input) { in.Routes[0].Auth = "maybe" },
		"injection in host":    func(in *Input) { in.Routes[0].Host = `x.example.com"},{"handler":"x` },
		"placeholder in path":  func(in *Input) { in.Routes[1].PathPrefix = "/{env.SECRET}" },
		"wildcard without dns": func(in *Input) { in.Domains[2].Wildcard = true },
		"nested domains":       func(in *Input) { in.Domains = append(in.Domains, Domain{Name: "sub.example.com"}) },
		"duplicate domain":     func(in *Input) { in.Domains = append(in.Domains, Domain{Name: "plain.net"}) },
		"bad dns config":       func(in *Input) { in.Domains[0].DNS.Config = dnsprofiles.Config{} },
		"no email":             func(in *Input) { in.Settings.ACMEEmail = "" },
		"http acme":            func(in *Input) { in.Settings.ACMEDirectory = "http://pebble:14000/dir" },
		"bad root path":        func(in *Input) { in.Settings.ACMETrustedRootFile = "../root.pem" },
		"bad resolver":         func(in *Input) { in.Settings.DNSResolvers = []string{"1.1.1.1"} },
		"bad rate limit":       func(in *Input) { in.Routes[1].RateLimit = &store.RateLimit{Events: 5, Window: "1 minute"} },
		"bad auth subdomain":   func(in *Input) { in.Settings.AuthSubdomain = "a.b" },
		"ui host invalid":      func(in *Input) { in.Settings.UIHost = "ui..other.com" },
	}
	for name, mut := range cases {
		in := baseInput()
		mut(&in)
		_, err := Build(in)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: expected validation error, got %v", name, err)
		}
	}
}

func FuzzRouteFieldsCannotInjectJSON(f *testing.F) {
	f.Add("app.example.com", "/api", "web", "site", "http://10.0.0.1:80")
	f.Fuzz(func(t *testing.T, host, prefix, container, folder, url string) {
		in := baseInput()
		in.Routes = []*store.Route{
			{ID: 1, Name: "a", Host: host, PathPrefix: prefix, Kind: store.RouteContainer, Target: store.RouteTarget{Container: container, Port: 80}, Auth: store.AuthNone, Enabled: true},
			{ID: 2, Name: "b", Host: "b.example.com", Kind: store.RouteStatic, Target: store.RouteTarget{Folder: folder}, Auth: store.AuthNone, Enabled: true},
			{ID: 3, Name: "c", Host: "c.example.com", Kind: store.RouteExternal, Target: store.RouteTarget{URL: url}, Auth: store.AuthNone, Enabled: true},
		}
		b, err := Build(in)
		if err != nil {
			return
		}
		// Whatever was accepted must not have introduced extra handlers or
		// placeholders beyond the ones Marineris itself writes.
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			t.Fatal("invalid JSON produced")
		}
		for _, v := range []string{host, prefix, container, folder} {
			if strings.ContainsAny(v, `{}"\`) {
				t.Fatalf("structural characters accepted: %q", v)
			}
		}
	})
}

// TestCaddyValidate runs the real `caddy validate` when CADDY_BIN points to
// a Marineris Caddy build (CI does this). It proves every module and field
// name in the generated config exists.
func TestCaddyValidate(t *testing.T) {
	bin := os.Getenv("CADDY_BIN")
	if bin == "" {
		t.Skip("CADDY_BIN not set")
	}
	in := baseInput()
	in.Env.AdminListen = "localhost:0"
	in.Env.AccessLog = filepath.Join(t.TempDir(), "access.log")
	b, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "caddy.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "validate", "--config", p).CombinedOutput()
	if err != nil {
		t.Fatalf("caddy validate failed: %v\n%s", err, out)
	}
}
