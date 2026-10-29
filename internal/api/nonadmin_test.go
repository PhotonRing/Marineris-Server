package api

import (
	"context"
	"sort"
	"testing"

	"github.com/photonring/marineris-server/internal/store"
)

// hubEnv: setup done, the UI behind Authelia, three apps on the Hub (one
// public, one for signed-in Users, one for Administrators only) and carol,
// a User who is not an Administrator. It returns the Owner's client.
func hubEnv(t *testing.T) (*env, *client) {
	e := newEnv(t)
	e.completeSetup()
	if err := e.store.CreateDomain(context.Background(), &store.Domain{Name: "example.com"}); err != nil {
		t.Fatal(err)
	}
	e.setAutheliaProtection(true)
	e.addUser("carol", "family")
	owner := e.autheliaClient(testUser)
	owner.do("GET", "/api/auth/session", nil)
	for _, app := range []struct{ name, auth string }{
		{"blog", store.AuthNone}, {"photos", store.AuthAuthelia}, {"grafana", store.AuthAdmins},
	} {
		r := owner.do("POST", "/api/routes", map[string]any{"name": app.name, "host": app.name + ".example.com", "kind": "spa",
			"target": map[string]any{"folder": app.name}, "auth": app.auth, "enabled": true})
		if r.Status != 200 && r.Status != 201 {
			t.Fatalf("route %s: %d %s", app.name, r.Status, r.Raw)
		}
	}
	return e, owner
}

func hubTitles(r resp) []string {
	out := []string{}
	tiles, _ := r.Body["tiles"].([]any)
	for _, t := range tiles {
		out = append(out, t.(map[string]any)["title"].(string))
	}
	sort.Strings(out)
	return out
}

func TestHubShowsANonAdministratorOnlyTheAppsTheyMayOpen(t *testing.T) {
	e, owner := hubEnv(t)
	carol := e.autheliaClient("carol")

	r := carol.do("GET", "/api/hub", nil)
	if r.Status != 200 {
		t.Fatalf("hub: %d %s", r.Status, r.Raw)
	}
	if got := hubTitles(r); len(got) != 2 || got[0] != "blog" || got[1] != "photos" {
		t.Errorf("carol's tiles: %v, want [blog photos]", got)
	}
	if w, _ := r.Body["widgets"].([]any); len(w) != 0 || r.Body["widgets"] == nil {
		t.Errorf("carol gets server widgets: %s", r.Raw)
	}
	if r.Body["title"] != "Marineris" {
		t.Errorf("hub title: %v", r.Body["title"])
	}

	r = owner.do("GET", "/api/hub", nil)
	if got := hubTitles(r); len(got) != 3 {
		t.Errorf("the Owner's tiles: %v, want all three", got)
	}
	if w, _ := r.Body["widgets"].([]any); len(w) != 6 {
		t.Errorf("the Owner's widgets: %v", r.Body["widgets"])
	}
}

// A User who is not an Administrator, signed in through Authelia, manages
// everything in their Account without anyone's help.
func TestNonAdministratorManagesTheirAccount(t *testing.T) {
	e, _ := hubEnv(t)
	secret := e.addUser("dave", "family")
	c := e.autheliaClient("dave")
	if r := c.do("GET", "/api/auth/session", nil); r.Status != 200 {
		t.Fatalf("session: %d %s", r.Status, r.Raw)
	}
	must := func(method, path string, body any) resp {
		t.Helper()
		r := c.do(method, path, body)
		if r.Status >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Raw)
		}
		return r
	}

	must("POST", "/api/auth/reauth", map[string]string{"password": testPassword, "code": e.freshCodeFor(secret)})
	must("POST", "/api/account/password", map[string]string{"current_password": testPassword, "new_password": "Another-long-passphrase-2026"})
	if codes, _ := must("POST", "/api/account/recovery-codes", nil).Body["recovery_codes"].([]any); len(codes) == 0 {
		t.Error("no recovery codes")
	}
	tb := must("POST", "/api/account/totp/begin", nil)
	must("POST", "/api/account/totp/finish", map[string]string{"code": e.freshCodeFor(tb.Body["totp_secret"].(string))})
	must("GET", "/api/account/passkeys", nil)
	must("GET", "/api/account/authelia-passkeys", nil)
	if u, _ := must("POST", "/api/account/authelia-passkeys", map[string]string{"name": "Phone"}).Body["url"].(string); u == "" {
		t.Error("no page to add an Authelia passkey")
	}
	must("POST", "/api/account/sessions/revoke-others", nil)
}

// Nothing writes one-time codes to Authelia's notifier any more: the
// Account card showing them and its endpoint are gone, for everyone.
func TestAutheliaNotificationEndpointIsGone(t *testing.T) {
	_, owner := hubEnv(t)
	if r := owner.do("GET", "/api/authelia/notification", nil); r.Status != 404 {
		t.Fatalf("notification endpoint: %d %s", r.Status, r.Raw)
	}
}

// An icon tells a route exists: a User gets only those of their own tiles.
func TestHubIconOfAnAppTheUserMayNotOpenIsNotFound(t *testing.T) {
	e, owner := hubEnv(t)
	svg := "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciLz4=" // <svg xmlns=…/>
	for _, id := range []string{"1", "2", "3", "4"} {
		if id == "4" {
			// A hidden route for signed-in Users.
			r := owner.do("POST", "/api/routes", map[string]any{"name": "hidden", "host": "hidden.example.com", "kind": "spa",
				"target": map[string]any{"folder": "hidden"}, "auth": store.AuthAuthelia, "enabled": true, "hub": map[string]any{"visible": false}})
			if r.Status != 200 && r.Status != 201 {
				t.Fatalf("hidden route: %d %s", r.Status, r.Raw)
			}
		}
		if r := owner.do("PUT", "/api/routes/"+id+"/icon", map[string]string{"data": svg}); r.Status != 200 {
			t.Fatalf("icon of route %s: %d %s", id, r.Status, r.Raw)
		}
	}
	carol := e.autheliaClient("carol")
	for id, want := range map[string]int{"1": 200, "2": 200, "3": 404, "4": 404, "99": 404} {
		if r := carol.do("GET", "/api/hub/icons/"+id, nil); r.Status != want {
			t.Errorf("carol, icon %s: %d, want %d", id, r.Status, want)
		}
	}
	for _, id := range []string{"1", "2", "3", "4"} {
		if r := owner.do("GET", "/api/hub/icons/"+id, nil); r.Status != 200 {
			t.Errorf("Owner, icon %s: %d", id, r.Status)
		}
	}
}
