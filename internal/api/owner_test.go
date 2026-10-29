package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/photonring/marineris-server/internal/store"
)

// groupsOfUser is the User's Groups as the Users page shows them.
func groupsOfUser(t *testing.T, c *client, id int64) string {
	t.Helper()
	r := c.do("GET", userPath(id, ""), nil)
	if r.Status != 200 {
		t.Fatalf("get user %d: %d %s", id, r.Status, r.Raw)
	}
	return fmt.Sprint(r.Body["groups"])
}

func handover(c *client, to int64) resp {
	return c.do("POST", "/api/owner", map[string]int64{"user_id": to})
}

func TestOnlyTheOwnerChangesBuiltinGroupsAndOwnership(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	carol := e.userID("carol")
	for _, call := range []struct{ method, path string }{
		{"PUT", memberPath(store.GroupAdmins, carol)},
		{"DELETE", memberPath(store.GroupAdmins, e.userID("bob"))},
		{"PUT", memberPath(store.GroupProxyAdmins, e.userID("bob"))},
		{"DELETE", memberPath(store.GroupProxyAdmins, e.userID("bob"))},
	} {
		if r := bob.do(call.method, call.path, nil); !refused(r) {
			t.Errorf("bob %s %s: %d %s", call.method, call.path, r.Status, r.Raw)
		}
	}
	if r := handover(bob, e.userID("bob")); !refused(r) {
		t.Errorf("bob takes ownership: %d %s", r.Status, r.Raw)
	}
	got := groupsOf(t, bob)
	if got[store.GroupAdmins] != "builtin=true members=admin,bob" || got[store.GroupProxyAdmins] != "builtin=true members=" {
		t.Fatalf("built-ins changed: %v", got)
	}
	// One audit entry per refusal, naming the route and the User.
	entries, err := e.store.ListAudit(context.Background(), 300)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range entries {
		if a.Action == "not_allowed" && a.Actor == "bob" && !a.Success {
			n++
			if !strings.HasPrefix(a.Target, "PUT /api/groups/") && !strings.HasPrefix(a.Target, "DELETE /api/groups/") && a.Target != "POST /api/owner" {
				t.Errorf("refusal target: %q", a.Target)
			}
		}
	}
	if n != 5 {
		t.Errorf("%d not_allowed audit entries, want 5", n)
	}
}

func TestOwnerChangesBuiltinGroupsWithStepUp(t *testing.T) {
	e := usersEnv(t)
	c := e.autheliaClient(testUser)
	c.do("GET", "/api/auth/session", nil)
	if r := c.do("PUT", memberPath(store.GroupAdmins, e.userID("carol")), nil); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("without step-up: %d %s", r.Status, r.Raw)
	}
	if r := handover(c, e.userID("bob")); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("handover without step-up: %d %s", r.Status, r.Raw)
	}
}

