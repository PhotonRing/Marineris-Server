package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

const (
	mfaTTL         = 5 * time.Minute
	mfaMaxAttempts = 5
)

type mfaPending struct {
	userID   int64
	expires  time.Time
	attempts int
}

type totpPending struct {
	secret  string
	expires time.Time
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, _ *reqCtx) error {
	return ok(w, map[string]string{"status": "ok"})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	return ok(w, map[string]any{
		"name":           "Marineris",
		"version":        s.Version,
		"listener":       rc.listener,
		"setup_complete": rc.settings.SetupComplete,
		"ui_host":        rc.settings.UIHost,
	})
}

func qrDataURL(text string) string {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	code.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}

// --- First-run setup -------------------------------------------------------

func (s *Server) handleSetupBegin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Token    string `json:"setup_token"`
		Username string `json:"username"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ip := ipKey(rc, r)
	if okay, wait := s.Throttle.Allowed(ip); !okay {
		return tooMany(wait)
	}
	secret, uri, err := s.Setup.Begin(r.Context(), req.Token, req.Username)
	if errors.Is(err, setup.ErrBadToken) {
		s.Throttle.Fail(ip)
		s.audit(r, rc, "setup.begin", "", false, "invalid setup token")
		return &Error{Status: http.StatusUnauthorized, Code: "bad_setup_token", Message: "invalid setup token"}
	}
	if errors.Is(err, setup.ErrAlreadyComplete) {
		return &Error{Status: http.StatusConflict, Code: "setup_complete", Message: "setup has already been completed"}
	}
	if err != nil {
		return err
	}
	return ok(w, map[string]string{"totp_secret": secret, "otpauth_uri": uri, "qr": qrDataURL(uri)})
}

func (s *Server) handleSetupFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req setup.Request
	if err := decode(r, &req); err != nil {
		return err
	}
	ip := ipKey(rc, r)
	if okay, wait := s.Throttle.Allowed(ip); !okay {
		return tooMany(wait)
	}
	res, err := s.Setup.Finish(r.Context(), req)
	switch {
	case errors.Is(err, setup.ErrBadToken):
		s.Throttle.Fail(ip)
		s.audit(r, rc, "setup.finish", "", false, "invalid setup token")
		return &Error{Status: http.StatusUnauthorized, Code: "bad_setup_token", Message: "invalid setup token"}
	case errors.Is(err, setup.ErrBadTOTP):
		s.Throttle.Fail(ip)
		return fieldError("totp_code", "the authenticator code is not valid; check the time on your phone")
	case errors.Is(err, setup.ErrNoPending):
		return badRequest("start setup again: the enrolment expired")
	case errors.Is(err, setup.ErrAlreadyComplete):
		return &Error{Status: http.StatusConflict, Code: "setup_complete", Message: "setup has already been completed"}
	case err != nil:
		return err
	}
	se, err := s.Sessions.Create(r.Context(), w, r, res.User.ID, "setup")
	if err != nil {
		return err
	}
	rc.user = res.User
	s.audit(r, rc, "setup.finish", res.User.Username, true, "")
	s.applyAsync(res.User.Username, "first-run setup", true, true)
	return ok(w, map[string]any{
		"recovery_codes": res.RecoveryCodes,
		"user":           res.User,
		"csrf_token":     se.CSRFToken,
	})
}

// --- Login -----------------------------------------------------------------

func tooMany(wait time.Duration) *Error {
	secs := int(wait.Seconds()) + 1
	return &Error{Status: http.StatusTooManyRequests, Code: "throttled",
		Message: fmt.Sprintf("too many failed attempts; try again in %d seconds", secs), Details: map[string]int{"retry_after": secs}}
}

// ipKey scopes throttling per listener: failures seen on the public edge
// never lock the loopback-only break-glass listener.
func ipKey(rc *reqCtx, r *http.Request) string {
	return "ip:" + rc.listener + ":" + auth.ClientIP(r)
}

func (s *Server) loginKeys(rc *reqCtx, r *http.Request, username string) []string {
	keys := []string{ipKey(rc, r)}
	// Per-account lockout only applies on the public listener, so an
	// attacker cannot lock the owner out of break-glass access.
	if rc.listener == Edge && username != "" {
		keys = append(keys, "user:"+strings.ToLower(username))
	}
	return keys
}

var errInvalidLogin = &Error{Status: http.StatusUnauthorized, Code: "invalid_login", Message: "invalid username or password"}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if !rc.settings.SetupComplete {
		return &Error{Status: http.StatusServiceUnavailable, Code: "setup_pending", Message: "setup has not been completed"}
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if len(username) > 64 || len(req.Password) > 4*auth.MaxPasswordLen {
		return errInvalidLogin
	}
	keys := s.loginKeys(rc, r, username)
	if okay, wait := s.Throttle.Allowed(keys...); !okay {
		return tooMany(wait)
	}
	ctx := r.Context()
	u, err := s.Store.GetUserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(ctx, req.Password)
		s.Throttle.Fail(keys...)
		s.audit(r, rc, "login.password", username, false, "unknown user")
		return errInvalidLogin
	}
	if err != nil {
		return err
	}
	good, err := auth.VerifyPassword(ctx, u.PasswordHash, req.Password)
	if err != nil {
		return err
	}
	if !good {
		s.Throttle.Fail(keys...)
		s.audit(r, rc, "login.password", username, false, "wrong password")
		return errInvalidLogin
	}
	token := secrets.Token(32)
	s.mfaMu.Lock()
	now := time.Now()
	for k, p := range s.mfa {
		if now.After(p.expires) {
			delete(s.mfa, k)
		}
	}
	s.mfa[token] = &mfaPending{userID: u.ID, expires: now.Add(mfaTTL)}
	s.mfaMu.Unlock()
	return ok(w, map[string]any{"mfa_required": true, "mfa_token": token})
}

func (s *Server) handleLoginMFA(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Token        string `json:"mfa_token"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	s.mfaMu.Lock()
	p, found := s.mfa[req.Token]
	if found && (time.Now().After(p.expires) || p.attempts >= mfaMaxAttempts) {
		delete(s.mfa, req.Token)
		found = false
	}
	s.mfaMu.Unlock()
	if !found {
		return &Error{Status: http.StatusUnauthorized, Code: "mfa_expired", Message: "the login expired; enter your password again"}
	}
	u, err := s.Store.GetUser(ctx, p.userID)
	if err != nil {
		return err
	}
	keys := s.loginKeys(rc, r, u.Username)
	if okay, wait := s.Throttle.Allowed(keys...); !okay {
		return tooMany(wait)
	}
	method := ""
	switch {
	case req.Code != "":
		good, err := s.verifyUserTOTP(ctx, u, req.Code)
		if err != nil {
			return err
		}
		if good {
			method = "password+totp"
		}
	case req.RecoveryCode != "":
		h := auth.HashRecoveryCode(req.RecoveryCode)
		used, err := s.Store.ConsumeRecoveryCode(ctx, u.ID, func(stored string) bool { return secrets.Equal(stored, h) })
		if err != nil {
			return err
		}
		if used {
			method = "password+recovery"
		}
	}
	if method == "" {
		s.mfaMu.Lock()
		p.attempts++
		s.mfaMu.Unlock()
		s.Throttle.Fail(keys...)
		s.audit(r, rc, "login.mfa", u.Username, false, "invalid second factor")
		return &Error{Status: http.StatusUnauthorized, Code: "invalid_code", Message: "invalid code"}
	}
	s.mfaMu.Lock()
	delete(s.mfa, req.Token)
	s.mfaMu.Unlock()
	s.Throttle.Succeed(keys...)
	return s.startSession(w, r, rc, u, method)
}

