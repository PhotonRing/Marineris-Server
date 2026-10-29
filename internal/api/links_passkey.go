package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/store"
)

// A link's second factor can be a passkey instead of an authenticator. The
// link's page is on the portal's host, so the passkey is Authelia's (it
// belongs to the portal's address) and signs in everywhere, like those added
// from Account → Passkeys (portal_passkeys.go). Marineris runs the ceremony,
// then imports the credential into Authelia before the User is created (an
// Invite) or unblocked (a Reset link).

// defaultLinkPasskeyName names a passkey whose page gave no name.
const defaultLinkPasskeyName = "Passkey"

// linkPasskeyPending is a passkey ceremony begun on a link's page.
type linkPasskeyPending struct {
	ceremony string
	rpid     string // the portal's host the page is on
	handle   string // the User's WebAuthn ID in Authelia
	expires  time.Time
}

// linkNewPasskey is a verified passkey, to register in Authelia.
type linkNewPasskey struct {
	rpid         string
	handle       string
	name         string
	cred         *webauthn.Credential
	discoverable bool
}

var errNoLinkPasskey = &Error{Status: http.StatusConflict, Code: "no_passkey",
	Message: "a passkey can be the second factor only when Authelia protects the UI; set up an authenticator app"}

// linkPasskeysOffered: a passkey of the portal signs in to the UI only
// through Authelia.
func (s *Server) linkPasskeysOffered(st store.Settings) bool {
	return st.UIProtectWithAuthelia && s.Passkeys != nil && s.AutheliaPasskeys != nil
}

// linkSecondFactors lists the second factors the link's page offers.
func (s *Server) linkSecondFactors(st store.Settings) []string {
	if s.linkPasskeysOffered(st) {
		return []string{"totp", "passkey"}
	}
	return []string{"totp"}
}

// handleLinkPasskey begins creating a passkey for the link's User.
func (s *Server) handleLinkPasskey(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, _, err := s.pageLink(r, rc)
	if err != nil {
		return err
	}
	if !setsSecondFactor(l) {
		return badRequest("this link does not reset the second factor")
	}
	if !s.linkPasskeysOffered(rc.settings) {
		return errNoLinkPasskey
	}
	// The origin is a portal host (checked for every Ticket route).
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Host == "" {
		return errLinkInvalid
	}
	rpid := origin.Host
	ctx := r.Context()
	if l.Kind == store.LinkInvite {
		// The Username is reserved by the Invite, so no User has it: rows a
		// former User left in Authelia go before the new one gets a handle.
		if err := s.deleteAutheliaUser(ctx, l.Username); err != nil {
			s.Log.Warn("clearing Authelia's rows before an Invite's passkey", "user", l.Username, "err", err)
		}
	}
	handle, err := s.AutheliaPasskeys.AutheliaUserHandle(ctx, rpid, l.Username)
	if errors.Is(err, configmgr.ErrAutheliaNotReady) {
		return &Error{Status: http.StatusServiceUnavailable, Code: "authelia_not_ready", Message: "Authelia has not started yet"}
	}
	if err != nil {
		return err
	}
	// No exclusions: the old passkeys of a second-factor reset are removed
	// before the new one is registered.
	ceremony, opts, err := s.Passkeys.BeginExternalRegistration(auth.RP{ID: rpid, Origin: "https://" + rpid}, l.Username, []byte(handle), nil)
	if err != nil {
		return err
	}
	now := time.Now()
	s.linkMu.Lock()
	for id, p := range s.linkPasskey {
		if now.After(p.expires) {
			delete(s.linkPasskey, id)
		}
	}
	if len(s.linkPasskey) >= 1000 {
		s.linkMu.Unlock()
		return &Error{Status: http.StatusTooManyRequests, Code: "busy", Message: "too many links being used; try again in a few minutes"}
	}
	s.linkPasskey[l.ID] = &linkPasskeyPending{ceremony: ceremony, rpid: rpid, handle: handle, expires: now.Add(linkTOTPTTL)}
	s.linkMu.Unlock()
	return ok(w, map[string]any{"options": opts})
}

