package api

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/store"
)

const (
	linkPrefix = "https://auth.example.com/marineris-link/#"
	// invitedPassword: what invited people choose in these tests.
	invitedPassword = "Olympus-Mons-2026!"
)

// inviteEnv: usersEnv (the Owner, Administrator bob, carol in family) with
// the domain example.com, whose Authelia portal is auth.example.com.
func inviteEnv(t *testing.T) *env {
	e := usersEnv(t)
	if err := e.store.CreateDomain(context.Background(), &store.Domain{Name: "example.com"}); err != nil {
		t.Fatal(err)
	}
	return e
}

// invite creates an Invite as c and returns the response and the link's
// token.
func invite(t *testing.T, c *client, body map[string]any) (resp, string) {
	t.Helper()
	r := c.do("POST", "/api/invites", body)
	url, _ := r.Body["url"].(string)
	if r.Status == 200 && !strings.HasPrefix(url, linkPrefix) {
		t.Fatalf("link address: %q", url)
	}
	return r, strings.TrimPrefix(url, linkPrefix)
}

// linkPage is the link page on the portal's host, holding token.
func (e *env) linkPage(token string) *client {
	c := &client{t: e.t, base: e.edge.URL, origin: "https://auth.example.com", header: map[string]string{}}
	if token != "" {
		c.header[linkHeader] = token
	}
	return c
}

// accept goes through the Invite page with password and authenticator and
// returns the finish response and the authenticator's secret.
func (e *env) accept(token, password string) (resp, string) {
	e.t.Helper()
	page := e.linkPage(token)
	r := page.do("POST", "/marineris-link/api/totp", nil)
	if r.Status != 200 {
		return r, ""
	}
	secret := r.Body["totp_secret"].(string)
	code, _ := totpNow(secret)
	return page.do("POST", "/marineris-link/api/finish", map[string]string{"password": password, "totp_code": code}), secret
}

