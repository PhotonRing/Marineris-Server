package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/photonring/marineris-server/internal/store"
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

// signInRefused tells why a User may not sign in, or nil. Every sign-in
// path (password, passkey, Authelia's vouching) and every request with an
// existing session asks it.
func signInRefused(u *store.User) *Error {
	if u.Disabled {
		return errDisabled
	}
	return nil
}

var errDisabled = &Error{Status: http.StatusForbidden, Code: "account_disabled",
	Message: "this User is disabled; ask an Administrator to enable it"}

// cleanDisplayName validates a person's display name: 1 to 64 characters,
// no control characters, no surrounding spaces.
func cleanDisplayName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || utf8.RuneCountInString(v) > 64 || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", errors.New("must be 1 to 64 characters, without control characters")
	}
	return v, nil
}

// cleanEmail validates an optional e-mail address (empty: none).
func cleanEmail(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if a, err := mail.ParseAddress(v); err != nil || a.Address != v || len(v) > 254 {
		return "", errors.New("must be a valid e-mail address")
	}
	return v, nil
}

// usernameRE: what works as a Username in Authelia and in every app.
var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)

// cleanUsername validates a new Username: 2 to 32 lowercase letters,
// digits, '.', '_' or '-', starting with a letter or digit. Uniqueness
// (ignoring case) is the store's check.
func cleanUsername(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !usernameRE.MatchString(v) {
		return "", errors.New("2 to 32 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	return v, nil
}