// verifyUserTOTP checks a code and consumes its time step (single use).
func (s *Server) verifyUserTOTP(ctx context.Context, u *store.User, code string) (bool, error) {
	if u.TOTPSecret == "" {
		return false, nil
	}
	secret, err := s.Box.Open(u.TOTPSecret, setup.TOTPContext(u.WebAuthnID))
	if err != nil {
		return false, err
	}
	step, good := auth.VerifyTOTP(string(secret), code, time.Now())
	if !good {
		return false, nil
	}
	return s.Store.AdvanceTOTPStep(ctx, u.ID, step)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, rc *reqCtx, u *store.User, method string) error {
	// Drop any session the browser already had (prevents fixation).
	_ = s.Sessions.Destroy(r.Context(), w, r)
	se, err := s.Sessions.Create(r.Context(), w, r, u.ID, method)
	if err != nil {
		return err
	}
	rc.user = u
	s.audit(r, rc, "login", u.Username, true, method)
	return ok(w, map[string]any{"user": u, "csrf_token": se.CSRFToken, "method": method})
}

func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if !rc.settings.SetupComplete {
		return &Error{Status: http.StatusServiceUnavailable, Code: "setup_pending", Message: "setup has not been completed"}
	}
	rp, err := s.rp(rc, r)
	if err != nil {
		return err
	}
	if okay, wait := s.Throttle.Allowed(ipKey(rc, r)); !okay {
		return tooMany(wait)
	}
	id, opts, err := s.Passkeys.BeginLogin(rp)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ceremony": id, "options": opts})
}

