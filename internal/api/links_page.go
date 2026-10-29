package api

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
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
	b, err := s.brand(r.Context(), rc.settings)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"kind":         l.Kind,
		"username":     l.Username,
		"display_name": l.DisplayName,
		// The page is on the portal's host: Marineris's image URLs are
		// not reachable there, Authelia's logo is.
		"brand": map[string]string{"name": b.Name, "accent": b.Accent},
		"sets":  []string{"password", "second_factor"},
	})
}

// handleLinkTOTP starts setting up an authenticator for the link's User.
func (s *Server) handleLinkTOTP(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, _, err := s.pageLink(r, rc)
	if err != nil {
		return err
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

// handleLinkFinish sets what the link sets. For an Invite: the User is
// created, with their Groups, only when the password and the
// authenticator's code are both valid.
func (s *Server) handleLinkFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	l, keys, err := s.pageLink(r, rc)
	if err != nil {
		return err
	}
	if l.Kind != store.LinkInvite {
		return errLinkInvalid
	}
	var req struct {
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := auth.CheckPasswordPolicy(l.Username, req.Password); err != nil {
		return fieldError("password", err.Error())
	}
	s.linkMu.Lock()
	p := s.linkTOTP[l.ID]
	s.linkMu.Unlock()
	if p == nil || time.Now().After(p.expires) {
		return badRequest("set up the authenticator again")
	}
	step, good := auth.VerifyTOTP(p.secret, req.TOTPCode, time.Now())
	if !good {
		s.Throttle.Fail(keys...)
		s.audit(r, rc, "invite.accept", l.Username, false, "wrong authenticator code")
		return fieldError("totp_code", "is not valid; check the time on your phone")
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
	sealed, err := s.Box.Seal([]byte(p.secret), setup.TOTPContext(handle))
	if err != nil {
		return err
	}
	// A former User with the same Username may have left rows in Authelia:
	// the new one inherits nothing.
	if err := s.deleteAutheliaUser(ctx, l.Username); err != nil {
		s.Log.Warn("clearing Authelia's rows before accepting an Invite", "user", l.Username, "err", err)
	}
	u := &store.User{PasswordHash: hash, TOTPSecret: sealed, TOTPLastStep: step, WebAuthnID: handle}
	switch err := s.Store.AcceptInvite(ctx, l, u); {
	case errors.Is(err, store.ErrNotFound):
		return errLinkInvalid
	case errors.Is(err, store.ErrUsernameTaken):
		s.audit(r, rc, "invite.accept", l.Username, false, "username taken")
		return &Error{Status: http.StatusConflict, Code: "username_taken",
			Message: "a User with this Username was created meanwhile; ask for a new Invite"}
	case err != nil:
		return err
	}
	s.linkMu.Lock()
	delete(s.linkTOTP, l.ID)
	s.linkMu.Unlock()
	s.Throttle.Succeed(keys[1:]...)
	rc.user = u
	s.audit(r, rc, "invite.accept", u.Username, true, "")
	s.applyNow(u.Username, "invite accepted by "+u.Username)
	return ok(w, map[string]string{"redirect_url": s.signInURL(ctx, rc.settings)})
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
