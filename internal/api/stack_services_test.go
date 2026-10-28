package api

import (
	"net/http"
	"strings"
	"testing"
)

const testCompose = `services:
  helper:
    image: alpine:3
  jellyfin:
    image: jellyfin/jellyfin:10.11
    environment:
      TZ: UTC
`

func TestStackServices(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	must := func(method, path string, body any) resp {
		t.Helper()
		r := admin.do(method, path, body)
		if r.Status != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Raw)
		}
		return r
	}
	must("POST", "/api/stacks", map[string]any{"name": "media", "compose": testCompose})

	// Services in file order, with their containers and status; without a
	// label or a route, the first service is the main one.
	r := must("GET", "/api/stacks/media/services", nil)
	if r.Body["main"] != "helper" || r.Body["state"] != "partial" {
		t.Fatalf("summary: %s", r.Raw)
	}
	svcs := r.Body["services"].([]any)
	j := svcs[1].(map[string]any)
	if j["name"] != "jellyfin" || j["state"] != "running" || len(j["containers"].([]any)) != 1 || j["image"] != "jellyfin/jellyfin:10.11" {
		t.Fatalf("jellyfin: %v", j)
	}
	if h := svcs[0].(map[string]any); h["state"] != "stopped" {
		t.Fatalf("helper: %v", h)
	}
	list := must("GET", "/api/stacks", nil)
	if !strings.Contains(string(list.Raw), `"state":"partial"`) || !strings.Contains(string(list.Raw), `"main":"helper"`) {
		t.Fatalf("stack list: %s", list.Raw)
	}

	// Edit a service: the compose file changes, the other service does not.
	r = must("PUT", "/api/stacks/media/services/jellyfin", map[string]any{
		"environment": []map[string]any{{"key": "TZ", "value": "Europe/Paris"}},
		"ports":       []string{"8096:8096"},
		"deploy":      true,
	})
	compose := r.Body["compose"].(string)
	if !strings.Contains(compose, `TZ: "Europe/Paris"`) || !strings.Contains(compose, `"8096:8096"`) || !strings.Contains(compose, "image: alpine:3") {
		t.Fatalf("compose after edit:\n%s", compose)
	}
	// Labels and typed mounts; a new named volume is declared in the file.
	r = must("PUT", "/api/stacks/media/services/jellyfin", map[string]any{
		"labels": []map[string]any{{"key": "io.marineris.main", "value": "true"}},
		"mounts": []map[string]any{{"type": "volume", "source": "library", "target": "/config"}, {"type": "bind", "source": "/srv/films", "target": "/films", "read_only": true}},
	})
	compose = r.Body["compose"].(string)
	for _, want := range []string{"- library:/config", "- /srv/films:/films:ro", "library: {}", `io.marineris.main: "true"`} {
		if !strings.Contains(compose, want) {
			t.Fatalf("missing %q in\n%s", want, compose)
		}
	}
	sum := must("GET", "/api/stacks/media/services", nil)
	if sum.Body["main"] != "jellyfin" || !strings.Contains(string(sum.Raw), `"volumes":["library"]`) || !strings.Contains(string(sum.Raw), `"bind_roots":["/srv"]`) {
		t.Fatalf("summary after labels and mounts: %s", sum.Raw)
	}
	if r := admin.do("PUT", "/api/stacks/media/services/jellyfin", map[string]any{"mounts": []map[string]any{{"type": "bind", "source": "/etc", "target": "/x"}}}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("bind mount of /etc: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PUT", "/api/stacks/media/services/jellyfin", map[string]any{"labels": []map[string]any{{"key": "io.marineris.system", "value": "true"}}}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("system label: %d %s", r.Status, r.Raw)
	}

	// The policy still applies.
	if r := admin.do("PUT", "/api/stacks/media/services/jellyfin", map[string]any{"volumes": []string{"/var/run/docker.sock:/var/run/docker.sock"}}); r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("docker socket accepted: %d %s", r.Status, r.Raw)
	}
	if r := admin.do("PUT", "/api/stacks/media/services/nope", map[string]any{"image": "x"}); r.Status != http.StatusNotFound {
		t.Fatalf("unknown service: %d", r.Status)
	}
	if r := admin.do("PUT", "/api/stacks/media/services/jellyfin", map[string]any{"privileged": true}); r.Status != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", r.Status)
	}

	// Actions apply to the service's containers only.
	must("POST", "/api/stacks/media/services/jellyfin/restart", nil)
	must("POST", "/api/stacks/media/services/jellyfin/recreate", nil)
	must("POST", "/api/stacks/media/services/jellyfin/update", nil)
	must("POST", "/api/stacks/media/restart", nil)
	must("POST", "/api/stacks/media/pull", nil)
	e.srv.Wait()
	e.docker.mu.Lock()
	var stackActions []string
	for _, a := range e.docker.actions {
		if strings.Contains(a, ":media-") {
			stackActions = append(stackActions, a)
		}
	}
	e.docker.mu.Unlock()
	if got := strings.Join(stackActions, ","); got != "restart:media-jellyfin-1,restart:media-jellyfin-1,restart:media-helper-1" {
		t.Fatalf("docker actions: %s", got)
	}

	// Icons: upload, served (sandboxed), shown as the service's, removed.
	must("PUT", "/api/stacks/media/icons/jellyfin", map[string]string{"data": pngPixel})
	r = must("GET", "/api/stacks/media/services", nil)
	icon, _ := r.Body["services"].([]any)[1].(map[string]any)["icon"].(string)
	if !strings.HasPrefix(icon, "/api/stacks/media/icons/jellyfin?v=") {
		t.Fatalf("icon url %q", icon)
	}
	if r := must("GET", icon, nil); r.Header.Get("Content-Type") != "image/png" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("icon headers %v", r.Header)
	}
	must("DELETE", "/api/stacks/media/icons/jellyfin", nil)
	must("PUT", "/api/stacks/media/icons/jellyfin", map[string]string{"data": pngPixel})
	must("DELETE", "/api/stacks/media", nil)
	if _, err := e.store.GetImage(t.Context(), "service", "media/jellyfin"); err == nil {
		t.Fatal("icons of a deleted stack are kept")
	}
}