func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	rp, err := s.rp(rc, r)
	if err != nil {
		return err
	}
	ip := ipKey(rc, r)
	if okay, wait := s.Throttle.Allowed(ip); !okay {
		return tooMany(wait)
	}
	u, err := s.Passkeys.FinishLogin(r.Context(), rp, r.URL.Query().Get("ceremony"), r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		s.Throttle.Fail(ip)
		s.audit(r, rc, "login.passkey", "", false, "")
		if errors.Is(err, auth.ErrPasskey) || errors.Is(err, store.ErrNotFound) {
			return &Error{Status: http.StatusUnauthorized, Code: "invalid_passkey", Message: "passkey not accepted"}
		}
		return err
	}
	s.Throttle.Succeed(ip)
	// Already logged in as the same user: this is a step-up confirmation.
	if se, cur, err := s.Sessions.Load(r.Context(), r); err == nil && cur.ID == u.ID {
		if err := s.Sessions.MarkAuth(r.Context(), se); err != nil {
			return err
		}
		rc.user = u
		s.audit(r, rc, "reauth", u.Username, true, "passkey")
		return ok(w, map[string]any{"user": u, "csrf_token": se.CSRFToken, "method": "passkey"})
	}
	return s.startSession(w, r, rc, u, "passkey")
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	return ok(w, map[string]any{
		"user":       rc.user,
		"csrf_token": rc.session.CSRFToken,
		"method":     rc.session.Method,
		"auth_at":    rc.session.AuthAt,
		"expires_at": rc.session.ExpiresAt,
		"step_up_ok": s.Sessions.RecentlyAuthenticated(rc.session, StepUpWindow),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.Sessions.Destroy(r.Context(), w, r); err != nil {
		return err
	}
	s.audit(r, rc, "logout", rc.actor(), true, "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	keys := s.loginKeys(rc, r, rc.user.Username)
	if okay, wait := s.Throttle.Allowed(keys...); !okay {
		return tooMany(wait)
	}
	ctx := r.Context()
	good, err := auth.VerifyPassword(ctx, rc.user.PasswordHash, req.Password)
	if err != nil {
		return err
	}
	if good {
		good, err = s.verifyUserTOTP(ctx, rc.user, req.Code)
		if err != nil {
			return err
		}
	}
	if !good {
		s.Throttle.Fail(keys...)
		s.audit(r, rc, "reauth", rc.actor(), false, "")
		return &Error{Status: http.StatusUnauthorized, Code: "invalid_login", Message: "invalid password or code"}
	}
	s.Throttle.Succeed(keys...)
	if err := s.Sessions.MarkAuth(ctx, rc.session); err != nil {
		return err
	}
	s.audit(r, rc, "reauth", rc.actor(), true, "password+totp")
	return ok(w, map[string]bool{"ok": true})
}

// --- Account ---------------------------------------------------------------

func (s *Server) handlePasskeyList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	creds, err := s.Store.ListWebAuthnCredentials(r.Context(), rc.user.ID)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, c := range creds {
		out = append(out, map[string]any{
			"id": base64.RawURLEncoding.EncodeToString(c.ID), "name": c.Name,
			"created_at": c.CreatedAt, "last_used_at": c.LastUsedAt,
		})
	}
	return ok(w, out)
}

func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	rp, err := s.rp(rc, r)
	if err != nil {
		return err
	}
	id, opts, err := s.Passkeys.BeginRegistration(r.Context(), rp, rc.user)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ceremony": id, "options": opts})
}

