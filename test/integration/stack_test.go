//go:build integration

// Package integration exercises a running Marineris stack (see run.sh):
// first-run setup, certificates from a test ACME CA, routing, Authelia
// forward-auth, the stack security policy, the socket proxy allow-list and
// automatic rollback of a configuration Caddy rejects.
package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
)

const (
	password = "Valles-Marineris-IT-2026!"
	zone     = "example.test"
)

var (
	localURL = env("IT_LOCAL_URL", "http://127.0.0.1:8081")
	edgeAddr = env("IT_EDGE_ADDR", "127.0.0.1:443")
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type api struct {
	t      *testing.T
	cookie string
	csrf   string
}

func (a *api) do(method, path string, body any) (int, map[string]any, []byte) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, localURL+path, rd)
	req.Header.Set("Origin", localURL)
	req.Header.Set("Content-Type", "application/json")
	if a.cookie != "" {
		req.Header.Set("Cookie", auth.SessionCookie+"="+a.cookie)
	}
	if a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	res, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, c := range res.Cookies() {
		if c.Name == auth.SessionCookie {
			a.cookie = c.Value
		}
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if tok, ok := m["csrf_token"].(string); ok {
		a.csrf = tok
	}
	return res.StatusCode, m, raw
}

func (a *api) must(method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, raw := a.do(method, path, body)
	if code != 200 {
		a.t.Fatalf("%s %s: HTTP %d: %s", method, path, code, raw)
	}
	return m
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func waitFor(t *testing.T, what string, timeout time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = fn(); err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s: %v", what, err)
}

// edgeClient talks to Caddy's published HTTPS port with any SNI.
func edgeClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, edgeAddr)
			},
			// The certificate comes from the throw-away Pebble CA; its
			// issuer is checked explicitly in TestStack.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test against Pebble
		},
	}
}

