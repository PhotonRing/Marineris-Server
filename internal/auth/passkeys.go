package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

// RP identifies the relying party for one ceremony: the host the browser is
// on (RP ID) and its exact origin.
type RP struct {
	ID     string
	Origin string
}

// Passkeys runs WebAuthn registration and passwordless login ceremonies.
type Passkeys struct {
	store       *store.Store
	displayName string

	mu      sync.Mutex
	pending map[string]*ceremony
	now     func() time.Time
}

type ceremony struct {
	kind    string // "register" or "login"
	userID  int64
	rp      RP
	session webauthn.SessionData
	expires time.Time
}

const ceremonyTTL = 5 * time.Minute

// ErrPasskey is returned for any failed passkey ceremony. Details are not
// exposed to clients.
var ErrPasskey = errors.New("auth: passkey verification failed")

// NewPasskeys returns a passkey service.
func NewPasskeys(st *store.Store, displayName string) *Passkeys {
	return &Passkeys{store: st, displayName: displayName, pending: map[string]*ceremony{}, now: time.Now}
}

type waUser struct {
	u     *store.User
	creds []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return w.u.WebAuthnID }
func (w *waUser) WebAuthnName() string                       { return w.u.Username }
func (w *waUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func (p *Passkeys) loadUser(ctx context.Context, u *store.User) (*waUser, error) {
	rows, err := p.store.ListWebAuthnCredentials(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	wu := &waUser{u: u}
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(row.Data), &c); err != nil {
			return nil, err
		}
		wu.creds = append(wu.creds, c)
	}
	return wu, nil
}

func (p *Passkeys) instance(rp RP) (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:          rp.ID,
		RPDisplayName: p.displayName,
		RPOrigins:     []string{rp.Origin},
	})
}

func (p *Passkeys) put(c *ceremony) string {
	id := secrets.Token(24)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, v := range p.pending {
		if now.After(v.expires) {
			delete(p.pending, k)
		}
	}
	if len(p.pending) > 10_000 {
		// Refuse to grow without bound under a flood of unfinished ceremonies.
		for k := range p.pending {
			delete(p.pending, k)
			break
		}
	}
	c.expires = now.Add(ceremonyTTL)
	p.pending[id] = c
	return id
}

// take removes and returns a pending ceremony (each can be finished once).
func (p *Passkeys) take(id, kind string) (*ceremony, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.pending[id]
	if !ok {
		return nil, false
	}
	delete(p.pending, id)
	if c.kind != kind || p.now().After(c.expires) {
		return nil, false
	}
	return c, true
}

// BeginRegistration starts registering a new passkey for u.
func (p *Passkeys) BeginRegistration(ctx context.Context, rp RP, u *store.User) (string, *protocol.CredentialCreation, error) {
	wa, err := p.instance(rp)
	if err != nil {
		return "", nil, err
	}
	wu, err := p.loadUser(ctx, u)
	if err != nil {
		return "", nil, err
	}
	var exclude []protocol.CredentialDescriptor
	for i := range wu.creds {
		exclude = append(exclude, wu.creds[i].Descriptor())
	}
	creation, session, err := wa.BeginRegistration(wu,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(exclude),
	)
	if err != nil {
		return "", nil, err
	}
	id := p.put(&ceremony{kind: "register", userID: u.ID, rp: rp, session: *session})
	return id, creation, nil
}

// FinishRegistration verifies the authenticator response and stores the
// passkey under name.
func (p *Passkeys) FinishRegistration(ctx context.Context, rp RP, u *store.User, ceremonyID, name string, body []byte) error {
	c, ok := p.take(ceremonyID, "register")
	if !ok || c.userID != u.ID || c.rp != rp {
		return ErrPasskey
	}
	wa, err := p.instance(rp)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	wu, err := p.loadUser(ctx, u)
	if err != nil {
		return err
	}
	cred, err := wa.CreateCredential(wu, c.session, parsed)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	if !cred.Flags.UserVerified {
		return fmt.Errorf("%w: user verification missing", ErrPasskey)
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return p.store.AddWebAuthnCredential(ctx, &store.WebAuthnCredential{ID: cred.ID, UserID: u.ID, Name: name, Data: string(data)})
}

// BeginLogin starts a passwordless (discoverable credential) login.
func (p *Passkeys) BeginLogin(rp RP) (string, *protocol.CredentialAssertion, error) {
	wa, err := p.instance(rp)
	if err != nil {
		return "", nil, err
	}
	assertion, session, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return "", nil, err
	}
	id := p.put(&ceremony{kind: "login", rp: rp, session: *session})
	return id, assertion, nil
}