func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	rp, err := s.rp(rc, r)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Passkey"
	}
	if err := validate.DisplayName(name); err != nil {
		return fieldError("name", err.Error())
	}
	body, err := readAll(r)
	if err != nil {
		return err
	}
	if err := s.Passkeys.FinishRegistration(r.Context(), rp, rc.user, r.URL.Query().Get("ceremony"), name, body); err != nil {
		s.audit(r, rc, "passkey.register", name, false, "")
		if errors.Is(err, auth.ErrPasskey) {
			return &Error{Status: http.StatusBadRequest, Code: "invalid_passkey", Message: "the passkey could not be registered"}
		}
		return err
	}
	s.audit(r, rc, "passkey.register", name, true, "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := base64.RawURLEncoding.DecodeString(r.PathValue("id"))
	if err != nil || len(id) == 0 || len(id) > 1024 {
		return badRequest("invalid passkey id")
	}
	if err := s.Store.DeleteWebAuthnCredential(r.Context(), rc.user.ID, id); err != nil {
		return err
	}
	s.audit(r, rc, "passkey.delete", r.PathValue("id"), true, "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	good, err := auth.VerifyPassword(ctx, rc.user.PasswordHash, req.Current)
	if err != nil {
		return err
	}
	if !good {
		s.audit(r, rc, "password.change", rc.actor(), false, "wrong current password")
		return fieldError("current_password", "is not correct")
	}
	if err := auth.CheckPasswordPolicy(rc.user.Username, req.New); err != nil {
		return fieldError("new_password", err.Error())
	}
	hash, err := auth.HashPassword(ctx, req.New)
	if err != nil {
		return err
	}
	if err := s.Store.SetPasswordHash(ctx, rc.user.ID, hash); err != nil {
		return err
	}
	if err := s.Store.DeleteUserSessions(ctx, rc.user.ID, rc.session.IDHash); err != nil {
		return err
	}
	s.audit(r, rc, "password.change", rc.actor(), true, "other sessions revoked")
	s.applyAsync(rc.actor(), "password change", true, false)
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleRecoveryCodes(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	codes, hashes := auth.NewRecoveryCodes()
	if err := s.Store.SetRecoveryCodes(r.Context(), rc.user.ID, hashes); err != nil {
		return err
	}
	s.audit(r, rc, "recovery_codes.regenerate", rc.actor(), true, "")
	return ok(w, map[string]any{"recovery_codes": codes})
}

func (s *Server) handleTOTPBegin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	secret := auth.NewTOTPSecret()
	s.totpMu.Lock()
	s.totpEnrol[rc.user.ID] = &totpPending{secret: secret, expires: time.Now().Add(15 * time.Minute)}
	s.totpMu.Unlock()
	uri := auth.TOTPURI("Marineris", rc.user.Username, secret)
	return ok(w, map[string]string{"totp_secret": secret, "otpauth_uri": uri, "qr": qrDataURL(uri)})
}

func (s *Server) handleTOTPFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	s.totpMu.Lock()
	p := s.totpEnrol[rc.user.ID]
	s.totpMu.Unlock()
	if p == nil || time.Now().After(p.expires) {
		return badRequest("start the authenticator enrolment again")
	}
	step, good := auth.VerifyTOTP(p.secret, req.Code, time.Now())
	if !good {
		return fieldError("code", "is not valid")
	}
	sealed, err := s.Box.Seal([]byte(p.secret), setup.TOTPContext(rc.user.WebAuthnID))
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.Store.SetTOTP(ctx, rc.user.ID, sealed); err != nil {
		return err
	}
	if _, err := s.Store.AdvanceTOTPStep(ctx, rc.user.ID, step); err != nil {
		return err
	}
	s.totpMu.Lock()
	delete(s.totpEnrol, rc.user.ID)
	s.totpMu.Unlock()
	s.audit(r, rc, "totp.enrol", rc.actor(), true, "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleRevokeOthers(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	if err := s.Store.DeleteUserSessions(r.Context(), rc.user.ID, rc.session.IDHash); err != nil {
		return err
	}
	s.audit(r, rc, "sessions.revoke_others", rc.actor(), true, "")
	return ok(w, map[string]bool{"ok": true})
}

// applyAsync applies configurations in the background (used after actions
// whose HTTP response must not wait for an Authelia restart).
func (s *Server) applyAsync(actor, reason string, authelia, caddy bool) {
	if s.Configs == nil {
		return
	}
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if authelia {
			if err := s.Configs.ApplyAuthelia(ctx, actor, reason); err != nil {
				s.Log.Error("authelia apply failed", "reason", reason, "err", err)
			}
		}
		if caddy {
			if err := s.Configs.ApplyCaddy(ctx, actor, reason); err != nil {
				s.Log.Error("caddy apply failed", "reason", reason, "err", err)
			}
		}
	}()
}

func readAll(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err
		}
		return nil, badRequest("cannot read body")
	}
	return b, nil
}
