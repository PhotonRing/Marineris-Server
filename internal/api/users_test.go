package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/store"
)

// userID returns the ID of a User by Username.
func (e *env) userID(name string) int64 {
	e.t.Helper()
	u, err := e.store.GetUserByName(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// signedIn returns a client signed in through Authelia as user, with a
// fresh step-up.
func (e *env) signedIn(user string) *client {
	e.t.Helper()
	c := e.autheliaClient(user)
	if r := c.do("GET", "/api/auth/session", nil); r.Status != 200 {
		e.t.Fatalf("session of %s: %d %s", user, r.Status, r.Raw)
	}
	se, err := e.store.GetSession(context.Background(), auth.HashToken(c.cookie))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.MarkSessionAuth(context.Background(), se.IDHash, time.Now().UTC()); err != nil {
		e.t.Fatal(err)
	}
	return c
}

// usersEnv: the Owner (testUser), an Administrator bob and a User carol in
// family, with the UI behind Authelia.
func usersEnv(t *testing.T) *env {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	e.addUser("bob", store.GroupAdmins)
	e.addUser("carol", "family")
	return e
}

func userPath(id int64, rest string) string { return fmt.Sprintf("/api/users/%d%s", id, rest) }

func TestUsersListShowsGroupsStateAndOwner(t *testing.T) {
	e := usersEnv(t)
	r := e.signedIn("bob").do("GET", "/api/users", nil)
	if r.Status != 200 {
		t.Fatalf("list: %d %s", r.Status, r.Raw)
	}
	var got []string
	for _, u := range r.array() {
		got = append(got, fmt.Sprintf("%v %v %v %v %v owner=%v", u["username"], u["display_name"], u["email"], u["groups"], u["state"], u["owner"]))
	}
	want := []string{
		"admin admin  [admins] active owner=true",
		"bob bob  [admins] active owner=false",
		"carol carol  [family] active owner=false",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if _, leak := r.array()[0]["password_hash"]; leak {
		t.Fatal("password hash in the list")
	}
}

func TestAdministratorEditsDisplayNameAndEmail(t *testing.T) {
	e := usersEnv(t)
	c := e.signedIn("bob")
	carol := e.userID("carol")
	r := c.do("PUT", userPath(carol, ""), map[string]string{"display_name": "Carol D.", "email": "carol@example.com"})
	if r.Status != 200 {
		t.Fatalf("update: %d %s", r.Status, r.Raw)
	}
	r = c.do("GET", userPath(carol, ""), nil)
	if r.Body["display_name"] != "Carol D." || r.Body["email"] != "carol@example.com" || r.Body["username"] != "carol" {
		t.Fatalf("after update: %s", r.Raw)
	}
	for _, bad := range []map[string]string{
		{"display_name": "", "email": ""},
		{"display_name": "Carol", "email": "not an address"},
		{"display_name": "Car\nol", "email": ""},
	} {
		if r := c.do("PUT", userPath(carol, ""), bad); r.Status != 400 {
			t.Errorf("%q accepted: %d %s", bad, r.Status, r.Raw)
		}
	}
	// Editing one's own profile is allowed.
	if r := c.do("PUT", userPath(e.userID("bob"), ""), map[string]string{"display_name": "Bob", "email": ""}); r.Status != 200 {
		t.Fatalf("own profile: %d %s", r.Status, r.Raw)
	}
}

func TestUsernameCannotBeChanged(t *testing.T) {
	e := usersEnv(t)
	carol := e.userID("carol")
	r := e.signedIn("bob").do("PUT", userPath(carol, ""), map[string]string{"username": "eve", "display_name": "Carol", "email": ""})
	if r.Status != 400 {
		t.Fatalf("username field accepted: %d %s", r.Status, r.Raw)
	}
	if u, _ := e.store.GetUser(context.Background(), carol); u.Username != "carol" {
		t.Fatalf("username changed to %s", u.Username)
	}
}

// An Administrator acting on another Administrator or on the Owner is
// refused; the Owner may act on anyone but cannot disable or delete
// themselves.
func TestAdministratorIsRefusedActingOnAnotherAdministratorOrOwner(t *testing.T) {
	e := usersEnv(t)
	e.addUser("dave", store.GroupAdmins)
	bob := e.signedIn("bob")
	actions := []struct{ method, rest string }{
		{"PUT", ""}, {"POST", "/disable"}, {"POST", "/enable"}, {"DELETE", ""}, {"PUT", "/groups"},
	}
	body := map[string]any{"display_name": "X", "email": ""}
	for _, target := range []string{testUser, "dave"} {
		for _, a := range actions {
			b := any(body)
			if a.rest == "/groups" {
				b = map[string]any{"groups": []string{}}
			}
			r := bob.do(a.method, userPath(e.userID(target), a.rest), b)
			if r.Status != 403 || r.code() != "owner_only" {
				t.Errorf("bob %s %s on %s: %d %s", a.method, a.rest, target, r.Status, r.Raw)
			}
		}
	}
	if u, _ := e.store.GetUserByName(context.Background(), "dave"); u.Disabled || u.DisplayName != "dave" {
		t.Fatalf("dave changed: %+v", u)
	}

	owner := e.signedIn(testUser)
	for _, a := range []struct{ method, rest string }{{"POST", "/disable"}, {"DELETE", ""}} {
		r := owner.do(a.method, userPath(e.userID(testUser), a.rest), nil)
		if r.Status != 409 || r.code() != "self" {
			t.Errorf("Owner %s %s on themselves: %d %s", a.method, a.rest, r.Status, r.Raw)
		}
	}
	if r := owner.do("PUT", userPath(e.userID("dave"), ""), body); r.Status != 200 {
		t.Fatalf("Owner edits an Administrator: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("POST", userPath(e.userID("dave"), "/disable"), nil); r.Status != 200 {
		t.Fatalf("Owner disables an Administrator: %d %s", r.Status, r.Raw)
	}
	// An Administrator cannot disable or delete themselves either.
	if r := bob.do("POST", userPath(e.userID("bob"), "/disable"), nil); r.Status != 409 {
		t.Fatalf("bob disables himself: %d %s", r.Status, r.Raw)
	}
}

// signInDirect signs user in with password and authenticator code, without
// Authelia.
func (e *env) signInDirect(c *client, user, secret string) resp {
	e.t.Helper()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": testPassword})
	if r.Status != 200 {
		return r
	}
	return c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": e.unusedCode(user, secret)})
}

// autheliaUser is a User as Authelia's users file describes them.
type autheliaUser struct {
	DisplayName string   `yaml:"displayname"`
	Email       string   `yaml:"email"`
	Groups      []string `yaml:"groups"`
	Disabled    bool     `yaml:"disabled"`
}

// autheliaUsers reads the users file Authelia reads, once background applies
// have finished.
func (e *env) autheliaUsers() map[string]autheliaUser {
	e.t.Helper()
	e.srv.Wait()
	b, err := os.ReadFile(filepath.Join(e.srv.Config.AutheliaDir(), "users_database.yml"))
	if err != nil {
		e.t.Fatal(err)
	}
	var doc struct {
		Users map[string]autheliaUser `yaml:"users"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		e.t.Fatal(err)
	}
	return doc.Users
}

func TestDisablingEndsSessionsAndRefusesEverySignIn(t *testing.T) {
	e := usersEnv(t)
	carolSecret := e.addUser("erin", "family")
	carol := e.signedIn("erin")
	owner := e.signedIn(testUser)
	if r := owner.do("POST", userPath(e.userID("erin"), "/disable"), nil); r.Status != 200 || r.Body["state"] != "disabled" {
		t.Fatalf("disable: %d %s", r.Status, r.Raw)
	}
	// The open session ends at once, and Authelia's vouching no longer
	// opens a new one.
	if r := carol.do("GET", "/api/auth/session", nil); r.Status != 403 || r.code() != "account_disabled" {
		t.Fatalf("disabled User's session: %d %s", r.Status, r.Raw)
	}
	if c := e.autheliaClient("erin"); c.do("GET", "/api/hub", nil).Status != 403 || c.cookie != "" {
		t.Fatal("Authelia vouching opened a session for a Disabled User")
	}
	if u, found := e.autheliaUsers()["erin"]; !found || !u.Disabled {
		t.Fatalf("not disabled in Authelia: %+v", u)
	}

	// Direct sign-in, on the edge and on the local listener.
	e.setAutheliaProtection(false)
	c := e.edgeClient()
	if r := e.signInDirect(c, "erin", carolSecret); r.Status != 403 || r.code() != "account_disabled" || c.cookie != "" {
		t.Fatalf("direct sign-in of a Disabled User: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(true)
	bobSecret := e.addUser("frank", store.GroupAdmins)
	if r := e.signedIn(testUser).do("POST", userPath(e.userID("frank"), "/disable"), nil); r.Status != 200 {
		t.Fatalf("disable frank: %d %s", r.Status, r.Raw)
	}
	if r := e.signInDirect(e.localClient(), "frank", bobSecret); r.Status != 403 || r.code() != "account_disabled" {
		t.Fatalf("local sign-in of a Disabled Administrator: %d %s", r.Status, r.Raw)
	}

	// Enabling gives everything back: same password, same authenticator.
	if r := e.signedIn(testUser).do("POST", userPath(e.userID("erin"), "/enable"), nil); r.Status != 200 || r.Body["state"] != "active" {
		t.Fatalf("enable: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(false)
	if r := e.signInDirect(e.edgeClient(), "erin", carolSecret); r.Status != 200 {
		t.Fatalf("enabled User signs in: %d %s", r.Status, r.Raw)
	}
	if e.autheliaUsers()["erin"].Disabled {
		t.Fatal("still disabled in Authelia")
	}
	assertAudit(t, e, "user.disable erin true", "user.enable erin true")
}

func TestDeletingAUserNeedsStepUpAndRemovesEverything(t *testing.T) {
	e := usersEnv(t)
	carol := e.userID("carol")
	carolSession := e.signedIn("carol")
	e.autheliaKeys.handles = map[string]string{"auth.example.com/carol": "h"}

	// Without a recent identity check, nothing happens.
	stale := e.autheliaClient("bob")
	stale.do("GET", "/api/auth/session", nil)
	if r := stale.do("DELETE", userPath(carol, ""), nil); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("delete without step-up: %d %s", r.Status, r.Raw)
	}
	if r := e.signedIn("bob").do("DELETE", userPath(carol, ""), nil); r.Status != 200 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	if _, err := e.store.GetUser(context.Background(), carol); err == nil {
		t.Fatal("carol still exists")
	}
	if r := carolSession.do("GET", "/api/auth/session", nil); r.Status != 403 || r.code() != "no_account" {
		t.Fatalf("deleted User's session: %d %s", r.Status, r.Raw)
	}
	if _, found := e.autheliaUsers()["carol"]; found {
		t.Fatal("carol still in Authelia's users file")
	}
	if len(e.autheliaKeys.handles) != 0 {
		t.Fatalf("carol's Authelia passkeys kept: %v", e.autheliaKeys.handles)
	}
	// Created again with the same Username, the User starts in no Group.
	e.addUser("carol")
	if r := e.signedIn("bob").do("GET", userPath(e.userID("carol"), ""), nil); fmt.Sprint(r.Body["groups"]) != "[]" {
		t.Fatalf("new carol: %s", r.Raw)
	}
	assertAudit(t, e, "user.delete carol true")
}

func TestAdministratorSetsCustomGroupsOfAUser(t *testing.T) {
	e := usersEnv(t)
	ctx := context.Background()
	for _, g := range []string{"friends", "media"} {
		if err := e.store.CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	bob := e.signedIn("bob")
	carol := e.userID("carol")
	r := bob.do("PUT", userPath(carol, "/groups"), map[string]any{"groups": []string{"friends", "media"}})
	if r.Status != 200 || fmt.Sprint(r.Body["groups"]) != "[friends media]" {
		t.Fatalf("set groups: %d %s", r.Status, r.Raw)
	}
	if got := e.autheliaUsers()["carol"].Groups; fmt.Sprint(got) != "[friends media]" {
		t.Fatalf("Authelia groups: %v", got)
	}
	// Built-in Groups are the Owner's (ticket 09): asking for one here is
	// refused and changes nothing.
	r = bob.do("PUT", userPath(carol, "/groups"), map[string]any{"groups": []string{"friends", store.GroupAdmins}})
	if r.Status != 403 || r.code() != "builtin_group" {
		t.Fatalf("admins through the User page: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("PUT", userPath(carol, "/groups"), map[string]any{"groups": []string{"nope"}}); r.Status != 400 {
		t.Fatalf("unknown Group: %d %s", r.Status, r.Raw)
	}
	// Setting the custom Groups of the Owner keeps them in admins.
	owner := e.signedIn(testUser)
	if r := owner.do("PUT", userPath(e.userID(testUser), "/groups"), map[string]any{"groups": []string{"media"}}); fmt.Sprint(r.Body["groups"]) != "[admins media]" {
		t.Fatalf("Owner's groups: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PUT", userPath(e.userID(testUser), "/groups"), map[string]any{"groups": []string{}}); fmt.Sprint(r.Body["groups"]) != "[admins]" {
		t.Fatalf("Owner's groups emptied: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "user.groups carol true", "user.groups carol false")
}

// assertAudit checks that each "action target success" is in the audit log.
func assertAudit(t *testing.T, e *env, want ...string) {
	t.Helper()
	entries, err := e.store.ListAudit(context.Background(), 300)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, a := range entries {
		have[fmt.Sprintf("%s %s %v", a.Action, a.Target, a.Success)] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("audit log lacks %q", w)
		}
	}
}

func TestUnknownUserIs404(t *testing.T) {
	e := usersEnv(t)
	if r := e.signedIn("bob").do("GET", userPath(999, ""), nil); r.Status != 404 {
		t.Fatalf("unknown user: %d %s", r.Status, r.Raw)
	}
}
