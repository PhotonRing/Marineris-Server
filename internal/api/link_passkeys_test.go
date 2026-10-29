package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const portalOrigin = "https://auth.example.com"

// beginPasskey starts creating a passkey on the link page and returns the
// creation options.
func beginPasskey(t *testing.T, page *client) map[string]any {
	t.Helper()
	r := page.do("POST", "/marineris-link/api/passkey", nil)
	if r.Status != 200 {
		t.Fatalf("passkey: %d %s", r.Status, r.Raw)
	}
	return r.Body["options"].(map[string]any)
}

// finishWithPasskey goes through the link page with a new passkey, made by
// a, and the password when not empty.
func (e *env) finishWithPasskey(token, password, name string, a *authenticator) resp {
	e.t.Helper()
	page := e.linkPage(token)
	body := map[string]any{"passkey": a.create(e.t, beginPasskey(e.t, page), portalOrigin)}
	if password != "" {
		body["password"] = password
	}
	if name != "" {
		body["passkey_name"] = name
	}
	return page.do("POST", "/marineris-link/api/finish", body)
}

func TestInviteAcceptedWithAPasskey(t *testing.T) {
	e := inviteEnv(t)
	_, token := invite(t, e.signedIn("bob"), map[string]any{"username": "dave", "display_name": "Dave Doe", "groups": []string{"family"}})

	page := e.linkPage(token)
	if r := page.do("GET", "/marineris-link/webauthn.js", nil); r.Status != 200 || !strings.Contains(string(r.Raw), "creationOptions") {
		t.Fatalf("webauthn.js: %d", r.Status)
	}
	r := page.do("POST", "/marineris-link/api/read", nil)
	if r.Status != 200 || strings.Join(stringsOf(r.Body["second_factors"]), ",") != "totp,passkey" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	// The passkey belongs to the portal's host, so it signs in everywhere.
	opts := beginPasskey(t, page)
	pk := opts["publicKey"].(map[string]any)
	if pk["rp"].(map[string]any)["id"] != "auth.example.com" {
		t.Fatalf("relying party: %v", pk["rp"])
	}
	a := newAuthenticator(t)
	cred := a.create(t, opts, portalOrigin)
	if _, err := e.store.GetUserByName(context.Background(), "dave"); err == nil {
		t.Fatal("dave exists before the passkey is set")
	}
	r = page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword, "passkey": cred, "passkey_name": "Laptop"})
	if r.Status != 200 || r.Body["redirect_url"] != "https://auth.example.com/?rd=https%3A%2F%2Fmarineris.example.com%2F" {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}

	added := e.autheliaKeys.added
	if len(added) != 1 || added[0].RPID != "auth.example.com" || added[0].Username != "dave" || added[0].Name != "Laptop" ||
		added[0].UserHandle != string(a.user) || string(added[0].Credential.ID) != string(a.id) || !added[0].Discoverable {
		t.Fatalf("registered in Authelia: %+v", added)
	}
	if u, found := e.autheliaUsers()["dave"]; !found || strings.Join(u.Groups, ",") != "family" || u.Disabled {
		t.Fatalf("authelia users: %+v", e.autheliaUsers())
	}
	if got := rightsOf(t, e.autheliaClient("dave").do("GET", "/api/auth/session", nil)); got != "dave Dave Doe groups=[family] admin=false proxy=false owner=false" {
		t.Fatalf("session: %s", got)
	}
	if r := page.do("POST", "/marineris-link/api/read", nil); r.Status != http.StatusNotFound {
		t.Fatalf("link used twice: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "invite.accept dave true", "passkey.register Laptop true")
}

// Until a passkey is verified and registered in Authelia, nothing is
// created and the link still works.
func TestInvitePasskeyRefusals(t *testing.T) {
	e := inviteEnv(t)
	_, token := invite(t, e.signedIn("bob"), map[string]any{"username": "dave"})
	page := e.linkPage(token)
	a := newAuthenticator(t)
	stillPending := func(what string) {
		t.Helper()
		if _, err := e.store.GetUserByName(context.Background(), "dave"); err == nil {
			t.Fatalf("%s: dave exists", what)
		}
		if r := page.do("POST", "/marineris-link/api/read", nil); r.Status != 200 {
			t.Fatalf("%s: link no longer valid: %d %s", what, r.Status, r.Raw)
		}
	}

	// A passkey made for another ceremony (none begun yet).
	r := page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword,
		"passkey": a.create(t, map[string]any{"publicKey": map[string]any{"rp": map[string]any{"id": "auth.example.com"}, "user": map[string]any{"id": ""}, "challenge": "x"}}, portalOrigin)})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("passkey without ceremony: %d %s", r.Status, r.Raw)
	}
	stillPending("no ceremony")

	// Made for another site.
	cred := a.create(t, beginPasskey(t, page), "https://evil.example")
	if r := page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword, "passkey": cred}); r.Status != http.StatusBadRequest || r.code() != "invalid_passkey" {
		t.Fatalf("wrong origin: %d %s", r.Status, r.Raw)
	}
	stillPending("wrong origin")

	// Neither, or both, second factors.
	if r := page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword}); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Raw), "second_factor") {
		t.Fatalf("no second factor: %d %s", r.Status, r.Raw)
	}
	cred = a.create(t, beginPasskey(t, page), portalOrigin)
	if r := page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword, "passkey": cred, "totp_code": "123456"}); r.Status != http.StatusBadRequest || !strings.Contains(string(r.Raw), "second_factor") {
		t.Fatalf("both second factors: %d %s", r.Status, r.Raw)
	}
	// A name too long.
	if r := page.do("POST", "/marineris-link/api/finish", map[string]any{"password": invitedPassword, "passkey": cred, "passkey_name": strings.Repeat("n", 65)}); r.Status != http.StatusBadRequest {
		t.Fatalf("long name: %d %s", r.Status, r.Raw)
	}
	stillPending("bad requests")

	// Authelia's import failing.
	e.autheliaKeys.failAdd = errors.New("authelia is down")
	if r := e.finishWithPasskey(token, invitedPassword, "", a); r.Status < 500 {
		t.Fatalf("import failing: %d %s", r.Status, r.Raw)
	}
	stillPending("import failing")
	e.autheliaKeys.failAdd = nil

	// Then it works, with the default name.
	if r := e.finishWithPasskey(token, invitedPassword, "", newAuthenticator(t)); r.Status != 200 {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}
	if added := e.autheliaKeys.added; len(added) != 1 || added[0].Name != "Passkey" {
		t.Fatalf("registered in Authelia: %+v", added)
	}
	assertAudit(t, e, "invite.accept dave false", "invite.accept dave true")
}

