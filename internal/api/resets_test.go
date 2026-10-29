package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/store"
)

// newPassword: what resetting Users choose in these tests.
const newPassword = "Noctis-Labyrinthus-2026!"

// resetLink creates a Reset link for the User id as c and returns the
// response and the link's token.
func resetLink(t *testing.T, c *client, id int64, resets string) (resp, string) {
	t.Helper()
	r := c.do("POST", userPath(id, "/reset"), map[string]string{"resets": resets})
	url, _ := r.Body["url"].(string)
	if r.Status == 200 && !strings.HasPrefix(url, linkPrefix) {
		t.Fatalf("link address: %q", url)
	}
	return r, strings.TrimPrefix(url, linkPrefix)
}

// A pending Reset link signs the User out everywhere and blocks every
// sign-in until it is used or revoked.
func TestResetLinkBlocksSignInAndEndsSessions(t *testing.T) {
	e := inviteEnv(t)
	secret := e.addUser("erin", "family")
	viaAuthelia := e.signedIn("erin")
	e.setAutheliaProtection(false)
	direct := e.edgeClient()
	if r := e.signInDirect(direct, "erin", secret); r.Status != 200 {
		t.Fatalf("direct sign-in: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(true)
	bob := e.signedIn("bob")

	r, token := resetLink(t, bob, e.userID("erin"), "password")
	if r.Status != 200 || r.Body["kind"] != "reset" || r.Body["resets"] != "password" || r.Body["username"] != "erin" || len(token) != 43 {
		t.Fatalf("reset link: %d %s", r.Status, r.Raw)
	}

	// Her Marineris sessions are gone, and Authelia's vouching no longer
	// opens one.
	e.setAutheliaProtection(false)
	if r := direct.do("GET", "/api/auth/session", nil); r.Status != 401 {
		t.Fatalf("open session: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(true)
	if r := viaAuthelia.do("GET", "/api/auth/session", nil); r.Status != 403 || r.code() != "reset_pending" {
		t.Fatalf("session through Authelia: %d %s", r.Status, r.Raw)
	}
	if c := e.autheliaClient("erin"); c.do("GET", "/api/hub", nil).Status != 403 || c.cookie != "" {
		t.Fatal("Authelia vouching opened a session for a User with a pending reset")
	}
	if u := e.autheliaUsers()["erin"]; !u.Disabled {
		t.Fatalf("not disabled in Authelia: %+v", u)
	}
	if r := bob.do("GET", userPath(e.userID("erin"), ""), nil); r.Body["state"] != "reset pending" {
		t.Fatalf("state: %s", r.Raw)
	}
	e.setAutheliaProtection(false)
	if r := e.signInDirect(e.edgeClient(), "erin", secret); r.Status != 403 || r.code() != "reset_pending" {
		t.Fatalf("direct sign-in: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "reset.create erin true")
}

// useReset goes through the Reset link page, setting a password when
// password is not empty and an authenticator when totp is true; it returns
// the finish response and the new authenticator's secret.
func (e *env) useReset(token, password string, totp bool) (resp, string) {
	e.t.Helper()
	page := e.linkPage(token)
	body := map[string]string{}
	secret := ""
	if totp {
		r := page.do("POST", "/marineris-link/api/totp", nil)
		if r.Status != 200 {
			return r, ""
		}
		secret = r.Body["totp_secret"].(string)
		body["totp_code"], _ = totpNow(secret)
	}
	if password != "" {
		body["password"] = password
	}
	return page.do("POST", "/marineris-link/api/finish", body), secret
}

// resetUser: erin, in family, with a Marineris passkey, an Authelia passkey
// and recovery codes; returns her authenticator secret and a recovery code.
func resetUser(t *testing.T, e *env) (secret, recovery string) {
	t.Helper()
	ctx := context.Background()
	secret = e.addUser("erin", "family")
	id := e.userID("erin")
	if err := e.store.AddWebAuthnCredential(ctx, &store.WebAuthnCredential{ID: []byte("erin-key"), UserID: id, Name: "phone", Data: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := e.autheliaKeys.AddAutheliaPasskey(ctx, configmgr.NewAutheliaPasskey{RPID: "auth.example.com", Username: "erin", Name: "phone", UserHandle: "h",
		Credential: &webauthn.Credential{ID: []byte("erin-authelia-key")}}); err != nil {
		t.Fatal(err)
	}
	codes, hashes := auth.NewRecoveryCodes()
	if err := e.store.SetRecoveryCodes(ctx, id, hashes); err != nil {
		t.Fatal(err)
	}
	return secret, codes[0]
}

// passkeysOf counts the Marineris passkeys a signed-in client sees, and
// the Authelia ones.
func passkeysOf(t *testing.T, e *env, c *client) (marineris, authelia int) {
	t.Helper()
	r := c.do("GET", "/api/account/passkeys", nil)
	if r.Status != 200 {
		t.Fatalf("passkeys: %d %s", r.Status, r.Raw)
	}
	keys, err := e.autheliaKeys.AutheliaPasskeys(context.Background(), "erin")
	if err != nil {
		t.Fatal(err)
	}
	return len(r.array()), len(keys)
}

func TestPasswordResetKeepsTheSecondFactor(t *testing.T) {
	e := inviteEnv(t)
	secret, _ := resetUser(t, e)
	_, token := resetLink(t, e.signedIn("bob"), e.userID("erin"), "password")

	r := e.linkPage(token).do("POST", "/marineris-link/api/read", nil)
	if r.Status != 200 || r.Body["kind"] != "reset" || r.Body["username"] != "erin" || strings.Join(stringsOf(r.Body["sets"]), ",") != "password" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	// A password reset needs no authenticator, and refuses a weak password.
	if r, _ := e.useReset(token, "short", false); r.Status != 400 {
		t.Fatalf("weak password: %d %s", r.Status, r.Raw)
	}
	if r, _ := e.useReset(token, newPassword, false); r.Status != 200 || r.Body["redirect_url"] == nil {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}
	// Used once.
	if r, _ := e.useReset(token, newPassword, false); r.Status != 404 || r.code() != "link_invalid" {
		t.Fatalf("second use: %d %s", r.Status, r.Raw)
	}

	if e.autheliaUsers()["erin"].Disabled {
		t.Fatal("still disabled in Authelia")
	}
	e.setAutheliaProtection(false)
	if r := e.signInDirect(e.edgeClient(), "erin", secret); r.Status != 401 {
		t.Fatalf("old password: %d %s", r.Status, r.Raw)
	}
	c := e.edgeClient()
	if r := e.signInDirectAs(c, "erin", newPassword, secret); r.Status != 200 {
		t.Fatalf("new password, same authenticator: %d %s", r.Status, r.Raw)
	}
	if m, a := passkeysOf(t, e, c); m != 1 || a != 1 {
		t.Fatalf("passkeys after a password reset: %d Marineris, %d Authelia", m, a)
	}
	assertAudit(t, e, "reset.use erin true")
}

func TestSecondFactorResetRemovesTheOldOnes(t *testing.T) {
	e := inviteEnv(t)
	oldSecret, recovery := resetUser(t, e)
	_, token := resetLink(t, e.signedIn("bob"), e.userID("erin"), "second_factor")

	r := e.linkPage(token).do("POST", "/marineris-link/api/read", nil)
	if r.Status != 200 || strings.Join(stringsOf(r.Body["sets"]), ",") != "second_factor" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	// A wrong code changes nothing, and the link still works.
	page := e.linkPage(token)
	page.do("POST", "/marineris-link/api/totp", nil)
	if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"totp_code": "abcdef"}); r.Status != 400 {
		t.Fatalf("wrong code: %d %s", r.Status, r.Raw)
	}
	r, secret := e.useReset(token, "", true)
	if r.Status != 200 {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}

	e.setAutheliaProtection(false)
	// Same password; the old authenticator and recovery codes no longer work.
	if r := e.signInDirect(e.edgeClient(), "erin", oldSecret); r.Status != 401 || r.code() != "invalid_code" {
		t.Fatalf("old authenticator: %d %s", r.Status, r.Raw)
	}
	c := e.edgeClient()
	r = c.do("POST", "/api/auth/login", map[string]string{"username": "erin", "password": testPassword})
	if r := c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "recovery_code": recovery}); r.Status != 401 {
		t.Fatalf("old recovery code: %d %s", r.Status, r.Raw)
	}
	c = e.edgeClient()
	if r := e.signInDirect(c, "erin", secret); r.Status != 200 {
		t.Fatalf("new authenticator: %d %s", r.Status, r.Raw)
	}
	if m, a := passkeysOf(t, e, c); m != 0 || a != 0 {
		t.Fatalf("passkeys after a second-factor reset: %d Marineris, %d Authelia", m, a)
	}
	assertAudit(t, e, "reset.use erin false", "reset.use erin true")
}

func TestResettingBoth(t *testing.T) {
	e := inviteEnv(t)
	oldSecret, _ := resetUser(t, e)
	_, token := resetLink(t, e.signedIn("bob"), e.userID("erin"), "both")

	if r := e.linkPage(token).do("POST", "/marineris-link/api/read", nil); strings.Join(stringsOf(r.Body["sets"]), ",") != "password,second_factor" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	// Both are needed.
	if r, _ := e.useReset(token, "", true); r.Status != 400 {
		t.Fatalf("without a password: %d %s", r.Status, r.Raw)
	}
	if r, _ := e.useReset(token, newPassword, false); r.Status != 400 {
		t.Fatalf("without an authenticator: %d %s", r.Status, r.Raw)
	}
	r, secret := e.useReset(token, newPassword, true)
	if r.Status != 200 {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}
	e.setAutheliaProtection(false)
	if r := e.signInDirectAs(e.edgeClient(), "erin", newPassword, oldSecret); r.Status != 401 {
		t.Fatalf("old authenticator: %d %s", r.Status, r.Raw)
	}
	if r := e.signInDirectAs(e.edgeClient(), "erin", newPassword, secret); r.Status != 200 {
		t.Fatalf("new credentials: %d %s", r.Status, r.Raw)
	}
}

func TestResetLinkCreateRules(t *testing.T) {
	e := inviteEnv(t)
	e.addUser("frank", store.GroupAdmins)
	bob := e.signedIn("bob")
	owner := e.signedIn(testUser)
	carol := e.userID("carol")

	// Step-up is needed.
	stale := e.autheliaClient("bob")
	stale.do("GET", "/api/auth/session", nil)
	if r, _ := resetLink(t, stale, carol, "both"); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("without step-up: %d %s", r.Status, r.Raw)
	}
	for _, bad := range []string{"", "totp", "everything"} {
		if r, _ := resetLink(t, bob, carol, bad); r.Status != 400 {
			t.Fatalf("resets %q: %d %s", bad, r.Status, r.Raw)
		}
	}
	// Nobody resets themselves, the Owner included.
	if r, _ := resetLink(t, bob, e.userID("bob"), "password"); r.Status != 409 || r.code() != "self" {
		t.Fatalf("bob resets himself: %d %s", r.Status, r.Raw)
	}
	if r, _ := resetLink(t, owner, e.userID(testUser), "password"); r.Status != 409 || r.code() != "self" {
		t.Fatalf("the Owner resets themselves: %d %s", r.Status, r.Raw)
	}
	// An Administrator does not reset another Administrator, nor the Owner.
	for _, name := range []string{"frank", testUser} {
		if r, _ := resetLink(t, bob, e.userID(name), "password"); r.Status != 403 || r.code() != "owner_only" {
			t.Fatalf("bob resets %s: %d %s", name, r.Status, r.Raw)
		}
	}
	// The Owner resets any Administrator...
	r, _ := resetLink(t, owner, e.userID("frank"), "second_factor")
	if r.Status != 200 {
		t.Fatalf("the Owner resets frank: %d %s", r.Status, r.Raw)
	}
	// ... and bob may not undo it.
	if r := bob.do("DELETE", "/api/links/"+jsonID(r.Body["id"]), nil); r.Status != 403 || r.code() != "owner_only" {
		t.Fatalf("bob revokes frank's reset: %d %s", r.Status, r.Raw)
	}
	// A new link replaces the pending one.
	_, first := resetLink(t, bob, carol, "password")
	if r, _ := resetLink(t, bob, carol, "both"); r.Status != 200 {
		t.Fatalf("second link: %d %s", r.Status, r.Raw)
	}
	if r := e.linkPage(first).do("POST", "/marineris-link/api/read", nil); r.Status != 404 || r.code() != "link_invalid" {
		t.Fatalf("replaced link: %d %s", r.Status, r.Raw)
	}
	if v := bob.do("GET", userPath(carol, ""), nil); v.Body["reset"].(map[string]any)["resets"] != "both" {
		t.Fatalf("pending reset: %s", v.Raw)
	}
	assertAudit(t, e, "reset.create carol true", "reset.create frank true", "reset.create frank false", "reset.revoke frank false")
}

// Revoking the link lets the User in again with their old credentials, also
// once it expired: an expired link keeps blocking the User.
func TestRevokingAResetLinkRestoresOldCredentials(t *testing.T) {
	e := inviteEnv(t)
	secret := e.addUser("erin", "family")
	bob := e.signedIn("bob")
	r, token := resetLink(t, bob, e.userID("erin"), "both")

	e.store.SetClock(func() time.Time { return time.Now().UTC().Add(store.LinkTTL + time.Minute) })
	defer e.store.SetClock(func() time.Time { return time.Now().UTC() })
	if r := e.linkPage(token).do("POST", "/marineris-link/api/read", nil); r.Status != 404 || r.code() != "link_invalid" {
		t.Fatalf("expired link: %d %s", r.Status, r.Raw)
	}
	if v := bob.do("GET", userPath(e.userID("erin"), ""), nil); v.Body["state"] != "reset pending" || v.Body["reset"].(map[string]any)["expired"] != true {
		t.Fatalf("expired reset: %s", v.Raw)
	}
	links := bob.do("GET", "/api/links", nil).array()
	if len(links) != 1 || links[0]["kind"] != "reset" || links[0]["expired"] != true {
		t.Fatalf("links: %v", links)
	}

	if r := bob.do("DELETE", "/api/links/"+jsonID(r.Body["id"]), nil); r.Status != 200 {
		t.Fatalf("revoke: %d %s", r.Status, r.Raw)
	}
	if e.autheliaUsers()["erin"].Disabled {
		t.Fatal("still disabled in Authelia")
	}
	if c := e.autheliaClient("erin"); c.do("GET", "/api/hub", nil).Status != 200 {
		t.Fatal("Authelia's vouching refused after revoking")
	}
	e.setAutheliaProtection(false)
	if r := e.signInDirect(e.edgeClient(), "erin", secret); r.Status != 200 {
		t.Fatalf("old credentials: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "reset.revoke erin true")
}