func TestInviteAcceptedThenSignsInThroughAuthelia(t *testing.T) {
	e := inviteEnv(t)
	bob := e.signedIn("bob")

	r, token := invite(t, bob, map[string]any{
		"username": "dave", "display_name": "Dave Doe", "email": "dave@example.com", "groups": []string{"family"},
	})
	if r.Status != 200 {
		t.Fatalf("invite: %d %s", r.Status, r.Raw)
	}
	// The link is a 256-bit random token.
	if len(token) != 43 {
		t.Fatalf("token: %q", token)
	}

	// The page is served on the edge listener (Caddy sends it the portal's
	// /marineris-link/ path), without a session.
	page := e.linkPage(token)
	if r := page.do("GET", "/marineris-link/", nil); r.Status != 200 || !strings.Contains(string(r.Raw), "link.js") {
		t.Fatalf("page: %d", r.Status)
	}
	r = page.do("POST", "/marineris-link/api/read", nil)
	if r.Status != 200 || r.Body["kind"] != "invite" || r.Body["username"] != "dave" || r.Body["display_name"] != "Dave Doe" {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	if brand, _ := r.Body["brand"].(map[string]any); brand["name"] == "" || brand["name"] == nil {
		t.Fatalf("read without branding: %s", r.Raw)
	}
	// Not a User until the second factor is set.
	if _, err := e.store.GetUserByName(context.Background(), "dave"); err == nil {
		t.Fatal("dave exists before accepting")
	}

	r, secret := e.accept(token, invitedPassword)
	if r.Status != 200 || r.Body["redirect_url"] != "https://auth.example.com/?rd=https%3A%2F%2Fmarineris.example.com%2F" {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}

	// Authelia knows dave, with his names and Groups.
	if u, found := e.autheliaUsers()["dave"]; !found || u.DisplayName != "Dave Doe" || u.Email != "dave@example.com" ||
		strings.Join(u.Groups, ",") != "family" || u.Disabled {
		t.Fatalf("authelia users: %+v", e.autheliaUsers())
	}
	// Signed in through Authelia, dave is a User, not an Administrator.
	if got := rightsOf(t, e.autheliaClient("dave").do("GET", "/api/auth/session", nil)); got != "dave Dave Doe groups=[family] admin=false proxy=false owner=false" {
		t.Fatalf("session: %s", got)
	}
	// The password and authenticator he chose sign him in too.
	e.setAutheliaProtection(false)
	if r := e.signInDirectAs(e.edgeClient(), "dave", invitedPassword, secret); r.Status != 200 {
		t.Fatalf("direct sign-in: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "invite.create dave true", "invite.accept dave true")
}

// signInDirectAs signs user in with password and authenticator code.
func (e *env) signInDirectAs(c *client, user, password, secret string) resp {
	e.t.Helper()
	r := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": password})
	if r.Status != 200 {
		return r
	}
	return c.do("POST", "/api/auth/login/mfa", map[string]string{"mfa_token": r.Body["mfa_token"].(string), "code": e.unusedCode(user, secret)})
}

// Expired, used, revoked and unknown links get the same answer.
func TestInviteLinkRefusals(t *testing.T) {
	e := inviteEnv(t)
	bob := e.signedIn("bob")
	ctx := context.Background()

	_, used := invite(t, bob, map[string]any{"username": "used"})
	if r, _ := e.accept(used, invitedPassword); r.Status != 200 {
		t.Fatalf("accept: %d %s", r.Status, r.Raw)
	}
	r, revoked := invite(t, bob, map[string]any{"username": "revoked"})
	if r := bob.do("DELETE", "/api/links/"+jsonID(r.Body["id"]), nil); r.Status != 200 {
		t.Fatalf("revoke: %d %s", r.Status, r.Raw)
	}
	_, expired := invite(t, bob, map[string]any{"username": "expired"})
	e.store.SetClock(func() time.Time { return time.Now().UTC().Add(store.LinkTTL + time.Minute) })
	defer e.store.SetClock(func() time.Time { return time.Now().UTC() })

	var first string
	for name, token := range map[string]string{"used": used, "revoked": revoked, "expired": expired, "unknown": "x" + used[1:]} {
		// A different client address each time: this test is not about
		// throttling.
		page := e.linkPage(token)
		page.header["X-Forwarded-For"] = "192.0.2." + map[string]string{"used": "1", "revoked": "2", "expired": "3", "unknown": "4"}[name]
		r := page.do("POST", "/marineris-link/api/read", nil)
		if r.Status != 404 || r.code() != "link_invalid" {
			t.Fatalf("%s: %d %s", name, r.Status, r.Raw)
		}
		if first == "" {
			first = string(r.Raw)
		} else if string(r.Raw) != first {
			t.Fatalf("%s answers differently: %s vs %s", name, r.Raw, first)
		}
		if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"password": invitedPassword, "totp_code": "000000"}); string(r.Raw) != first {
			t.Fatalf("%s finish: %d %s", name, r.Status, r.Raw)
		}
	}
	for _, name := range []string{"revoked", "expired"} {
		if _, err := e.store.GetUserByName(ctx, name); err == nil {
			t.Fatalf("%s was created", name)
		}
	}
	assertAudit(t, e, "invite.revoke revoked true")
}

// jsonID formats a JSON number ID for a path.
func jsonID(v any) string {
	f, _ := v.(float64)
	return strconv.FormatInt(int64(f), 10)
}

func TestInviteUsernameRules(t *testing.T) {
	e := inviteEnv(t)
	bob := e.signedIn("bob")

	for _, bad := range []string{"", "a", "Dave", "-dave", ".dave", "da ve", "dave!", "élodie", strings.Repeat("a", 33)} {
		if r, _ := invite(t, bob, map[string]any{"username": bad}); r.Status != 400 {
			t.Errorf("username %q: %d %s", bad, r.Status, r.Raw)
		}
	}
	for _, good := range []string{"ab", "d.doe_2-x", strings.Repeat("a", 32)} {
		if r, _ := invite(t, bob, map[string]any{"username": good}); r.Status != 200 {
			t.Errorf("username %q: %d %s", good, r.Status, r.Raw)
		}
	}
	// Taken by a User, ignoring case: the Owner is "admin".
	if err := e.store.CreateUser(context.Background(), &store.User{Username: "Erin", PasswordHash: "x", WebAuthnID: []byte("handle-erin")}); err != nil {
		t.Fatal(err)
	}
	for _, taken := range []string{"carol", "admin", "erin"} {
		if r, _ := invite(t, bob, map[string]any{"username": taken}); r.Status != 400 || !strings.Contains(string(r.Raw), "already taken") {
			t.Errorf("username %q: %d %s", taken, r.Status, r.Raw)
		}
	}
	// Reserved by a pending Invite, free again once it is revoked or expired.
	r, _ := invite(t, bob, map[string]any{"username": "dave"})
	if r2, _ := invite(t, bob, map[string]any{"username": "dave"}); r2.Status != 400 || !strings.Contains(string(r2.Raw), "reserved") {
		t.Fatalf("reserved: %d %s", r2.Status, r2.Raw)
	}
	if r := bob.do("DELETE", "/api/links/"+jsonID(r.Body["id"]), nil); r.Status != 200 {
		t.Fatalf("revoke: %d %s", r.Status, r.Raw)
	}
	if r, _ := invite(t, bob, map[string]any{"username": "dave"}); r.Status != 200 {
		t.Fatalf("after revoke: %d %s", r.Status, r.Raw)
	}
	e.store.SetClock(func() time.Time { return time.Now().UTC().Add(store.LinkTTL + time.Minute) })
	defer e.store.SetClock(func() time.Time { return time.Now().UTC() })
	if r, _ := invite(t, bob, map[string]any{"username": "dave"}); r.Status != 200 {
		t.Fatalf("after expiry: %d %s", r.Status, r.Raw)
	}
}

func TestInviteNeedsStepUpAndAPortal(t *testing.T) {
	e := usersEnv(t)
	// Signed in through Authelia, without a recent step-up.
	c := e.autheliaClient("bob")
	c.do("GET", "/api/auth/session", nil)
	if r, _ := invite(t, c, map[string]any{"username": "dave"}); r.Status != 403 || r.code() != "reauth_required" {
		t.Fatalf("without step-up: %d %s", r.Status, r.Raw)
	}
	// No domain yet: no portal for the link to open on.
	if r, _ := invite(t, e.signedIn("bob"), map[string]any{"username": "dave"}); r.Status != 409 || r.code() != "no_portal" {
		t.Fatalf("without a portal: %d %s", r.Status, r.Raw)
	}
}

func TestInviteGroups(t *testing.T) {
	e := inviteEnv(t)
	bob := e.signedIn("bob")
	ctx := context.Background()
	for _, g := range []string{"friends", "gone"} {
		if err := e.store.CreateGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	// An Administrator invites into custom Groups only.
	for _, builtin := range []string{store.GroupAdmins, store.GroupProxyAdmins} {
		if r, _ := invite(t, bob, map[string]any{"username": "dave", "groups": []string{"family", builtin}}); r.Status != 403 || r.code() != "builtin_group" {
			t.Fatalf("%s: %d %s", builtin, r.Status, r.Raw)
		}
	}
	if r, _ := invite(t, bob, map[string]any{"username": "dave", "groups": []string{"nope"}}); r.Status != 400 {
		t.Fatalf("unknown group: %d %s", r.Status, r.Raw)
	}
	r, token := invite(t, bob, map[string]any{"username": "dave", "groups": []string{"Friends", "gone"}})
	if r.Status != 200 || strings.Join(stringsOf(r.Body["groups"]), ",") != "friends,gone" {
		t.Fatalf("invite: %d %s", r.Status, r.Raw)
	}
	// A renamed Group follows the Invite; a deleted one is dropped.
	if err := e.store.RenameGroup(ctx, "friends", "pals"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteGroup(ctx, "gone"); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.accept(token, invitedPassword); r.Status != 200 {
		t.Fatalf("accept: %d %s", r.Status, r.Raw)
	}
	u, err := e.store.GetUserByName(ctx, "dave")
	if err != nil || strings.Join(u.Groups, ",") != "pals" {
		t.Fatalf("dave: %+v %v", u, err)
	}
	assertAudit(t, e, "invite.create dave false")
}

func stringsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestPendingInvitesAreListedButNotUsers(t *testing.T) {
	e := inviteEnv(t)
	bob := e.signedIn("bob")
	invite(t, bob, map[string]any{"username": "dave", "display_name": "Dave Doe", "groups": []string{"family"}})

	r := bob.do("GET", "/api/links", nil)
	list := r.array()
	if r.Status != 200 || len(list) != 1 || list[0]["kind"] != "invite" || list[0]["username"] != "dave" ||
		list[0]["display_name"] != "Dave Doe" || list[0]["created_by"] != "bob" || list[0]["expires_at"] == nil {
		t.Fatalf("links: %d %s", r.Status, r.Raw)
	}
	// The token was shown once: never again.
	if strings.Contains(string(r.Raw), "url") || strings.Contains(string(r.Raw), "token") {
		t.Fatalf("the list shows the link: %s", r.Raw)
	}
	// A pending Invite is not a User: not on the Users page, not in Authelia.
	if strings.Contains(string(bob.do("GET", "/api/users", nil).Raw), "dave") {
		t.Fatal("dave is listed as a User")
	}
	if _, found := e.autheliaUsers()["dave"]; found {
		t.Fatal("a pending Invite is rendered in Authelia")
	}
}

func TestInviteFinishRefusesWeakPasswordAndWrongCode(t *testing.T) {
	e := inviteEnv(t)
	_, token := invite(t, e.signedIn("bob"), map[string]any{"username": "dave"})
	page := e.linkPage(token)
	r := page.do("POST", "/marineris-link/api/totp", nil)
	secret, _ := r.Body["totp_secret"].(string)
	code, _ := totpNow(secret)

	if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"password": "short", "totp_code": code}); r.Status != 400 || !strings.Contains(string(r.Raw), "password") {
		t.Fatalf("weak password: %d %s", r.Status, r.Raw)
	}
	if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"password": "dave-" + invitedPassword, "totp_code": code}); r.Status != 400 {
		t.Fatalf("password with the username: %d %s", r.Status, r.Raw)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"password": invitedPassword, "totp_code": wrong}); r.Status != 400 || !strings.Contains(string(r.Raw), "totp_code") {
		t.Fatalf("wrong code: %d %s", r.Status, r.Raw)
	}
	if _, err := e.store.GetUserByName(context.Background(), "dave"); err == nil {
		t.Fatal("dave was created")
	}
	// The link still works.
	if r := page.do("POST", "/marineris-link/api/finish", map[string]string{"password": invitedPassword, "totp_code": code}); r.Status != 200 {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}
	assertAudit(t, e, "invite.accept dave false", "invite.accept dave true")
}

