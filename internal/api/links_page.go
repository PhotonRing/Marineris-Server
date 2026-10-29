package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
)

// The page that opens Invites (and Reset links), on the Authelia portal's
// host (caddy.PortalLinkPath): the person is not signed in, and the UI's
// host may be behind Authelia. The link's token, in the URL fragment (never
// sent to a server, nor logged), comes in linkHeader and is the credential.
// An Invite becomes a User only once a password and a second factor are
// set.

const linkHeader = "X-Marineris-Link"

// linkTOTPTTL: how long an authenticator being set up through a link waits
// for its first code.
const linkTOTPTTL = 15 * time.Minute

// errLinkInvalid is the one answer for unknown, expired, used and revoked
// links alike.
var errLinkInvalid = &Error{Status: http.StatusNotFound, Code: "link_invalid",
	Message: "this link is not valid: it may have expired, been used or been revoked; ask for a new one"}

// pageLink returns the pending link of the request, throttled like login:
// unknown tokens count against the client's address, wrong codes against
// the link too (keys).
func (s *Server) pageLink(r *http.Request, rc *reqCtx) (*store.Link, []string, error) {
	ip := ipKey(rc, r)
	if okay, wait := s.Throttle.Allowed(ip); !okay {
		return nil, nil, tooMany(wait)
	}
	token := r.Header.Get(linkHeader)
	var l *store.Link
	err := store.ErrNotFound
	if token != "" && len(token) <= 64 {
		l, err = s.Store.PendingLink(r.Context(), auth.HashToken(token))
	}
	if errors.Is(err, store.ErrNotFound) {
		s.Throttle.Fail(ip)
		return nil, nil, errLinkInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	keys := []string{ip, "link:" + strconv.FormatInt(l.ID, 10)}
	if okay, wait := s.Throttle.Allowed(keys...); !okay {
		return nil, nil, tooMany(wait)
	}
	return l, keys, nil
}

// handleLinkRead tells the page whose link it is and what to set.
func (s *Server) handleLinkRead(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, _, err := s.pageLink(r, rc)
	if err != nil {
		return err
	}
	ctx := r.Context()
	b, err := s.brand(ctx, rc.settings)
	if err != nil {
		return err
	}
	display := l.DisplayName
	if l.Kind == store.LinkReset {
		u, err := s.Store.GetUser(ctx, l.UserID)
		if err != nil {
			return err
		}
		display = u.DisplayName
	}
	return ok(w, map[string]any{
		"kind":         l.Kind,
		"username":     l.Username,
		"display_name": display,
		// The page is on the portal's host: Marineris's image URLs are
		// not reachable there, Authelia's logo is.
		"brand": map[string]string{"name": b.Name, "accent": b.Accent},
		"sets":  linkSets(l),
		// The second factor is one of these (links_passkey.go).
		"second_factors": s.linkSecondFactors(rc.settings),
	})
}

// linkSets is what the link's page asks for: both for an Invite, what it
// resets for a Reset link.
func linkSets(l *store.Link) []string {
	sets := []string{}
	if l.Kind == store.LinkInvite || l.ResetsPassword() {
		sets = append(sets, "password")
	}
	if l.Kind == store.LinkInvite || l.ResetsSecondFactor() {
		sets = append(sets, "second_factor")
	}
	return sets
}

// setsSecondFactor reports whether the link's page sets a second factor.
func setsSecondFactor(l *store.Link) bool {
	return l.Kind == store.LinkInvite || l.ResetsSecondFactor()
}

// handleLinkTOTP starts setting up an authenticator for the link's User.
func (s *Server) handleLinkTOTP(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, _, err := s.pageLink(r, rc)
	if err != nil {
		return err
	}
	if !setsSecondFactor(l) {
		return badRequest("this link does not reset the second factor")
	}
	secret := auth.NewTOTPSecret()
	now := time.Now()
	s.linkMu.Lock()
	for id, p := range s.linkTOTP {
		if now.After(p.expires) {
			delete(s.linkTOTP, id)
		}
	}
	if len(s.linkTOTP) >= 1000 {
		s.linkMu.Unlock()
		return &Error{Status: http.StatusTooManyRequests, Code: "busy", Message: "too many links being used; try again in a few minutes"}
	}
	s.linkTOTP[l.ID] = &totpPending{secret: secret, expires: now.Add(linkTOTPTTL)}
	s.linkMu.Unlock()
	uri := auth.TOTPURI("Marineris", l.Username, secret)
	return ok(w, map[string]string{"totp_secret": secret, "otpauth_uri": uri, "qr": qrDataURL(uri)})
}

// linkFinish is what the link's page sends: the fields of what the link
// sets. The second factor is an authenticator's code or a passkey (the
// browser's answer to the creation options, and its name).
type linkFinish struct {
	Password    string          `json:"password"`
	TOTPCode    string          `json:"totp_code"`
	Passkey     json.RawMessage `json:"passkey"`
	PasskeyName string          `json:"passkey_name"`
}

// handleLinkFinish sets what the link sets: an Invite creates its User, a
// Reset link replaces the User's password, second factor or both.
func (s *Server) handleLinkFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, keys, err := s.pageLink(r, rc)
	if err != nil {
		return err
	}
	var req linkFinish
	if err := decode(r, &req); err != nil {
		return err
	}
	switch l.Kind {
	case store.LinkInvite:
		return s.finishInvite(w, r, rc, l, keys, req)
	case store.LinkReset:
		return s.finishReset(w, r, rc, l, keys, req)
	}
	return errLinkInvalid
}

