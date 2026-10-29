package api

import (
	"context"
	"database/sql"
	"testing"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
)

// addUser creates a User with testPassword and a fresh authenticator in the
// given Groups (custom ones are created on the fly) and returns its TOTP
// secret.
func (e *env) addUser(name string, groups ...string) string {
	e.t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(ctx, testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	secret := auth.NewTOTPSecret()
	handle := []byte("handle-" + name)
	sealed, err := e.srv.Box.Seal([]byte(secret), setup.TOTPContext(handle))
	if err != nil {
		e.t.Fatal(err)
	}
	u := &store.User{Username: name, PasswordHash: hash, TOTPSecret: sealed, WebAuthnID: handle}
	if err := e.store.CreateUser(ctx, u); err != nil {
		e.t.Fatal(err)
	}
	for _, g := range groups {
		if g != store.GroupAdmins && g != store.GroupProxyAdmins {
			_ = e.store.CreateGroup(ctx, g)
		}
		if err := e.store.AddGroupMember(ctx, u.ID, g); err != nil {
			e.t.Fatal(err)
		}
	}
	return secret
}

// Every route states who may call it: a route without a decision, or with
// one that does not fit its access level, stops the server from starting.
func TestEveryRouteStatesWhoMayCallIt(t *testing.T) {
	e := newEnv(t)
	if err := validateRoutes(e.srv.Routes()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Route{
		{Method: "GET", Pattern: "/api/new", Access: Authed},
		{Method: "GET", Pattern: "/api/new", Access: Authed, Who: Anyone},
		{Method: "GET", Pattern: "/api/new", Access: Public, Who: Administrator},
		{Method: "POST", Pattern: "/api/new", Access: Ticket, Who: AnyUser},
	} {
		if err := validateRoutes([]Route{bad}); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("a server with an unclassified route started")
		}
	}()
	e.srv.routes = append(e.srv.routes, Route{Method: "GET", Pattern: "/api/new", Access: Authed})
	mustValidRoutes(e.srv.routes)
}

func refused(r resp) bool { return r.Status == 403 && r.code() == "not_allowed" }

// callEveryRoute calls every session route as the Authelia-vouched user and
// reports, for each, whether it was refused for lack of rights. The session
// is reloaded before each call, so routes that end it (logout) or change the
// CSRF token do not affect the next ones.
func callEveryRoute(e *env, user string) map[string]bool {
	out := map[string]bool{}
	c := e.autheliaClient(user)
	for _, rt := range e.srv.Routes() {
		if rt.Access != Authed && rt.Access != StepUp {
			continue
		}
		if r := c.do("GET", "/api/auth/session", nil); r.Status != 200 {
			e.t.Fatalf("session of %s: %d %s", user, r.Status, r.Raw)
		}
		out[rt.Method+" "+rt.Pattern] = refused(c.do(rt.Method, concrete(rt.Pattern), "{}"))
	}
	return out
}

func TestNonAdministratorIsRefusedEverythingButTheirOwn(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	e.addUser("carol", "family")

	got := callEveryRoute(e, "carol")
	for _, rt := range e.srv.Routes() {
		key := rt.Method + " " + rt.Pattern
		was, called := got[key]
		if !called {
			continue
		}
		if want := rt.Who != AnyUser; was != want {
			t.Errorf("%s (%s): refused=%v, want %v", key, rt.Who, was, want)
		}
	}
	if len(got) == 0 {
		t.Fatal("no route called")
	}
}

func TestAdministratorIsRefusedOwnerAndProxyRoutes(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	e.addUser("bob", store.GroupAdmins)

	got := callEveryRoute(e, "bob")
	for _, rt := range e.srv.Routes() {
		key := rt.Method + " " + rt.Pattern
		was, called := got[key]
		if !called {
			continue
		}
		if want := rt.Who == ProxyAdmin || rt.Who == Owner; was != want {
			t.Errorf("%s (%s): refused=%v, want %v", key, rt.Who, was, want)
		}
	}
}

func TestOwnerAndProxyAdministratorRollBack(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)
	e.addUser("dave", store.GroupAdmins, store.GroupProxyAdmins)
	// Not an Administrator, so not a Proxy & SSO Administrator either.
	e.addUser("erin", store.GroupProxyAdmins)

	for user, want := range map[string]bool{testUser: false, "dave": false, "erin": true} {
		c := e.autheliaClient(user)
		c.do("GET", "/api/auth/session", nil)
		if r := c.do("POST", "/api/config/versions/1/rollback", "{}"); refused(r) != want {
			t.Errorf("%s: rollback %d %s, want refused=%v", user, r.Status, r.Raw, want)
		}
	}
}