func TestLinkPageIsThrottledAndOnlyForThePortal(t *testing.T) {
	e := inviteEnv(t)
	_, token := invite(t, e.signedIn("bob"), map[string]any{"username": "dave"})

	// From the UI's origin, or any other: refused.
	other := e.linkPage(token)
	other.origin = "https://" + uiHost
	if r := other.do("POST", "/marineris-link/api/read", nil); r.Status != 403 {
		t.Fatalf("UI origin: %d %s", r.Status, r.Raw)
	}
	// From the portal of another domain: refused, links open on the UI
	// domain's portal only.
	if err := e.store.CreateDomain(context.Background(), &store.Domain{Name: "friend.org"}); err != nil {
		t.Fatal(err)
	}
	other.origin = "https://auth.friend.org"
	if r := other.do("POST", "/marineris-link/api/read", nil); r.Status != 403 || r.code() != "bad_origin" {
		t.Fatalf("another portal's origin: %d %s", r.Status, r.Raw)
	}
	// Not on the local listener.
	local := e.linkPage(token)
	local.base = e.local.URL
	if r := local.do("POST", "/marineris-link/api/read", nil); r.Body["username"] != nil || r.code() != "" {
		t.Fatalf("local listener: %d %s", r.Status, r.Raw)
	}

	// Guessing tokens gets throttled, like guessing passwords.
	guess := e.linkPage("not-a-token")
	var last resp
	for range 7 {
		last = guess.do("POST", "/marineris-link/api/read", nil)
	}
	if last.Status != 429 {
		t.Fatalf("guessing: %d %s", last.Status, last.Raw)
	}
	// Another client is not locked out.
	page := e.linkPage(token)
	page.header["X-Forwarded-For"] = "192.0.2.9"
	if r := page.do("POST", "/marineris-link/api/read", nil); r.Status != 200 {
		t.Fatalf("other client: %d %s", r.Status, r.Raw)
	}
}