// linkSecondFactor is the second factor set up on the link's page: an
// authenticator, or a passkey (then totpSecret is empty).
type linkSecondFactor struct {
	totpSecret string // in clear: sealed with the User's WebAuthn ID
	totpStep   int64  // the step of the code that confirmed it
	passkey    *linkNewPasskey
}

// secondFactor checks the second factor set up on the link's page: the
// authenticator, confirmed by a valid code, or a verified passkey. A wrong
// code or passkey counts against the link and is audited as action.
func (s *Server) secondFactor(r *http.Request, rc *reqCtx, l *store.Link, keys []string, req linkFinish, action string) (*linkSecondFactor, error) {
	withPasskey := len(req.Passkey) > 0 && string(req.Passkey) != "null"
	switch {
	case withPasskey && req.TOTPCode != "":
		return nil, fieldError("second_factor", "an authenticator code or a passkey, not both")
	case !withPasskey && req.TOTPCode == "":
		return nil, fieldError("second_factor", "set up an authenticator app or a passkey")
	case withPasskey:
		return s.passkeyFactor(r, rc, l, keys, req, action)
	}
	s.linkMu.Lock()
	p := s.linkTOTP[l.ID]
	s.linkMu.Unlock()
	if p == nil || time.Now().After(p.expires) {
		return nil, badRequest("set up the authenticator again")
	}
	step, good := auth.VerifyTOTP(p.secret, req.TOTPCode, time.Now())
	if !good {
		s.Throttle.Fail(keys...)
		s.audit(r, rc, action, l.Username, false, "wrong authenticator code")
		return nil, fieldError("totp_code", "is not valid; check the time on your phone")
	}
	return &linkSecondFactor{totpSecret: p.secret, totpStep: step}, nil
}

// linkDone forgets what the page set up for the link once it is used.
func (s *Server) linkDone(l *store.Link, keys []string) {
	s.linkMu.Lock()
	delete(s.linkTOTP, l.ID)
	delete(s.linkPasskey, l.ID)
	s.linkMu.Unlock()
	s.Throttle.Succeed(keys[1:]...)
}

// finishInvite creates the Invite's User, with their Groups, only when the
// password and the second factor are both valid.
func (s *Server) finishInvite(w http.ResponseWriter, r *http.Request, rc *reqCtx, l *store.Link, keys []string, req linkFinish) error {
	if err := auth.CheckPasswordPolicy(l.Username, req.Password); err != nil {
		return fieldError("password", err.Error())
	}
	sf, err := s.secondFactor(r, rc, l, keys, req, "invite.accept")
	if err != nil {
		return err
	}
	ctx := r.Context()
	hash, err := auth.HashPassword(ctx, req.Password)
	if err != nil {
		return err
	}
	handle := make([]byte, 64)
	if _, err := rand.Read(handle); err != nil {
		return err
	}
	sealed := ""
	if sf.totpSecret != "" {
		if sealed, err = s.Box.Seal([]byte(sf.totpSecret), setup.TOTPContext(handle)); err != nil {
			return err
		}
	}
	step := sf.totpStep
	// A former User with the same Username may have left rows in Authelia:
	// the new one inherits nothing.
	if err := s.deleteAutheliaUser(ctx, l.Username); err != nil {
		s.Log.Warn("clearing Authelia's rows before accepting an Invite", "user", l.Username, "err", err)
	}
	// A passkey is in Authelia before the User exists.
	if sf.passkey != nil {
		if err := s.registerLinkPasskey(r, rc, "invite.accept", l.Username, sf.passkey); err != nil {
			return err
		}
	}
	u := &store.User{PasswordHash: hash, TOTPSecret: sealed, TOTPLastStep: step, WebAuthnID: handle}
	err = s.Store.AcceptInvite(ctx, l, u)
	if err != nil && sf.passkey != nil {
		s.dropLinkPasskey(ctx, l.Username, sf.passkey)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errLinkInvalid
	case errors.Is(err, store.ErrUsernameTaken):
		s.audit(r, rc, "invite.accept", l.Username, false, "username taken")
		return &Error{Status: http.StatusConflict, Code: "username_taken",
			Message: "a User with this Username was created meanwhile; ask for a new Invite"}
	case err != nil:
		return err
	}
	s.linkDone(l, keys)
	rc.user = u
	s.audit(r, rc, "invite.accept", u.Username, true, "")
	s.auditLinkPasskey(r, rc, sf)
	s.applyNow(u.Username, "invite accepted by "+u.Username)
	return ok(w, map[string]string{"redirect_url": s.signInURL(ctx, rc.settings)})
}