func TestOnlyOwnerOrProxyAdministratorChangesUIProtection(t *testing.T) {
	e := newEnv(t)
	_, ownerSecret := e.completeSetup()
	e.setAutheliaProtection(true)
	clients := map[string]*client{}
	for user, secret := range map[string]string{
		"bob":    e.addUser("bob", store.GroupAdmins),
		"dave":   e.addUser("dave", store.GroupAdmins, store.GroupProxyAdmins),
		testUser: ownerSecret,
	} {
		// Settings need step-up, which an Authelia sign-in alone is not.
		c := e.autheliaClient(user)
		c.do("GET", "/api/auth/session", nil)
		code := e.freshCodeFor(secret)
		if user == testUser {
			code = e.freshCode(secret)
		}
		if r := c.do("POST", "/api/auth/reauth", map[string]string{"password": testPassword, "code": code}); r.Status != 200 {
			t.Fatalf("%s reauth: %d %s", user, r.Status, r.Raw)
		}
		clients[user] = c
	}
	// The UI host must belong to a domain for the settings to be saved.
	if r := clients[testUser].do("POST", "/api/domains", map[string]any{"name": "example.com"}); r.Status != 200 {
		t.Fatalf("domain: %d %s", r.Status, r.Raw)
	}

	put := func(user string, change func(map[string]any)) resp {
		t.Helper()
		c := clients[user]
		st := c.do("GET", "/api/settings", nil).Body
		delete(st, "setup_complete")
		change(st)
		return c.do("PUT", "/api/settings", st)
	}
	// Other settings, sent back with these two unchanged, still work.
	if r := put("bob", func(st map[string]any) { st["hub_title"] = "Home" }); r.Status != 200 {
		t.Fatalf("bob, other settings: %d %s", r.Status, r.Raw)
	}
	for _, change := range []func(map[string]any){
		func(st map[string]any) { st["auth_subdomain"] = "login" },
		func(st map[string]any) { st["ui_protect_with_authelia"] = false },
	} {
		if r := put("bob", change); !refused(r) {
			t.Errorf("bob changed how Authelia protects the UI: %d %s", r.Status, r.Raw)
		}
	}
	if st, _ := e.store.GetSettings(context.Background()); st.AuthSubdomain == "login" || !st.UIProtectWithAuthelia {
		t.Fatalf("refused change was stored: %+v", st)
	}
	if r := put("dave", func(st map[string]any) { st["auth_subdomain"] = "login" }); r.Status != 200 {
		t.Fatalf("proxy admin: %d %s", r.Status, r.Raw)
	}
	if r := put(testUser, func(st map[string]any) { st["auth_subdomain"] = "sso" }); r.Status != 200 {
		t.Fatalf("owner: %d %s", r.Status, r.Raw)
	}
}

func TestLocalListenerOnlyLetsAdministratorsSignIn(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	signIn := func(user, secret string) (resp, *client) {
		t.Helper()
		c := e.localClient()
		r := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": testPassword})
		if r.Status != 200 {
			t.Fatalf("%s password: %d %s", user, r.Status, r.Raw)
		}
		code, _ := totpNow(secret)
		return c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": code}), c
	}
	carolSecret := e.addUser("carol", "family")
	r, c := signIn("carol", carolSecret)
	if !refused(r) || c.cookie != "" {
		t.Fatalf("non-Administrator signed in locally: %d %s cookie=%q", r.Status, r.Raw, c.cookie)
	}
	if r := c.do("GET", "/api/auth/session", nil); r.Status != 401 {
		t.Fatalf("session after refusal: %d", r.Status)
	}
	if r, _ := signIn("bob", e.addUser("bob", store.GroupAdmins)); r.Status != 200 {
		t.Fatalf("Administrator refused locally: %d %s", r.Status, r.Raw)
	}
	// The same non-Administrator signs in on the edge.
	e.setAutheliaProtection(true)
	if r := e.autheliaClient("carol").do("GET", "/api/auth/session", nil); r.Status != 200 {
		t.Fatalf("carol on the edge: %d %s", r.Status, r.Raw)
	}
}

func TestLocalListenerEndsSessionOfDemotedAdministrator(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	secret := e.addUser("bob", store.GroupAdmins)
	c := e.localClient()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": testPassword})
	code, _ := totpNow(secret)
	if r = c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": code}); r.Status != 200 {
		t.Fatalf("bob: %d %s", r.Status, r.Raw)
	}
	// Leave admins behind the API's back: the Groups endpoints come later.
	db, err := sql.Open("sqlite", "file:"+e.srv.Config.DBPath()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("DELETE FROM group_members WHERE user_id = (SELECT id FROM users WHERE username = 'bob')"); err != nil {
		t.Fatal(err)
	}
	if r := c.do("GET", "/api/account/passkeys", nil); !refused(r) {
		t.Fatalf("demoted Administrator kept local access: %d %s", r.Status, r.Raw)
	}
}