func TestProxyAdminsHoldsOnlyAdministrators(t *testing.T) {
	e := usersEnv(t)
	owner := e.signedIn(testUser)
	carol := e.userID("carol")
	if r := owner.do("PUT", memberPath(store.GroupProxyAdmins, carol), nil); r.Status != 409 || r.code() != "not_administrator" {
		t.Fatalf("carol into proxy-admins: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PUT", memberPath(store.GroupAdmins, carol), nil); r.Status != 200 {
		t.Fatalf("carol into admins: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("PUT", memberPath(store.GroupProxyAdmins, carol), nil); r.Status != 200 {
		t.Fatalf("carol into proxy-admins: %d %s", r.Status, r.Raw)
	}
	if got := groupsOfUser(t, owner, carol); got != "[admins family proxy-admins]" {
		t.Fatalf("carol: %s", got)
	}
	if got := e.autheliaUsers()["carol"].Groups; !slices.Equal(got, []string{"admins", "family", "proxy-admins"}) {
		t.Fatalf("authelia groups of carol: %v", got)
	}
	// Leaving admins leaves proxy-admins in the same change.
	if r := owner.do("DELETE", memberPath(store.GroupAdmins, carol), nil); r.Status != 200 {
		t.Fatalf("carol out of admins: %d %s", r.Status, r.Raw)
	}
	if got := groupsOfUser(t, owner, carol); got != "[family]" {
		t.Fatalf("carol: %s", got)
	}
	// Taking someone out of proxy-admins alone keeps them Administrator.
	bob := e.userID("bob")
	owner.do("PUT", memberPath(store.GroupProxyAdmins, bob), nil)
	if r := owner.do("DELETE", memberPath(store.GroupProxyAdmins, bob), nil); r.Status != 200 {
		t.Fatalf("bob out of proxy-admins: %d %s", r.Status, r.Raw)
	}
	if got := groupsOfUser(t, owner, bob); got != "[admins]" {
		t.Fatalf("bob: %s", got)
	}
	assertAudit(t, e,
		"group.member_add proxy-admins false", "group.member_add admins true",
		"group.member_add proxy-admins true", "group.member_remove admins true",
		"group.member_remove proxy-admins true")
}

func TestDisabledUserMayJoinBuiltinGroups(t *testing.T) {
	e := usersEnv(t)
	owner := e.signedIn(testUser)
	carol := e.userID("carol")
	owner.do("POST", userPath(carol, "/disable"), nil)
	for _, g := range []string{store.GroupAdmins, store.GroupProxyAdmins} {
		if r := owner.do("PUT", memberPath(g, carol), nil); r.Status != 200 {
			t.Fatalf("disabled carol into %s: %d %s", g, r.Status, r.Raw)
		}
	}
}

func TestOwnerStaysInAdminsAndAdminsNeverEmpties(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	owner := e.signedIn(testUser)
	me := e.userID(testUser)
	if r := owner.do("DELETE", memberPath(store.GroupAdmins, me), nil); r.Status != 409 || r.code() != "last_administrator" {
		t.Fatalf("last member leaves admins: %d %s", r.Status, r.Raw)
	}
	e.addUser("bob", store.GroupAdmins)
	if r := owner.do("DELETE", memberPath(store.GroupAdmins, me), nil); r.Status != 409 || r.code() != "owner_administrator" {
		t.Fatalf("Owner leaves admins: %d %s", r.Status, r.Raw)
	}
	if got := groupsOfUser(t, owner, me); got != "[admins]" {
		t.Fatalf("Owner: %s", got)
	}
	assertAudit(t, e, "group.member_remove admins false")
}

func TestOwnershipHandover(t *testing.T) {
	e := usersEnv(t)
	owner := e.signedIn(testUser)
	bob := e.signedIn("bob")
	e.addUser("dave", store.GroupAdmins)

	if r := handover(owner, e.userID("carol")); r.Status != 409 || r.code() != "not_administrator" {
		t.Fatalf("to a non-Administrator: %d %s", r.Status, r.Raw)
	}
	owner.do("POST", userPath(e.userID("dave"), "/disable"), nil)
	if r := handover(owner, e.userID("dave")); r.Status != 409 || r.code() != "user_disabled" {
		t.Fatalf("to a Disabled User: %d %s", r.Status, r.Raw)
	}
	if r := handover(owner, e.userID(testUser)); r.Status != 409 || r.code() != "self" {
		t.Fatalf("to themselves: %d %s", r.Status, r.Raw)
	}
	if r := handover(owner, 999); r.Status != 404 {
		t.Fatalf("to nobody: %d %s", r.Status, r.Raw)
	}
	if r := handover(owner, e.userID("bob")); r.Status != 200 {
		t.Fatalf("to bob: %d %s", r.Status, r.Raw)
	}

	if got, want := rightsOf(t, bob.do("GET", "/api/auth/session", nil)), "bob bob groups=[admins] admin=true proxy=false owner=true"; got != want {
		t.Errorf("bob: got %s want %s", got, want)
	}
	if got, want := rightsOf(t, owner.do("GET", "/api/auth/session", nil)), "admin admin groups=[admins] admin=true proxy=false owner=false"; got != want {
		t.Errorf("former Owner: got %s want %s", got, want)
	}
	// The former Owner is now an Administrator like the others.
	if r := owner.do("POST", userPath(e.userID("bob"), "/disable"), nil); r.Status != 403 || r.code() != "owner_only" {
		t.Errorf("former Owner disables the new one: %d %s", r.Status, r.Raw)
	}
	if r := handover(owner, e.userID(testUser)); !refused(r) {
		t.Errorf("former Owner hands over again: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "owner.transfer carol false", "owner.transfer bob true")
}

// Every User route acting on a User, and every custom Group member route,
// refuses another Administrator acting on the Owner. Driven from the route
// table, so a new route on a User is covered without editing this test.
func TestNobodyButTheOwnerActsOnTheOwner(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	owner := e.userID(testUser)
	n := 0
	for _, rt := range e.srv.Routes() {
		if rt.Who != Administrator || rt.Method == "GET" || !strings.Contains(rt.Pattern, "{id}") {
			continue
		}
		path := strings.Replace(rt.Pattern, "{id}", fmt.Sprint(owner), 1)
		switch {
		case strings.HasPrefix(rt.Pattern, "/api/users/{id}"):
		case strings.HasPrefix(rt.Pattern, "/api/groups/{name}/members/"):
			path = strings.Replace(path, "{name}", "family", 1)
		default:
			continue
		}
		body := `{"display_name":"X","email":"","groups":[]}`
		if rt.Method == "PUT" && rt.Pattern == "/api/users/{id}" {
			body = `{"display_name":"X","email":""}`
		} else if rt.Pattern == "/api/users/{id}/groups" {
			body = `{"groups":[]}`
		}
		n++
		if r := bob.do(rt.Method, path, body); r.Status != 403 || r.code() != "owner_only" {
			t.Errorf("%s %s on the Owner: %d %s", rt.Method, rt.Pattern, r.Status, r.Raw)
		}
	}
	if n < 7 {
		t.Fatalf("only %d routes tried", n)
	}
	u, err := e.store.GetUser(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if u.Disabled || u.DisplayName != testUser || !slices.Equal(u.Groups, []string{store.GroupAdmins}) {
		t.Fatalf("Owner changed: %+v", u)
	}
}
