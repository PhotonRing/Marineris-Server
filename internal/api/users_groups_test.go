package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/photonring/marineris-server/internal/store"
)

func rightsOf(t *testing.T, r resp) string {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("session: %d %s", r.Status, r.Raw)
	}
	rights, _ := r.Body["rights"].(map[string]any)
	u, _ := r.Body["user"].(map[string]any)
	return fmt.Sprintf("%v %v groups=%v admin=%v proxy=%v owner=%v",
		u["username"], u["display_name"], u["groups"], rights["administrator"], rights["proxy_admin"], rights["owner"])
}

func TestSetupMakesTheFirstUserOwnerAndAdministrator(t *testing.T) {
	e := newEnv(t)
	c, _ := e.completeSetup()
	r := c.do("GET", "/api/auth/session", nil)
	if got, want := rightsOf(t, r), "admin admin groups=[admins] admin=true proxy=false owner=true"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if _, ok := r.Body["user"].(map[string]any)["role"]; ok {
		t.Fatal("the session still reports a role")
	}
}

func TestSessionReportsRightsFromGroups(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	ctx := context.Background()
	add := func(name, display string, groups ...string) {
		u := &store.User{Username: name, DisplayName: display, PasswordHash: "x", WebAuthnID: []byte(name)}
		if err := e.store.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		for _, g := range groups {
			if err := e.store.AddGroupMember(ctx, u.ID, g); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.store.CreateGroup(ctx, "family"); err != nil {
		t.Fatal(err)
	}
	add("bob", "Bob", store.GroupAdmins, store.GroupProxyAdmins)
	add("carol", "", "family")
	add("dave", "Dave", store.GroupProxyAdmins) // not an Administrator: not a Proxy & SSO one either

	for user, want := range map[string]string{
		"bob":   "bob Bob groups=[admins proxy-admins] admin=true proxy=true owner=false",
		"carol": "carol carol groups=[family] admin=false proxy=false owner=false",
		"dave":  "dave Dave groups=[proxy-admins] admin=false proxy=false owner=false",
		testUser: "admin admin groups=[admins] admin=true proxy=false owner=true",
	} {
		if got := rightsOf(t, e.autheliaClient(user).do("GET", "/api/auth/session", nil)); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
}
