package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/store"
)

// fakeAutheliaPasskeys stands for Authelia's database.
type fakeAutheliaPasskeys struct {
	mu      sync.Mutex
	handles map[string]string // rpid/username → user handle
	keys    []configmgr.AutheliaPasskey
	added   []configmgr.NewAutheliaPasskey
}

func (f *fakeAutheliaPasskeys) AutheliaPasskeys(_ context.Context, username string) ([]configmgr.AutheliaPasskey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []configmgr.AutheliaPasskey{}
	for i, k := range f.keys {
		if f.added[i].Username == username {
			out = append(out, k)
		}
	}
	return out, nil
}

func (f *fakeAutheliaPasskeys) AutheliaUserHandle(_ context.Context, rpid, username string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h, ok := f.handles[rpid+"/"+username]; ok {
		return h, nil
	}
	return "handle-of-" + username, nil
}

func (f *fakeAutheliaPasskeys) AddAutheliaPasskey(_ context.Context, p configmgr.NewAutheliaPasskey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handles == nil {
		f.handles = map[string]string{}
	}
	f.handles[p.RPID+"/"+p.Username] = p.UserHandle
	f.added = append(f.added, p)
	f.keys = append(f.keys, configmgr.AutheliaPasskey{
		ID: int64(len(f.keys) + 1), Name: p.Name, Host: p.RPID, CreatedAt: time.Now(),
		KID: base64.StdEncoding.EncodeToString(p.Credential.ID),
	})
	return nil
}

func (f *fakeAutheliaPasskeys) DeleteAutheliaPasskey(_ context.Context, username string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, k := range f.keys {
		if k.ID == id && f.added[i].Username == username {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			f.added = append(f.added[:i], f.added[i+1:]...)
			return nil
		}
	}
	return store.ErrNotFound
}

func (f *fakeAutheliaPasskeys) DeleteAutheliaUser(ctx context.Context, username string) error {
	_ = f.DeleteAutheliaSecondFactors(ctx, username)
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.handles {
		if strings.HasSuffix(k, "/"+username) {
			delete(f.handles, k)
		}
	}
	return nil
}

// DeleteAutheliaSecondFactors removes the User's passkeys (the fake keeps no
// authenticator); the user handle stays.
func (f *fakeAutheliaPasskeys) DeleteAutheliaSecondFactors(_ context.Context, username string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.keys) - 1; i >= 0; i-- {
		if f.added[i].Username == username {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			f.added = append(f.added[:i], f.added[i+1:]...)
		}
	}
	return nil
}

// authenticator is a software passkey ("none" attestation, ES256).
type authenticator struct {
	key  *ecdsa.PrivateKey
	id   []byte
	user []byte
}

func newAuthenticator(t *testing.T) *authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &authenticator{key: key, id: id}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// create answers the creation options as a browser would, from origin.
func (a *authenticator) create(t *testing.T, options map[string]any, origin string) map[string]any {
	t.Helper()
	pk := options["publicKey"].(map[string]any)
	rpID := pk["rp"].(map[string]any)["id"].(string)
	user, _ := base64.RawURLEncoding.DecodeString(pk["user"].(map[string]any)["id"].(string))
	a.user = user
	clientData, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": pk["challenge"], "origin": origin})
	rpHash := sha256.Sum256([]byte(rpID))
	x, y := a.key.X.FillBytes(make([]byte, 32)), a.key.Y.FillBytes(make([]byte, 32))
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	authData := append([]byte{}, rpHash[:]...)
	authData = append(authData, 0x01|0x04|0x40) // user present, verified, attested data
	authData = binary.BigEndian.AppendUint32(authData, 0)
	authData = append(authData, make([]byte, 16)...) // AAGUID
	authData = binary.BigEndian.AppendUint16(authData, uint16(len(a.id)))
	authData = append(authData, a.id...)
	authData = append(authData, cose...)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authData})
	return map[string]any{
		"id": b64url(a.id), "rawId": b64url(a.id), "type": "public-key",
		"clientExtensionResults": map[string]any{"credProps": map[string]any{"rk": true}},
		"response":               map[string]any{"clientDataJSON": b64url(clientData), "attestationObject": b64url(att), "transports": []string{"internal"}},
	}
}

