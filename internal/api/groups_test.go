package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/photonring/marineris-server/internal/store"
)

// groupsOf lists the Groups as the Groups page sees them: name, built-in
// flag and member Usernames.
func groupsOf(t *testing.T, c *client) map[string]string {
	t.Helper()
	r := c.do("GET", "/api/groups", nil)
	if r.Status != 200 {
		t.Fatalf("list groups: %d %s", r.Status, r.Raw)
	}
	var list []struct {
		Name    string `json:"name"`
		Builtin bool   `json:"builtin"`
		Members []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"members"`
	}
	if err := json.Unmarshal(r.Raw, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, g := range list {
		var names []string
		for _, m := range g.Members {
			names = append(names, m.Username)
		}
		out[g.Name] = fmt.Sprintf("builtin=%v members=%s", g.Builtin, strings.Join(names, ","))
	}
	return out
}

// field is the request field a 400 answer blames.
func (r resp) field() string {
	d, _ := r.Body["details"].(map[string]any)
	f, _ := d["field"].(string)
	return f
}

func memberPath(group string, id int64) string {
	return fmt.Sprintf("/api/groups/%s/members/%d", group, id)
}

func TestGroupsListShowsBuiltinsAndMembers(t *testing.T) {
	e := usersEnv(t)
	got := groupsOf(t, e.signedIn("bob"))
	want := map[string]string{
		store.GroupAdmins:      "builtin=true members=admin,bob",
		store.GroupProxyAdmins: "builtin=true members=",
		"family":               "builtin=false members=carol",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestCreateGroupValidatesItsName(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	if r := bob.do("POST", "/api/groups", `{"name":"friends_2-x"}`); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	if got := groupsOf(t, bob)["friends_2-x"]; got != "builtin=false members=" {
		t.Fatalf("new group: %q", got)
	}
	for _, bad := range []string{"", "a", "Friends", "with space", "dot.ted", "-lead", strings.Repeat("a", 33), "admins", "proxy-admins", "été"} {
		r := bob.do("POST", "/api/groups", map[string]string{"name": bad})
		if r.Status != 400 || r.field() != "name" {
			t.Errorf("%q: %d %s", bad, r.Status, r.Raw)
		}
	}
	if r := bob.do("POST", "/api/groups", `{"name":"family"}`); r.Status != 409 {
		t.Errorf("duplicate: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "group.create friends_2-x true")
}

func TestRenameGroup(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	bob.do("POST", "/api/groups", `{"name":"friends"}`)
	if r := bob.do("PUT", "/api/groups/family", `{"name":"relatives"}`); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Raw)
	}
	groups := groupsOf(t, bob)
	if _, still := groups["family"]; still || groups["relatives"] != "builtin=false members=carol" {
		t.Fatalf("after rename: %v", groups)
	}
	if got := e.autheliaUsers()["carol"].Groups; !slices.Equal(got, []string{"relatives"}) {
		t.Errorf("authelia groups of carol: %v", got)
	}
	if r := bob.do("PUT", "/api/groups/relatives", `{"name":"friends"}`); r.Status != 409 {
		t.Errorf("rename onto a taken name: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("PUT", "/api/groups/relatives", `{"name":"Bad Name"}`); r.Status != 400 || r.field() != "name" {
		t.Errorf("rename to an invalid name: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("PUT", "/api/groups/nope", `{"name":"other"}`); r.Status != 404 {
		t.Errorf("rename unknown: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "group.rename relatives true")
}

func TestDeleteGroupKeepsItsUsers(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	if r := bob.do("DELETE", "/api/groups/family", nil); r.Status != 200 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	if _, still := groupsOf(t, bob)["family"]; still {
		t.Fatal("group still listed")
	}
	if r := bob.do("GET", userPath(e.userID("carol"), ""), nil); r.Status != 200 {
		t.Fatalf("carol after delete: %d %s", r.Status, r.Raw)
	}
	carol, ok := e.autheliaUsers()["carol"]
	if !ok || len(carol.Groups) != 0 {
		t.Errorf("authelia carol: present=%v groups=%v", ok, carol.Groups)
	}
	if r := bob.do("DELETE", "/api/groups/family", nil); r.Status != 404 {
		t.Errorf("delete again: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "group.delete family true")
}

func TestDeleteGroupNeedsStepUp(t *testing.T) {
	e := usersEnv(t)
	c := e.autheliaClient("bob")
	c.do("GET", "/api/auth/session", nil)
	if r := c.do("DELETE", "/api/groups/family", nil); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("delete without step-up: %d %s", r.Status, r.Raw)
	}
}

func TestBuiltinGroupsCannotBeChangedFromTheGroupsPage(t *testing.T) {
	e := usersEnv(t)
	for _, who := range []string{testUser, "bob"} {
		c := e.signedIn(who)
		carol := e.userID("carol")
		for _, g := range []string{store.GroupAdmins, store.GroupProxyAdmins} {
			for _, call := range [][3]string{
				{"PUT", "/api/groups/" + g, `{"name":"renamed"}`},
				{"DELETE", "/api/groups/" + g, ""},
				// Membership has its own Owner routes; any other spelling
				// reaches the custom Group routes, which refuse it.
				{"PUT", memberPath(strings.ToUpper(g), carol), ""},
				{"DELETE", memberPath(strings.ToUpper(g), e.userID("bob")), ""},
			} {
				var body any
				if call[2] != "" {
					body = call[2]
				}
				if r := c.do(call[0], call[1], body); r.Status != 403 || r.code() != "builtin_group" {
					t.Errorf("%s %s %s: %d %s", who, call[0], call[1], r.Status, r.Raw)
				}
			}
		}
	}
	got := groupsOf(t, e.signedIn(testUser))
	if got[store.GroupAdmins] != "builtin=true members=admin,bob" || got[store.GroupProxyAdmins] != "builtin=true members=" {
		t.Fatalf("built-ins changed: %v", got)
	}
}

func TestMembersReachAutheliaAtOnce(t *testing.T) {
	e := usersEnv(t)
	bob := e.signedIn("bob")
	e.addUser("dave")
	dave := e.userID("dave")
	if r := bob.do("PUT", memberPath("family", dave), nil); r.Status != 200 {
		t.Fatalf("add: %d %s", r.Status, r.Raw)
	}
	// Adding again changes nothing.
	if r := bob.do("PUT", memberPath("family", dave), nil); r.Status != 200 {
		t.Fatalf("add again: %d %s", r.Status, r.Raw)
	}
	if got := groupsOf(t, bob)["family"]; got != "builtin=false members=carol,dave" {
		t.Fatalf("family: %q", got)
	}
	if got := e.autheliaUsers()["dave"].Groups; !slices.Equal(got, []string{"family"}) {
		t.Fatalf("authelia groups of dave: %v", got)
	}
	if r := bob.do("DELETE", memberPath("family", e.userID("carol")), nil); r.Status != 200 {
		t.Fatalf("remove: %d %s", r.Status, r.Raw)
	}
	if got := e.autheliaUsers()["carol"].Groups; len(got) != 0 {
		t.Fatalf("authelia groups of carol: %v", got)
	}
	for _, path := range []string{memberPath("nope", dave), memberPath("family", 999)} {
		if r := bob.do("PUT", path, nil); r.Status != 404 {
			t.Errorf("PUT %s: %d %s", path, r.Status, r.Raw)
		}
	}
	assertAudit(t, e, "group.member_add family true", "group.member_remove family true")
}

// Renaming or deleting a Group changes the Groups of its members: an
// Administrator who is not the Owner may do it only when no other
// Administrator, and not the Owner, is in it.
func TestAdministratorCannotRenameOrDeleteAGroupOfAnotherAdministrator(t *testing.T) {
	e := usersEnv(t)
	e.addUser("erin", store.GroupAdmins, "family")
	bob := e.signedIn("bob")
	if r := bob.do("PUT", "/api/groups/family", `{"name":"relatives"}`); r.Status != 403 || r.code() != "owner_only" {
		t.Errorf("rename with erin in it: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("DELETE", "/api/groups/family", nil); r.Status != 403 || r.code() != "owner_only" {
		t.Errorf("delete with erin in it: %d %s", r.Status, r.Raw)
	}
	bob.do("POST", "/api/groups", `{"name":"owners"}`)
	if r := e.signedIn(testUser).do("PUT", memberPath("owners", e.userID(testUser)), nil); r.Status != 200 {
		t.Fatalf("owner joins: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("DELETE", "/api/groups/owners", nil); r.Status != 403 || r.code() != "owner_only" {
		t.Errorf("delete with the Owner in it: %d %s", r.Status, r.Raw)
	}
	if _, still := groupsOf(t, bob)["family"]; !still {
		t.Fatal("refused rename or delete happened")
	}
	assertAudit(t, e, "group.rename family false", "group.delete family false", "group.delete owners false")

	// Alone among non-Administrators, bob may; the Owner always may.
	bob.do("POST", "/api/groups", `{"name":"friends"}`)
	bob.do("PUT", memberPath("friends", e.userID("bob")), nil)
	bob.do("PUT", memberPath("friends", e.userID("carol")), nil)
	if r := bob.do("PUT", "/api/groups/friends", `{"name":"pals"}`); r.Status != 200 {
		t.Errorf("rename with bob and carol in it: %d %s", r.Status, r.Raw)
	}
	if r := bob.do("DELETE", "/api/groups/pals", nil); r.Status != 200 {
		t.Errorf("delete with bob and carol in it: %d %s", r.Status, r.Raw)
	}
	owner := e.signedIn(testUser)
	if r := owner.do("PUT", "/api/groups/family", `{"name":"relatives"}`); r.Status != 200 {
		t.Errorf("Owner renames: %d %s", r.Status, r.Raw)
	}
	if r := owner.do("DELETE", "/api/groups/relatives", nil); r.Status != 200 {
		t.Errorf("Owner deletes: %d %s", r.Status, r.Raw)
	}
}

func TestAdministratorCannotChangeAnotherAdministratorsGroups(t *testing.T) {
	e := usersEnv(t)
	e.addUser("erin", store.GroupAdmins)
	bob := e.signedIn("bob")
	owner := e.userID(testUser)
	for _, target := range []int64{e.userID("erin"), owner} {
		if r := bob.do("PUT", memberPath("family", target), nil); r.Status != 403 || r.code() != "owner_only" {
			t.Errorf("add %d: %d %s", target, r.Status, r.Raw)
		}
	}
	// Themselves, and the Owner acting on anyone, are fine.
	if r := bob.do("PUT", memberPath("family", e.userID("bob")), nil); r.Status != 200 {
		t.Errorf("bob adds himself: %d %s", r.Status, r.Raw)
	}
	if r := e.signedIn(testUser).do("PUT", memberPath("family", e.userID("erin")), nil); r.Status != 200 {
		t.Errorf("owner adds erin: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "group.member_add family false")
}
