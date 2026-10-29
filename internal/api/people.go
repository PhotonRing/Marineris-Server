package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// Rules shared by every endpoint acting on a User (Users, Groups, links).

// errOwnerOnly: an Administrator tried to act on another Administrator or on
// the Owner. The route was allowed; the target is not.
var errOwnerOnly = &Error{Status: http.StatusForbidden, Code: "owner_only",
	Message: "only the Owner manages Administrators and the Owner"}

// errSelf: nobody disables or deletes themselves.
var errSelf = &Error{Status: http.StatusConflict, Code: "self",
	Message: "you cannot do this to your own User"}

// mayActOn reports whether actor may change target: always on themselves
// and for the Owner; otherwise never on an Administrator or on the Owner.
// Actions that nobody may do to themselves (disable, delete) check errSelf
// on top.
func (s *Server) mayActOn(ctx context.Context, actor, target *store.User) error {
	if actor.ID == target.ID {
		return nil
	}
	owner, err := s.Store.OwnerID(ctx)
	if err != nil {
		return err
	}
	if actor.ID == owner {
		return nil
	}
	if target.ID == owner || target.IsAdministrator() {
		return errOwnerOnly
	}
	return nil
}

// checkActOn is mayActOn with the refusal audited: action on subject,
// details prefix followed by the error code.
func (s *Server) checkActOn(r *http.Request, rc *reqCtx, target *store.User, action, subject, prefix string) error {
	return s.auditRefusal(r, rc, action, subject, prefix, s.mayActOn(r.Context(), rc.user, target))
}

// auditRefusal audits err as a refused action when it is an API refusal
// (*Error), and returns err unchanged. Other errors are not refusals.
func (s *Server) auditRefusal(r *http.Request, rc *reqCtx, action, subject, prefix string, err error) error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		s.audit(r, rc, action, subject, false, prefix+apiErr.Code)
	}
	return err
}

// signInRefused tells why a User may not sign in, or nil. Every sign-in
// path (password, passkey, Authelia's vouching) and every request with an
// existing session asks it.
func signInRefused(u *store.User) *Error {
	if u.Disabled {
		return errDisabled
	}
	if u.ResetPending {
		return errResetPending
	}
	return nil
}

var errDisabled = &Error{Status: http.StatusForbidden, Code: "user_disabled",
	Message: "this User is disabled; ask an Administrator to enable it"}

var errResetPending = &Error{Status: http.StatusForbidden, Code: "reset_pending",
	Message: "a Reset link is pending for this User: open it to set new credentials, or ask an Administrator to revoke it"}

// The values below land in Authelia's users file: they go through
// internal/validate's allow-lists, trimmed first. Errors name the field.

func cleanDisplayName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if err := validate.UserDisplayName(v); err != nil {
		return "", fieldError("display_name", err.Error())
	}
	return v, nil
}

// cleanEmail: an optional e-mail address (empty: none).
func cleanEmail(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if err := validate.Email(v); err != nil {
		return "", fieldError("email", err.Error())
	}
	return v, nil
}

func cleanUsername(v string) (string, error) {
	v = strings.TrimSpace(v)
	if err := validate.Username(v); err != nil {
		return "", fieldError("username", err.Error())
	}
	return v, nil
}

// cleanGroupNames trims and lowercases the Group names of a request in
// place; the store refuses unknown ones.
func cleanGroupNames(names []string) []string {
	for i, g := range names {
		names[i] = strings.ToLower(strings.TrimSpace(g))
	}
	return names
}