// A passkey of the portal cannot sign in to a UI that Authelia does not
// protect: the page then offers the authenticator only.
func TestNoPasskeyWhenTheUIIsNotBehindAuthelia(t *testing.T) {
	e := inviteEnv(t)
	_, token := invite(t, e.signedIn("bob"), map[string]any{"username": "dave"})
	e.setAutheliaProtection(false)
	page := e.linkPage(token)
	if r := page.do("POST", "/marineris-link/api/read", nil); r.Status != 200 || strings.Join(stringsOf(r.Body["second_factors"]), ",") != "totp" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	if r := page.do("POST", "/marineris-link/api/passkey", nil); r.Status != http.StatusConflict {
		t.Fatalf("passkey: %d %s", r.Status, r.Raw)
	}
}

// A second-factor Reset link can set a passkey instead of an authenticator:
// the old authenticator and passkeys are gone, the new passkey signs in.
func TestSecondFactorResetWithAPasskey(t *testing.T) {
	for _, resets := range []string{"second_factor", "both"} {
		t.Run(resets, func(t *testing.T) {
			e := inviteEnv(t)
			oldSecret, _ := resetUser(t, e)
			_, token := resetLink(t, e.signedIn("bob"), e.userID("erin"), resets)
			password := ""
			if resets == "both" {
				password = newPassword
			}
			a := newAuthenticator(t)
			if r := e.finishWithPasskey(token, password, "Phone", a); r.Status != 200 {
				t.Fatalf("finish: %d %s", r.Status, r.Raw)
			}

			keys, err := e.autheliaKeys.AutheliaPasskeys(context.Background(), "erin")
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 1 || keys[0].Name != "Phone" || keys[0].Host != "auth.example.com" {
				t.Fatalf("Authelia passkeys: %+v", keys)
			}
			if u := e.autheliaUsers()["erin"]; u.Disabled {
				t.Fatalf("still disabled in Authelia: %+v", u)
			}
			c := e.autheliaClient("erin")
			if r := c.do("GET", "/api/auth/session", nil); r.Status != 200 {
				t.Fatalf("session through Authelia: %d %s", r.Status, r.Raw)
			}
			if m, _ := passkeysOf(t, e, c); m != 0 {
				t.Fatalf("Marineris passkeys after the reset: %d", m)
			}
			// The old authenticator no longer works.
			e.setAutheliaProtection(false)
			pw := testPassword
			if password != "" {
				pw = password
			}
			r := e.edgeClient().do("POST", "/api/auth/login", map[string]string{"username": "erin", "password": pw})
			if r.Status != 200 {
				t.Fatalf("password: %d %s", r.Status, r.Raw)
			}
			code, _ := totpNow(oldSecret)
			if r := e.edgeClient().do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": code}); r.Status != 401 {
				t.Fatalf("old authenticator: %d %s", r.Status, r.Raw)
			}
			assertAudit(t, e, "reset.use erin true", "passkey.register Phone true")
		})
	}
}