// FinishLogin verifies a passkey assertion and returns the user. User
// verification (PIN or biometrics on the authenticator) is required, so a
// passkey login counts as multi-factor.
func (p *Passkeys) FinishLogin(ctx context.Context, rp RP, ceremonyID string, body io.Reader) (*store.User, error) {
	c, ok := p.take(ceremonyID, "login")
	if !ok || c.rp != rp {
		return nil, ErrPasskey
	}
	wa, err := p.instance(rp)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	var found *waUser
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := p.store.GetUserByWebAuthnID(ctx, userHandle)
		if err != nil {
			return nil, err
		}
		wu, err := p.loadUser(ctx, u)
		if err != nil {
			return nil, err
		}
		found = wu
		return wu, nil
	}
	_, cred, err := wa.ValidatePasskeyLogin(handler, c.session, parsed)
	if err != nil || found == nil {
		return nil, fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	if !cred.Flags.UserVerified {
		return nil, fmt.Errorf("%w: user verification missing", ErrPasskey)
	}
	if cred.Authenticator.CloneWarning {
		return nil, fmt.Errorf("%w: possible cloned authenticator", ErrPasskey)
	}
	data, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	if err := p.store.UpdateWebAuthnCredential(ctx, cred.ID, string(data)); err != nil {
		return nil, err
	}
	return found.u, nil
}

// External registration: a passkey for another relying party (the Authelia
// portal), whose user handle that party chose. Marineris verifies it; the
// caller stores it there.

type externalUser struct {
	handle []byte
	name   string
}

func (e externalUser) WebAuthnID() []byte                         { return e.handle }
func (e externalUser) WebAuthnName() string                       { return e.name }
func (e externalUser) WebAuthnDisplayName() string                { return e.name }
func (e externalUser) WebAuthnCredentials() []webauthn.Credential { return nil }

// BeginExternalRegistration starts registering a passkey for username, with
// user handle handle, at rp. exclude lists the user's existing credential
// IDs there, so that an authenticator does not register twice.
func (p *Passkeys) BeginExternalRegistration(rp RP, username string, handle []byte, exclude [][]byte) (string, *protocol.CredentialCreation, error) {
	wa, err := p.instance(rp)
	if err != nil {
		return "", nil, err
	}
	var ex []protocol.CredentialDescriptor
	for _, id := range exclude {
		ex = append(ex, protocol.CredentialDescriptor{Type: protocol.PublicKeyCredentialType, CredentialID: id})
	}
	creation, session, err := wa.BeginRegistration(externalUser{handle: handle, name: username},
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(ex),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()),
	)
	if err != nil {
		return "", nil, err
	}
	id := p.put(&ceremony{kind: "external", rp: rp, session: *session})
	return id, creation, nil
}

// FinishExternalRegistration verifies the authenticator response. The
// passkey is discoverable unless the browser says otherwise.
func (p *Passkeys) FinishExternalRegistration(rp RP, username string, handle []byte, ceremonyID string, body []byte) (*webauthn.Credential, bool, error) {
	c, ok := p.take(ceremonyID, "external")
	if !ok || c.rp != rp {
		return nil, false, ErrPasskey
	}
	wa, err := p.instance(rp)
	if err != nil {
		return nil, false, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	cred, err := wa.CreateCredential(externalUser{handle: handle, name: username}, c.session, parsed)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrPasskey, err)
	}
	if !cred.Flags.UserVerified {
		return nil, false, fmt.Errorf("%w: user verification missing", ErrPasskey)
	}
	discoverable := true
	if props := parsed.ClientExtensionResults.CredProps; props != nil && props.RK != nil {
		discoverable = *props.RK
	}
	return cred, discoverable, nil
}
