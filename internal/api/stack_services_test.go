package api

import (
	"fmt"
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

func TestStackRenameAndRouteIcons(t *testing.T) {
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
	must("POST", "/api/domains", map[string]any{"name": "example.com"})
	must("POST", "/api/stacks", map[string]any{"name": "media", "compose": testCompose + "volumes:\n  library: {}\n", "env": "TZ=UTC\n"})
	must("PUT", "/api/stacks/media/icons/jellyfin", map[string]string{"data": pngPixel})
	rt := must("POST", "/api/routes", map[string]any{"name": "films", "host": "films.example.com", "kind": "container",
		"target": map[string]any{"container": "media-jellyfin-1", "port": 8096}, "auth": "none", "enabled": true})
	routeID := int64(rt.Body["id"].(float64))

	iconURL := func(id int64) string {
		t.Helper()
		for _, r := range must("GET", "/api/routes", nil).array() {
			if int64(r["id"].(float64)) == id {
				return r["icon"].(string)
			}
		}
		t.Fatalf("route %d not listed", id)
		return ""
	}
	served := func(id int64) string {
		t.Helper()
		r := admin.do("GET", fmt.Sprintf("/api/hub/icons/%d", id), nil)
		if r.Status != http.StatusOK {
			return ""
		}
		return string(r.Raw)
	}

	// Automatic: a route to a stack's container shows its service's icon,
	// without a copy of its own.
	if rt.Body["hub"].(map[string]any)["has_icon"] != false || iconURL(routeID) == "" || served(routeID) == "" {
		t.Fatalf("automatic icon: %s %q", rt.Raw, iconURL(routeID))
	}
	// It follows the service: a new service icon changes the route's.
	before := iconURL(routeID)
	must("PUT", "/api/stacks/media/icons/jellyfin", map[string]string{"data": "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4="})
	if iconURL(routeID) == before || !strings.Contains(served(routeID), "<svg") {
		t.Fatal("the route does not follow its service's icon")
	}
	// An icon of its own replaces it; removing it goes back to the service's.
	must("PUT", fmt.Sprintf("/api/routes/%d/icon", routeID), map[string]string{"data": pngPixel})
	if strings.Contains(served(routeID), "<svg") {
		t.Fatal("the route's own icon is not used")
	}
	must("DELETE", fmt.Sprintf("/api/routes/%d/icon", routeID), nil)
	if !strings.Contains(served(routeID), "<svg") {
		t.Fatal("back to automatic: the service's icon is not used")
	}

	// Explicit links: to a stack (its main service) or a service, checked.
	icons := must("GET", "/api/stack-icons", nil)
	if !strings.Contains(string(icons.Raw), `"stack":"media","service":"jellyfin"`) || strings.Contains(string(icons.Raw), `"service":"helper"`) {
		t.Fatalf("stack icons (services with an icon of their own): %s", icons.Raw)
	}
	for _, bad := range []map[string]string{{"stack": "media", "service": "nope"}, {"stack": "nope"}} {
		if r := admin.do("PUT", fmt.Sprintf("/api/routes/%d/icon", routeID), bad); r.Status != http.StatusBadRequest {
			t.Fatalf("link to %v: %d %s", bad, r.Status, r.Raw)
		}
	}
	other := must("POST", "/api/routes", map[string]any{"name": "web", "host": "web.example.com", "kind": "container",
		"target": map[string]any{"container": "web", "port": 80}, "auth": "none", "enabled": true})
	otherID := int64(other.Body["id"].(float64))
	if iconURL(otherID) != "" {
		t.Fatal("a route to a container outside the stacks has no icon by default")
	}
	must("PUT", fmt.Sprintf("/api/routes/%d/icon", otherID), map[string]string{"stack": "media", "service": "jellyfin"})
	if !strings.Contains(served(otherID), "<svg") {
		t.Fatal("linked icon not served")
	}

	// 0.1.3 migration: a route's own icon moves to its service when the
	// service has none, and the route follows it.
	helper := must("POST", "/api/routes", map[string]any{"name": "helper", "host": "helper.example.com", "kind": "container",
		"target": map[string]any{"container": "media-helper-1", "port": 80}, "auth": "none", "enabled": true})
	helperID := int64(helper.Body["id"].(float64))
	must("PUT", fmt.Sprintf("/api/routes/%d/icon", helperID), map[string]string{"data": pngPixel})
	if done, err := e.srv.MoveRouteIconsToServices(t.Context()); err != nil || !done {
		t.Fatalf("migration: %v %v", done, err)
	}
	if _, err := e.store.GetImage(t.Context(), "service", "media/helper"); err != nil {
		t.Fatal("the route icon did not move to its service")
	}
	if r, _ := e.store.GetRoute(t.Context(), helperID); r.Hub.HasIcon || served(helperID) == "" {
		t.Fatal("the route still has its own copy, or shows nothing")
	}
	// A route whose service already had an icon keeps its own.
	must("PUT", fmt.Sprintf("/api/routes/%d/icon", routeID), map[string]string{"data": pngPixel})
	if done, _ := e.srv.MoveRouteIconsToServices(t.Context()); !done {
		t.Fatal("the migration runs once")
	}
	if r, _ := e.store.GetRoute(t.Context(), routeID); !r.Hub.HasIcon {
		t.Fatal("a deliberate route icon was removed")
	}
	must("PUT", fmt.Sprintf("/api/routes/%d/icon", otherID), map[string]string{"stack": "media"})

	// Rename: refused names and conflicts first.
	must("POST", "/api/stacks", map[string]any{"name": "other", "compose": testCompose})
	for name, code := range map[string]int{"Bad Name": 400, "other": 409, "": 400} {
		if r := admin.do("POST", "/api/stacks/media/rename", map[string]string{"name": name}); r.Status != code {
			t.Errorf("rename to %q: %d %s", name, r.Status, r.Raw)
		}
	}
	must("POST", "/api/stacks/media/rename", map[string]string{"name": "films"})
	if r := admin.do("GET", "/api/stacks/media", nil); r.Status != http.StatusNotFound {
		t.Fatal("the old name still exists")
	}
	st := must("GET", "/api/stacks/films", nil)
	// The stack was deployed: its volumes keep their data under the old names,
	// the .env is still readable, the icons followed and the route too.
	if !strings.Contains(st.Body["compose"].(string), "name: media_library") || st.Body["env"] != "TZ=UTC\n" {
		t.Fatalf("renamed stack: %s", st.Raw)
	}
	if _, err := e.store.GetImage(t.Context(), "service", "films/jellyfin"); err != nil {
		t.Fatal("the service icon did not follow the rename")
	}
	if r, _ := e.store.GetRoute(t.Context(), otherID); r.Hub.IconLink != "films" {
		t.Fatalf("the route icon link did not follow the rename: %q", r.Hub.IconLink)
	}
	routes := must("GET", "/api/routes", nil)
	if !strings.Contains(string(routes.Raw), `"container":"films-jellyfin-1"`) {
		t.Fatalf("route target not renamed: %s", routes.Raw)
	}
	must("POST", "/api/stacks/films/start", nil)
}