// passkeyFactor verifies the passkey created on the link's page. A passkey
// that does not verify counts against the link and is audited as action.
func (s *Server) passkeyFactor(r *http.Request, rc *reqCtx, l *store.Link, keys []string, req linkFinish, action string) (*linkSecondFactor, error) {
	if !s.linkPasskeysOffered(rc.settings) {
		return nil, errNoLinkPasskey
	}
	name := strings.TrimSpace(req.PasskeyName)
	if name == "" {
		name = defaultLinkPasskeyName
	}
	if utf8.RuneCountInString(name) > 64 || len(name) > 64 {
		return nil, fieldError("passkey_name", "between 1 and 64 characters")
	}
	s.linkMu.Lock()
	p := s.linkPasskey[l.ID]
	delete(s.linkPasskey, l.ID) // one try per ceremony
	s.linkMu.Unlock()
	rp := auth.RP{}
	if p != nil {
		rp = auth.RP{ID: p.rpid, Origin: "https://" + p.rpid}
	}
	if p == nil || time.Now().After(p.expires) || r.Header.Get("Origin") != rp.Origin {
		return nil, badRequest("create the passkey again")
	}
	cred, discoverable, err := s.Passkeys.FinishExternalRegistration(rp, l.Username, []byte(p.handle), p.ceremony, req.Passkey)
	if errors.Is(err, auth.ErrPasskey) {
		s.Throttle.Fail(keys...)
		s.audit(r, rc, action, l.Username, false, "invalid passkey")
		return nil, &Error{Status: http.StatusBadRequest, Code: "invalid_passkey", Message: "the passkey could not be registered; try again"}
	}
	if err != nil {
		return nil, err
	}
	return &linkSecondFactor{passkey: &linkNewPasskey{rpid: p.rpid, handle: p.handle, name: name, cred: cred, discoverable: discoverable}}, nil
}

// registerLinkPasskey imports the link's passkey into Authelia. A failure
// is audited as action and leaves the link pending.
func (s *Server) registerLinkPasskey(r *http.Request, rc *reqCtx, action, username string, p *linkNewPasskey) error {
	err := s.AutheliaPasskeys.AddAutheliaPasskey(r.Context(), configmgr.NewAutheliaPasskey{
		RPID: p.rpid, Username: username, UserHandle: p.handle, Name: p.name,
		Credential: p.cred, Discoverable: p.discoverable,
	})
	if err == nil {
		return nil
	}
	s.Log.Error("registering a link's passkey in Authelia", "user", username, "err", err)
	s.audit(r, rc, action, username, false, "authelia: "+err.Error())
	return &Error{Status: http.StatusServiceUnavailable, Code: "authelia_unavailable",
		Message: "your passkey could not be registered yet; try again in a minute"}
}

// auditLinkPasskey records the passkey a used link registered, if any.
func (s *Server) auditLinkPasskey(r *http.Request, rc *reqCtx, sf *linkSecondFactor) {
	if sf != nil && sf.passkey != nil {
		s.audit(r, rc, "passkey.register", sf.passkey.name, true, "authelia")
	}
}

// dropLinkPasskey removes the link's passkey from Authelia again, when the
// link could not be used after all.
func (s *Server) dropLinkPasskey(ctx context.Context, username string, p *linkNewPasskey) {
	keys, err := s.AutheliaPasskeys.AutheliaPasskeys(ctx, username)
	for _, k := range keys {
		if kid, e := base64.StdEncoding.DecodeString(k.KID); e == nil && k.Host == p.rpid && bytes.Equal(kid, p.cred.ID) {
			err = s.AutheliaPasskeys.DeleteAutheliaPasskey(ctx, username, k.ID)
		}
	}
	if err != nil {
		s.Log.Warn("removing the passkey of an unused link from Authelia", "user", username, "err", err)
	}
}