// portalEnv: setup done, the UI on marineris.example.com behind Authelia,
// whose portal is auth.example.com; c is signed in to Authelia, with a
// recent step-up.
func portalEnv(t *testing.T) (*env, *client) {
	e := newEnv(t)
	e.completeSetup()
	ctx := context.Background()
	if err := e.store.CreateDomain(ctx, &store.Domain{Name: "example.com"}); err != nil {
		t.Fatal(err)
	}
	e.setAutheliaProtection(true)
	c := e.autheliaClient(testUser)
	r := c.do("GET", "/api/auth/session", nil)
	if r.Status != 200 {
		t.Fatalf("session: %d %s", r.Status, r.Raw)
	}
	c.csrf = r.Body["csrf_token"].(string)
	se, err := e.store.GetSession(ctx, auth.HashToken(c.cookie))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkSessionAuth(ctx, se.IDHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return e, c
}

// pageClient is the page on the portal's host.
func (e *env) pageClient(ticket string) *client {
	c := &client{t: e.t, base: e.edge.URL, origin: "https://auth.example.com", header: map[string]string{}}
	if ticket != "" {
		c.header[ticketHeader] = ticket
	}
	return c
}

func TestAddAutheliaPasskeyFromTheUI(t *testing.T) {
	e, c := portalEnv(t)

	r := c.do("POST", "/api/account/authelia-passkeys", map[string]string{"name": "Laptop"})
	if r.Status != 200 {
		t.Fatalf("ticket: %d %s", r.Status, r.Raw)
	}
	url, _ := r.Body["url"].(string)
	prefix := "https://auth.example.com/marineris-passkey/#"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("page address: %q", url)
	}
	ticket := strings.TrimPrefix(url, prefix)

	// The page is served on the edge listener (Caddy sends it the portal's
	// /marineris-passkey/ path).
	page := e.pageClient("")
	if r := page.do("GET", "/marineris-passkey/", nil); r.Status != 200 || !strings.Contains(string(r.Raw), "passkey.js") {
		t.Fatalf("page: %d", r.Status)
	}

	// The ticket only works from the portal's host.
	for _, bad := range []*client{
		{t: t, base: e.edge.URL, origin: "https://" + uiHost, header: map[string]string{ticketHeader: ticket}},
		{t: t, base: e.edge.URL, origin: "https://evil.example", header: map[string]string{ticketHeader: ticket}},
		{t: t, base: e.edge.URL, origin: "https://auth.example.com", header: map[string]string{ticketHeader: ticket, "Sec-Fetch-Site": "cross-site"}},
		e.pageClient(""),
		e.pageClient("forged"),
	} {
		if r := bad.do("POST", "/marineris-passkey/api/begin", nil); r.Status != http.StatusForbidden {
			t.Fatalf("begin from %s: %d %s", bad.origin, r.Status, r.Raw)
		}
	}
	page = e.pageClient(ticket)
	r = page.do("POST", "/marineris-passkey/api/begin", nil)
	if r.Status != 200 || r.Body["name"] != "Laptop" || r.Body["username"] != testUser {
		t.Fatalf("begin: %d %s", r.Status, r.Raw)
	}
	opts := r.Body["options"].(map[string]any)
	pk := opts["publicKey"].(map[string]any)
	if pk["rp"].(map[string]any)["id"] != "auth.example.com" {
		t.Fatalf("relying party: %v", pk["rp"])
	}
	if sel := pk["authenticatorSelection"].(map[string]any); sel["residentKey"] != "required" || sel["userVerification"] != "required" {
		t.Fatalf("selection: %v", sel)
	}

	a := newAuthenticator(t)
	r = page.do("POST", "/marineris-passkey/api/finish", a.create(t, opts, "https://auth.example.com"))
	if r.Status != 200 || r.Body["return_url"] != "https://"+uiHost+"/account?passkey=added" {
		t.Fatalf("finish: %d %s", r.Status, r.Raw)
	}
	added := e.autheliaKeys.added
	if len(added) != 1 || added[0].RPID != "auth.example.com" || added[0].Username != testUser ||
		added[0].Name != "Laptop" || !added[0].Discoverable || string(a.user) != added[0].UserHandle ||
		string(added[0].Credential.ID) != string(a.id) {
		t.Fatalf("registered in Authelia: %+v", added)
	}

	// The ticket worked once.
	if r := page.do("POST", "/marineris-passkey/api/begin", nil); r.Status != http.StatusForbidden || r.code() != "invalid_ticket" {
		t.Fatalf("ticket reused: %d %s", r.Status, r.Raw)
	}

	// The UI lists it, refuses a second one with the same name, and
	// removes it.
	list := c.do("GET", "/api/account/authelia-passkeys", nil).array()
	if len(list) != 1 || list[0]["name"] != "Laptop" || list[0]["host"] != "auth.example.com" || list[0]["kid"] != nil {
		t.Fatalf("list: %v", list)
	}
	if r := c.do("POST", "/api/account/authelia-passkeys", map[string]string{"name": "laptop"}); r.Status != http.StatusBadRequest {
		t.Fatalf("duplicate name: %d %s", r.Status, r.Raw)
	}
	if r := c.do("DELETE", "/api/account/authelia-passkeys/1", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	if r := c.do("DELETE", "/api/account/authelia-passkeys/1", nil); r.Status != http.StatusNotFound {
		t.Fatalf("delete again: %d %s", r.Status, r.Raw)
	}
}

func TestAutheliaPasskeyRefusals(t *testing.T) {
	e, c := portalEnv(t)

	// A passkey made for another origin is refused, and the ticket is spent.
	ticket := strings.Split(c.do("POST", "/api/account/authelia-passkeys", map[string]string{"name": "Key"}).Body["url"].(string), "#")[1]
	page := e.pageClient(ticket)
	opts := page.do("POST", "/marineris-passkey/api/begin", nil).Body["options"].(map[string]any)
	if r := page.do("POST", "/marineris-passkey/api/finish", newAuthenticator(t).create(t, opts, "https://evil.example")); r.Status != http.StatusBadRequest {
		t.Fatalf("foreign origin passkey: %d %s", r.Status, r.Raw)
	}
	if r := page.do("POST", "/marineris-passkey/api/begin", nil); r.Status != http.StatusForbidden {
		t.Fatalf("ticket after a failure: %d", r.Status)
	}
	if len(e.autheliaKeys.added) != 0 {
		t.Fatal("refused passkey registered")
	}

	// Bad names.
	for _, name := range []string{"", "  ", strings.Repeat("x", 65)} {
		if r := c.do("POST", "/api/account/authelia-passkeys", map[string]string{"name": name}); r.Status != http.StatusBadRequest {
			t.Fatalf("name %q: %d", name, r.Status)
		}
	}

	// Not behind Authelia: no portal, no ticket.
	e.setAutheliaProtection(false)
	if r := c.do("POST", "/api/account/authelia-passkeys", map[string]string{"name": "Key"}); r.Status == 200 {
		t.Fatalf("ticket without Authelia protection: %d %s", r.Status, r.Raw)
	}

	// The page and its API are not on the local listener.
	l := &client{t: t, base: e.local.URL, origin: "https://auth.example.com", header: map[string]string{ticketHeader: ticket}}
	if r := l.do("POST", "/marineris-passkey/api/begin", nil); r.Body["options"] != nil || r.code() == "invalid_ticket" {
		t.Fatalf("local listener: %d %s", r.Status, r.Raw)
	}
}