func edgeGet(t *testing.T, url string, header map[string]string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := edgeClient().Do(req)
	if err != nil {
		return 0, err.Error(), nil
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

func poll(timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return err
}

// clientIPSeenByCaddy reads the client address of the latest request from
// Caddy's JSON access log.
func clientIPSeenByCaddy(t *testing.T) string {
	t.Helper()
	edgeGet(t, "https://web."+zone+"/", nil)
	var ip string
	waitFor(t, "access log entry", 30*time.Second, func() error {
		out, err := exec.Command("docker", "exec", "marineris-caddy", "tail", "-n", "1", "/var/log/caddy/access.log").CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		var entry struct {
			ClientIP string `json:"client_ip"`
			Request  struct {
				RemoteIP string `json:"remote_ip"`
			} `json:"request"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(out), &entry); err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		ip = entry.ClientIP
		if ip == "" {
			ip = entry.Request.RemoteIP
		}
		if ip == "" {
			return fmt.Errorf("no client IP in %s", out)
		}
		return nil
	})
	return ip
}

func crowdsecDiagnostics(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{
		{"exec", "marineris-crowdsec", "cscli", "decisions", "list"},
		{"exec", "marineris-crowdsec", "cscli", "bouncers", "list"},
		{"exec", "marineris-caddy", "tail", "-n", "3", "/var/log/caddy/access.log"},
	} {
		out, _ := exec.Command("docker", args...).CombinedOutput()
		t.Logf("docker %s:\n%s", strings.Join(args, " "), out)
	}
	out, _ := exec.Command("sh", "-c", "docker logs marineris-caddy 2>&1 | grep -i crowdsec | tail -n 30").CombinedOutput()
	t.Logf("caddy crowdsec logs:\n%s", out)
}

func servedIssuer(host string) (string, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", edgeAddr,
		&tls.Config{ServerName: host, InsecureSkipVerify: true}) //nolint:gosec // inspection only
	if err != nil {
		return "", err
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].Issuer.CommonName, nil
}

func TestStack(t *testing.T) {
	a := &api{t: t}
	waitFor(t, "marineris", 2*time.Minute, func() error {
		code, _, _ := a.do("GET", "/api/health", nil)
		if code != 200 {
			return fmt.Errorf("health %d", code)
		}
		return nil
	})

	// --- First-run setup through the local listener --------------------
	logs := docker(t, "logs", "marineris")
	m := regexp.MustCompile(`"setup_token":"([A-Za-z0-9_-]+)"`).FindAllStringSubmatch(logs, -1)
	if len(m) == 0 {
		t.Fatal("setup token not found in the logs")
	}
	token := m[len(m)-1][1]
	begin := a.must("POST", "/api/setup/begin", map[string]string{"setup_token": token, "username": "admin"})
	secret := begin["totp_secret"].(string)
	code, _ := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	a.must("POST", "/api/setup/finish", map[string]string{
		"setup_token": token, "username": "admin", "password": password,
		"totp_code": code, "acme_email": "it@example.test",
	})
	if c, _, _ := a.do("POST", "/api/setup/begin", map[string]string{"setup_token": token}); c != 409 {
		t.Fatalf("setup must not run twice, got %d", c)
	}

	crowdsec := os.Getenv("IT_CROWDSEC") == "1"
	if crowdsec {
		// The bouncer needs a running LAPI with the Caddy bouncer registered.
		waitFor(t, "crowdsec bouncer registration", 5*time.Minute, func() error {
			out, err := exec.Command("docker", "exec", "marineris-crowdsec", "cscli", "bouncers", "list", "-o", "raw").CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, out)
			}
			if !strings.Contains(string(out), "caddy") {
				return fmt.Errorf("bouncer not registered: %s", out)
			}
			return nil
		})
	}

	// --- Point ACME at Pebble ------------------------------------------
	settings := a.must("GET", "/api/settings", nil)
	settings["acme_directory"] = "https://pebble:14000/dir"
	settings["acme_trusted_root_file"] = "/it-certs/root.pem"
	settings["crowdsec_enabled"] = crowdsec
	delete(settings, "setup_complete")
	a.must("PUT", "/api/settings", settings)

	// --- Domain (HTTP-01), demo stack and routes -------------------------
	a.must("POST", "/api/domains", map[string]any{"name": zone})

	// The policy refuses dangerous stacks before anything reaches Docker.
	c, body, _ := a.do("POST", "/api/stacks/check", map[string]any{"name": "evil", "compose": "services:\n  x:\n    image: alpine:3\n    volumes: ['/var/run/docker.sock:/var/run/docker.sock']\n"})
	if c != 200 || len(body["blocking"].([]any)) == 0 {
		t.Fatalf("docker.sock mount not flagged: %v", body)
	}

	compose := `services:
  web:
    image: caddy:2.11-alpine
    container_name: it-web
    command: ["caddy", "respond", "--listen", ":8000", "hello user=[{http.request.header.Remote-User}]"]
    networks: [default, apps]
networks:
  apps:
    external: true
    name: marineris-apps
`
	a.must("POST", "/api/stacks", map[string]any{"name": "demo", "compose": compose})
	a.must("POST", "/api/stacks/demo/deploy", nil)

	a.must("POST", "/api/routes", map[string]any{"name": "web", "host": "web." + zone, "kind": "container",
		"target": map[string]any{"container": "it-web", "port": 8000}, "auth": "none", "enabled": true})
	a.must("POST", "/api/routes", map[string]any{"name": "secure", "host": "secure." + zone, "kind": "container",
		"target": map[string]any{"container": "it-web", "port": 8000}, "auth": "authelia", "enabled": true})
	a.must("POST", "/api/routes", map[string]any{"name": "ops", "host": "ops." + zone, "kind": "container",
		"target": map[string]any{"container": "it-web", "port": 8000}, "auth": "admins", "enabled": true})
	if cur := a.must("GET", "/api/config/components/authelia/current", nil); !strings.Contains(fmt.Sprint(cur["content"]), "group:admins") {
		t.Fatal("the admin-only route has no group rule in the Authelia configuration")
	}

	// --- Certificates are issued by the test CA -------------------------
	for _, host := range []string{"web." + zone, "secure." + zone, "ops." + zone, "auth." + zone} {
		waitFor(t, "certificate for "+host, 3*time.Minute, func() error {
			iss, err := servedIssuer(host)
			if err != nil {
				return err
			}
			if !strings.Contains(strings.ToLower(iss), "pebble") {
				return fmt.Errorf("issuer %q", iss)
			}
			return nil
		})
	}

	// --- Routing ----------------------------------------------------------
	status, out, _ := edgeGet(t, "https://web."+zone+"/", nil)
	if status != 200 || !strings.Contains(out, "hello user=[]") {
		t.Fatalf("public route: %d %q", status, out)
	}
	// Protected route: anonymous requests are sent to the Authelia portal,
	// and a forged identity header changes nothing.
	// Authelia answers browsers with 302 and API clients with 401; both
	// point to the portal and never reach the application.
	for _, host := range []string{"secure." + zone, "ops." + zone} {
		for _, h := range []map[string]string{
			nil,
			{"Accept": "text/html"},
			{"Remote-User": "admin"},
			{"Remote-User": "admin", "Remote-Groups": "admins", "X-Forwarded-User": "admin"},
		} {
			status, out, hdr := edgeGet(t, "https://"+host+"/", h)
			if (status != 302 && status != 401) || !strings.HasPrefix(hdr.Get("Location"), "https://auth."+zone) || strings.Contains(out, "hello user") {
				t.Fatalf("protected route %s with %v: %d %q", host, h, status, out)
			}
			if h["Accept"] == "text/html" && status != 302 {
				t.Fatalf("browsers must be redirected, got %d", status)
			}
		}
	}
	waitFor(t, "authelia portal", time.Minute, func() error {
		status, _, _ := edgeGet(t, "https://auth."+zone+"/", nil)
		if status != 200 {
			return fmt.Errorf("portal status %d", status)
		}
		return nil
	})
	// --- Signing in survives Authelia's restarts (sessions in Valkey) ------
	sessionSurvivesRestarts(t, a, secret)

	// Unknown hosts get nothing useful.
	if status, _, _ := edgeGet(t, "https://unknown."+zone+"/", nil); status == 200 {
		t.Fatal("unknown host served content")
	}

	// --- Stack services: status, edit and recreate, restart, update -------
	sv := a.must("GET", "/api/stacks/demo/services", nil)
	if sv["main"] != "web" || sv["state"] != "running" {
		t.Fatalf("stack services: %v", sv)
	}
	before := a.must("GET", "/api/containers/it-web", nil)
	a.must("PUT", "/api/stacks/demo/services/web", map[string]any{
		"environment": []map[string]string{{"key": "GREETING", "value": "hello"}},
		"labels":      []map[string]string{{"key": "io.marineris.main", "value": "true"}},
		"mounts":      []map[string]any{{"type": "volume", "source": "files", "target": "/srv/files", "read_only": true}},
		"deploy":      true,
	})
	after := a.must("GET", "/api/containers/it-web", nil)
	if after["id"] == before["id"] || !strings.Contains(fmt.Sprint(after["env"]), "GREETING=") {
		t.Fatalf("the edited service was not recreated with its new environment: %v", after["env"])
	}
	if !strings.Contains(fmt.Sprint(after["mounts"]), "demo_files:/srv/files") || fmt.Sprint(after["labels"].(map[string]any)["io.marineris.main"]) != "true" {
		t.Fatalf("the new volume or label is missing: %v %v", after["mounts"], after["labels"])
	}
	a.must("POST", "/api/stacks/demo/services/web/restart", nil)
	a.must("POST", "/api/stacks/demo/services/web/update", nil)
	waitFor(t, "the recreated service behind its route", time.Minute, func() error {
		if status, _, _ := edgeGet(t, "https://web."+zone+"/", nil); status != 200 {
			return fmt.Errorf("status %d", status)
		}
		return nil
	})

	// --- Renaming a running stack keeps its data and its routes ----------
	a.must("POST", "/api/stacks/demo/rename", map[string]any{"name": "demo-renamed"})
	moved := a.must("GET", "/api/containers/it-web", nil)
	if moved["stack"] != "demo-renamed" || !strings.Contains(fmt.Sprint(moved["mounts"]), "demo_files:/srv/files") {
		t.Fatalf("renamed stack: stack %v, mounts %v (the volume must keep its name and data)", moved["stack"], moved["mounts"])
	}
	waitFor(t, "the renamed stack behind its route", time.Minute, func() error {
		if status, _, _ := edgeGet(t, "https://web."+zone+"/", nil); status != 200 {
			return fmt.Errorf("status %d", status)
		}
		return nil
	})

	// --- CrowdSec: a ban decision blocks the client at the edge ----------
	if crowdsec {
		cur := a.must("GET", "/api/config/components/caddy/current", nil)
		if !strings.Contains(fmt.Sprint(cur["content"]), `"handler": "crowdsec"`) {
			t.Fatal("the CrowdSec handler is not in the Caddy configuration")
		}
		ip := clientIPSeenByCaddy(t)
		t.Logf("Caddy sees this client as %s", ip)
		docker(t, "exec", "marineris-crowdsec", "cscli", "decisions", "add", "--ip", ip, "--duration", "10m", "--reason", "integration-test")
		if err := poll(2*time.Minute, func() error {
			if status, _, _ := edgeGet(t, "https://web."+zone+"/", nil); status != 403 {
				return fmt.Errorf("status %d", status)
			}
			return nil
		}); err != nil {
			crowdsecDiagnostics(t)
			t.Fatalf("ban not enforced: %v", err)
		}
		docker(t, "exec", "marineris-crowdsec", "cscli", "decisions", "delete", "--ip", ip)
		waitFor(t, "ban to be lifted", 2*time.Minute, func() error {
			if status, _, _ := edgeGet(t, "https://web."+zone+"/", nil); status != 200 {
				return fmt.Errorf("status %d", status)
			}
			return nil
		})
	}

	// --- A configuration Caddy rejects is rolled back --------------------
	settings = a.must("GET", "/api/settings", nil)
	delete(settings, "setup_complete")
	good := settings["acme_trusted_root_file"]
	settings["acme_trusted_root_file"] = "/it-certs/does-not-exist.pem"
	if c, body, raw := a.do("PUT", "/api/settings", settings); c != 502 || body["code"] != "apply_failed" {
		t.Fatalf("expected apply failure, got %d %s", c, raw)
	}
	if now := a.must("GET", "/api/settings", nil); now["acme_trusted_root_file"] != good {
		t.Fatal("settings not reverted after failed apply")
	}
	if status, out, _ := edgeGet(t, "https://web."+zone+"/", nil); status != 200 {
		t.Fatalf("traffic broken after rejected config: %d %q", status, out)
	}

	// --- Overview and audit ---------------------------------------------
	ov := a.must("GET", "/api/overview", nil)
	if ov["docker_ok"] != true {
		t.Fatalf("docker not reachable through the proxy: %v", ov)
	}
	_, _, auditRaw := a.do("GET", "/api/audit", nil)
	for _, action := range []string{"setup.finish", "route.create", "stack.deploy", "settings.update"} {
		if !strings.Contains(string(auditRaw), action) {
			t.Errorf("audit log misses %s", action)
		}
	}
}

// sessionSurvivesRestarts signs in through the Authelia portal, then edits a
// route (which changes Authelia's configuration and restarts it) and
// restarts Valkey: the same session must still open the protected route.
func sessionSurvivesRestarts(t *testing.T, a *api, secret string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := edgeClient()
	c.Jar = jar
	post := func(path string, body map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "https://auth."+zone+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, raw)
		}
	}
	target := "https://secure." + zone + "/"
	signedIn := func() error {
		res, err := c.Get(target)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || !strings.Contains(string(b), "hello user=[admin]") {
			return fmt.Errorf("status %d %q", res.StatusCode, b)
		}
		return nil
	}
	waitFor(t, "the authenticator registered in Authelia", 2*time.Minute, func() error {
		// Authelia refuses a code it has seen: a new one each time.
		code, _ := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
		post("/api/firstfactor", map[string]any{"username": "admin", "password": password, "keepMeLoggedIn": false, "targetURL": target})
		b, _ := json.Marshal(map[string]any{"token": code, "targetURL": target})
		req, _ := http.NewRequest("POST", "https://auth."+zone+"/api/secondfactor/totp", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			time.Sleep(30 * time.Second)
			return fmt.Errorf("second factor: %d", res.StatusCode)
		}
		return signedIn()
	})

	startedAt := func() string {
		return strings.TrimSpace(docker(t, "inspect", "-f", "{{.State.StartedAt}}", "marineris-authelia"))
	}
	before := startedAt()
	a.must("POST", "/api/routes", map[string]any{"name": "secure2", "host": "secure2." + zone, "kind": "container",
		"target": map[string]any{"container": "it-web", "port": 8000}, "auth": "authelia", "enabled": true})
	if startedAt() == before {
		t.Fatal("the route change did not restart Authelia; the test proves nothing")
	}
	waitFor(t, "the session after Authelia's restart", time.Minute, signedIn)

	docker(t, "restart", "marineris-valkey")
	waitFor(t, "the session after Valkey's restart", time.Minute, signedIn)
}

// TestSocketProxyAllowList checks the Docker API restrictions from inside
// the Marineris network namespace (the only allowed client).
func TestSocketProxyAllowList(t *testing.T) {
	probe := func(netns, method, path, body string) string {
		args := []string{"run", "--rm", "--network", netns, "--entrypoint", "wget", "marineris-caddy:" + env("IT_TAG", "dev"),
			"-q", "-S", "-O", "/dev/null", "-T", "10"}
		if method == "POST" {
			args = append(args, "--header", "Content-Type: application/json", "--post-data", body)
		}
		args = append(args, "http://socket-proxy:2375"+path)
		out, _ := exec.Command("docker", args...).CombinedOutput()
		if m := regexp.MustCompile(`HTTP/1\.1 (\d+)`).FindStringSubmatch(string(out)); m != nil {
			return m[1]
		}
		return "error: " + strings.TrimSpace(string(out))
	}
	ns := "container:marineris"
	allowed := map[string]string{
		"/v1.44/_ping":                     "200",
		"/v1.44/containers/json":           "200",
		"/v1.44/containers/marineris/json": "200",
	}
	for path, want := range allowed {
		if got := probe(ns, "GET", path, ""); got != want {
			t.Errorf("GET %s: got %s, want %s", path, got, want)
		}
	}
	denied := []struct{ method, path, body string }{
		{"POST", "/v1.44/containers/marineris/exec", `{"Cmd":["id"]}`},
		{"POST", "/v1.44/build", "{}"},
		{"POST", "/v1.44/commit?container=marineris", "{}"},
		{"GET", "/v1.44/containers/marineris/archive?path=/", ""},
		{"GET", "/v1.44/containers/marineris/export", ""},
		{"POST", "/v1.44/containers/marineris/attach", "{}"},
		{"GET", "/v1.44/secrets", ""},
		{"GET", "/v1.44/swarm", ""},
		{"GET", "/v1.44/plugins", ""},
		{"POST", "/v1.44/containers/marineris/update", "{}"},
		// Bind mounts outside the allowed roots are refused by the proxy itself.
		{"POST", "/v1.44/containers/create", `{"Image":"alpine:3","HostConfig":{"Binds":["/:/host"]}}`},
		{"POST", "/v1.44/containers/create", `{"Image":"alpine:3","HostConfig":{"Binds":["/var/run/docker.sock:/s"]}}`},
	}
	for _, d := range denied {
		if got := probe(ns, d.method, d.path, d.body); got != "403" {
			t.Errorf("%s %s: got %s, want 403", d.method, d.path, got)
		}
	}
	// Any other container on the docker-api network is refused entirely.
	if got := probe("marineris_docker-api", "GET", "/v1.44/_ping", ""); got != "403" {
		t.Errorf("foreign client on docker-api network: got %s, want 403", got)
	}
}

// TestHardening checks the runtime security settings of the stack.
func TestHardening(t *testing.T) {
	for _, name := range []string{"marineris", "marineris-caddy", "marineris-authelia", "marineris-valkey", "marineris-socket-proxy"} {
		var info []struct {
			Config struct {
				User string
			}
			HostConfig struct {
				Privileged     bool
				ReadonlyRootfs bool
				CapDrop        []string
				CapAdd         []string
				SecurityOpt    []string
				PortBindings   map[string][]struct{ HostIp, HostPort string }
			}
		}
		if err := json.Unmarshal([]byte(docker(t, "inspect", name)), &info); err != nil || len(info) != 1 {
			t.Fatalf("inspect %s: %v", name, err)
		}
		hc := info[0].HostConfig
		if hc.Privileged || !hc.ReadonlyRootfs || len(hc.CapAdd) != 0 || !contains(hc.CapDrop, "ALL") || !contains(hc.SecurityOpt, "no-new-privileges:true") {
			t.Errorf("%s is not hardened: %+v", name, hc)
		}
		if u := info[0].Config.User; u == "" || strings.HasPrefix(u, "0") || u == "root" {
			t.Errorf("%s runs as root (%q)", name, u)
		}
		if name == "marineris" {
			for port, binds := range hc.PortBindings {
				for _, b := range binds {
					if b.HostIp != "127.0.0.1" {
						t.Errorf("marineris publishes %s on %q: must be loopback only", port, b.HostIp)
					}
				}
			}
		}
	}
	mounts := docker(t, "inspect", "--format", "{{range .Mounts}}{{.Source}} {{end}}", "marineris")
	if strings.Contains(mounts, "docker.sock") {
		t.Fatal("the Docker socket must never be mounted into Marineris")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