// finishReset sets only what the Reset link resets; the rest of the User
// stays as it was. A new second factor replaces every old one: Authelia's
// authenticator and passkeys go first, then, with the link used, the
// Marineris authenticator, recovery codes and passkeys. Using the link lifts
// the block on the User.
func (s *Server) finishReset(w http.ResponseWriter, r *http.Request, rc *reqCtx, l *store.Link, keys []string, req linkFinish) error {
	ctx := r.Context()
	u, err := s.Store.GetUser(ctx, l.UserID)
	if err != nil {
		return err
	}
	var change store.ResetChange
	if l.ResetsPassword() {
		if err := auth.CheckPasswordPolicy(u.Username, req.Password); err != nil {
			return fieldError("password", err.Error())
		}
	}
	var sf *linkSecondFactor
	if l.ResetsSecondFactor() {
		if sf, err = s.secondFactor(r, rc, l, keys, req, "reset.use"); err != nil {
			return err
		}
	}
	if l.ResetsPassword() {
		if change.PasswordHash, err = auth.HashPassword(ctx, req.Password); err != nil {
			return err
		}
	}
	if sf != nil {
		if sf.passkey != nil {
			change.Passkey = true
		} else if change.TOTPSecret, err = s.Box.Seal([]byte(sf.totpSecret), setup.TOTPContext(u.WebAuthnID)); err != nil {
			return err
		}
		change.TOTPLastStep = sf.totpStep
		// Before the new factor exists: a lost device must never sign in
		// again. The User stays blocked if this fails.
		if err := s.deleteAutheliaSecondFactors(ctx, u.Username); err != nil {
			s.Log.Error("removing a User's second factors from Authelia's database", "user", u.Username, "err", err)
			s.audit(r, rc, "reset.use", u.Username, false, "authelia: "+err.Error())
			return &Error{Status: http.StatusServiceUnavailable, Code: "authelia_unavailable",
				Message: "your old authenticator and passkeys could not be removed yet; try again in a minute"}
		}
		// Then the new passkey; the User stays blocked if this fails.
		if sf.passkey != nil {
			if err := s.registerLinkPasskey(r, rc, "reset.use", u.Username, sf.passkey); err != nil {
				return err
			}
		}
	}
	err = s.Store.UseReset(ctx, l, change)
	if err != nil && sf != nil && sf.passkey != nil {
		s.dropLinkPasskey(ctx, u.Username, sf.passkey)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errLinkInvalid
	case err != nil:
		return err
	}
	s.linkDone(l, keys)
	rc.user = u
	s.audit(r, rc, "reset.use", u.Username, true, l.Resets)
	s.auditLinkPasskey(r, rc, sf)
	s.applyNow(u.Username, "reset link used by "+u.Username)
	return ok(w, map[string]string{"redirect_url": s.signInURL(ctx, rc.settings)})
}

func (s *Server) deleteAutheliaSecondFactors(ctx context.Context, username string) error {
	if s.AutheliaPasskeys == nil {
		return nil
	}
	err := s.AutheliaPasskeys.DeleteAutheliaSecondFactors(ctx, username)
	if errors.Is(err, configmgr.ErrAutheliaNotReady) {
		return nil
	}
	return err
}

// applyNow applies Authelia's configuration before answering, so the User
// the page sends to the sign-in is known to Authelia, with their
// authenticator. A failure is retried in the background.
func (s *Server) applyNow(actor, reason string) {
	if s.Configs == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := s.Configs.ApplyAuthelia(ctx, actor, reason); err != nil {
		s.Log.Error("authelia apply failed; retrying in the background", "reason", reason, "err", err)
		s.applyAsync(actor, reason, true, false)
	}
}

// signInURL is where a link's page sends the person once done: the Authelia
// sign-in, back to the UI, when Authelia protects the UI; the UI otherwise.
func (s *Server) signInURL(ctx context.Context, st store.Settings) string {
	ui := "https://" + st.UIHost + "/"
	if !st.UIProtectWithAuthelia {
		return ui
	}
	portal, err := s.linkPortal(ctx, st)
	if err != nil || portal == "" {
		return ui
	}
	return portal + "/?rd=" + url.QueryEscape(ui)
}
