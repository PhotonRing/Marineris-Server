package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/store"
)

// autheliaClient is a browser behind Caddy and Authelia: Caddy sets
// Remote-User from Authelia's answer.
func (e *env) autheliaClient(user string) *client {
	c := e.edgeClient()
	if user != "" {
		c.header["Remote-User"] = user
	}
	return c
}

func TestAutheliaSignInOpensTheUI(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	e.setAutheliaProtection(true)

	// Signed in to Authelia: the UI opens without its own login.
	c := e.autheliaClient(testUser)
	r := c.do("GET", "/api/auth/session", nil)
	if r.Status != 200 || c.cookie == "" {
		t.Fatalf("session: %d %s", r.Status, r.Raw)
	}
	if r.Body["method"] != auth.MethodAuthelia || r.Body["step_up_ok"] != false {
		t.Fatalf("an Authelia session must still ask for a step-up: %v", r.Body)
	}
	csrf, cookie := r.Body["csrf_token"], c.cookie

	// The same session is reused, and works for changes (CSRF token).
	r = c.do("GET", "/api/auth/session", nil)
	if r.Status != 200 || r.Body["csrf_token"] != csrf || c.cookie != cookie {
		t.Fatalf("session not reused: %d %v", r.Status, r.Body)
	}
	c.csrf = csrf.(string)
	if r := c.do("POST", "/api/auth/logout", nil); r.Status != 200 {
		t.Fatalf("logout: %d %s", r.Status, r.Raw)
	}

	// Sensitive actions still need a confirmation.
	c = e.autheliaClient(testUser)
	r = c.do("GET", "/api/auth/session", nil)
	c.csrf = r.Body["csrf_token"].(string)
	if r := c.do("POST", "/api/account/passkeys/begin", nil); r.Status != http.StatusForbidden || r.Body["code"] != "reauth_required" {
		t.Fatalf("step-up not required: %d %s", r.Status, r.Raw)
	}
}

func TestAutheliaSignInIsTheOnlySignIn(t *testing.T) {
	e := newEnv(t)
	_, secret := e.completeSetup()
	c := e.edgeClient()
	if r := e.login(c, secret); r.Status != 200 {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(true)

	// Signed out of Authelia: a Marineris session alone opens nothing.
	if r := c.do("GET", "/api/auth/session", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("session without Authelia: %d", r.Status)
	}

	// Another user signs in to Authelia in the same browser: the session
	// becomes theirs.
	ctx := context.Background()
	bob := &store.User{Username: "bob", PasswordHash: "x", WebAuthnID: []byte("bob")}
	if err := e.store.CreateUser(ctx, bob); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddGroupMember(ctx, bob.ID, store.GroupAdmins); err != nil {
		t.Fatal(err)
	}
	c = e.edgeClient()
	e.login(c, secret)
	c.header["Remote-User"] = "bob"
	r := c.do("GET", "/api/auth/session", nil)
	if r.Status != 200 || r.Body["user"].(map[string]any)["username"] != "bob" {
		t.Fatalf("session of the previous user kept: %d %s", r.Status, r.Raw)
	}

	// Authelia knows a user Marineris does not.
	if r := e.autheliaClient("mallory").do("GET", "/api/auth/session", nil); r.Status != http.StatusForbidden || r.Body["code"] != "no_account" {
		t.Fatalf("unknown user: %d %s", r.Status, r.Raw)
	}
}

func TestRemoteUserIgnoredWithoutAuthelia(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	// Not protected by Authelia: the header proves nothing.
	if r := e.autheliaClient(testUser).do("GET", "/api/auth/session", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("edge trusted Remote-User without Authelia: %d", r.Status)
	}
	// Never on the break-glass listener.
	e.setAutheliaProtection(true)
	c := e.localClient()
	c.header["Remote-User"] = testUser
	if r := c.do("GET", "/api/auth/session", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("local listener trusted Remote-User: %d", r.Status)
	}
}

func TestMetaPortalURL(t *testing.T) {
	e := newEnv(t)
	e.completeSetup()
	if err := e.store.CreateDomain(context.Background(), &store.Domain{Name: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if r := e.edgeClient().do("GET", "/api/meta", nil); r.Body["portal_url"] != "" {
		t.Fatalf("portal without Authelia protection: %v", r.Body["portal_url"])
	}
	e.setAutheliaProtection(true)
	if r := e.edgeClient().do("GET", "/api/meta", nil); r.Body["portal_url"] != "https://auth.example.com" {
		t.Fatalf("portal: %v", r.Body["portal_url"])
	}
	if r := e.localClient().do("GET", "/api/meta", nil); r.Body["portal_url"] != "" {
		t.Fatalf("portal on the break-glass listener: %v", r.Body["portal_url"])
	}
}
