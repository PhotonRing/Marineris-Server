package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/store"
)

// The Users page: Administrators list, edit, disable, enable and delete
// Users and set their custom Groups. Who may be acted on is mayActOn's
// decision (people.go). A Username never changes.

// userView is a User as the Users page shows it.
type userView struct {
	*store.User
	// State: active or disabled (Invite and reset pending come with the
	// links).
	State string `json:"state"`
	Owner bool   `json:"owner"`
}

func viewOf(u *store.User, owner int64) userView {
	state := "active"
	if u.Disabled {
		state = "disabled"
	}
	return userView{User: u, State: state, Owner: u.ID == owner}
}

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	owner, err := s.Store.OwnerID(ctx)
	if err != nil {
		return err
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, viewOf(u, owner))
	}
	return ok(w, out)
}

func (s *Server) handleUserGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	u, err := s.targetUser(r)
	if err != nil {
		return err
	}
	owner, err := s.Store.OwnerID(r.Context())
	if err != nil {
		return err
	}
	return ok(w, viewOf(u, owner))
}

// targetUser loads the User named by the {id} path value.
func (s *Server) targetUser(r *http.Request) (*store.User, error) {
	id, err := idParam(r)
	if err != nil {
		return nil, err
	}
	return s.Store.GetUser(r.Context(), id)
}

// actOn loads the target User and checks that rc.user may do action to it;
// refusals are audited. self: whether the action is allowed on oneself.
func (s *Server) actOn(r *http.Request, rc *reqCtx, action string, self bool) (*store.User, error) {
	u, err := s.targetUser(r)
	if err != nil {
		return nil, err
	}
	var refusal error
	if u.ID == rc.user.ID && !self {
		refusal = errSelf
	} else if err := s.mayActOn(r.Context(), rc.user, u); err != nil {
		refusal = err
	}
	var apiErr *Error
	if errors.As(refusal, &apiErr) {
		s.audit(r, rc, action, u.Username, false, apiErr.Code)
	}
	return u, refusal
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	// Strict decoding: a "username" field is refused like any unknown one.
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := s.actOn(r, rc, "user.update", true)
	if err != nil {
		return err
	}
	name, err := cleanDisplayName(req.DisplayName)
	if err != nil {
		return fieldError("display_name", err.Error())
	}
	email, err := cleanEmail(req.Email)
	if err != nil {
		return fieldError("email", err.Error())
	}
	if err := s.Store.UpdateUserProfile(r.Context(), u.ID, name, email); err != nil {
		return err
	}
	s.audit(r, rc, "user.update", u.Username, true, "")
	s.applyAsync(rc.actor(), "user "+u.Username+" updated", true, false)
	return s.handleUserGet(w, r, rc)
}

func (s *Server) handleUserDisable(disabled bool) func(http.ResponseWriter, *http.Request, *reqCtx) error {
	action := "user.enable"
	if disabled {
		action = "user.disable"
	}
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
		u, err := s.actOn(r, rc, action, false)
		if err != nil {
			return err
		}
		// Disabling ends the User's Marineris sessions at once; Authelia
		// re-reads the users file on their next request.
		if err := s.Store.SetUserDisabled(r.Context(), u.ID, disabled); err != nil {
			return err
		}
		s.audit(r, rc, action, u.Username, true, "")
		s.applyAsync(rc.actor(), action+" "+u.Username, true, false)
		return s.handleUserGet(w, r, rc)
	}
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	u, err := s.actOn(r, rc, "user.delete", false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	// Sessions, Marineris passkeys and Group memberships go with the row.
	if err := s.Store.DeleteUser(ctx, u.ID); err != nil {
		return err
	}
	s.audit(r, rc, "user.delete", u.Username, true, "")
	s.applyAsync(rc.actor(), "user "+u.Username+" deleted", true, false)
	out := map[string]any{"ok": true}
	// Authelia's own rows (passkeys, authenticator): a later User with the
	// same Username must inherit nothing.
	if err := s.deleteAutheliaUser(ctx, u.Username); err != nil {
		s.Log.Error("removing a deleted User from Authelia's database", "user", u.Username, "err", err)
		out["warning"] = "the User was deleted, but their passkeys and authenticator could not be removed from Authelia yet: " + err.Error()
	}
	return ok(w, out)
}

func (s *Server) deleteAutheliaUser(ctx context.Context, username string) error {
	if s.AutheliaPasskeys == nil {
		return nil
	}
	err := s.AutheliaPasskeys.DeleteAutheliaUser(ctx, username)
	if errors.Is(err, configmgr.ErrAutheliaNotReady) {
		return nil
	}
	return err
}

var errBuiltinGroups = &Error{Status: http.StatusForbidden, Code: "builtin_group",
	Message: "admins and proxy-admins are managed by the Owner"}

func (s *Server) handleUserGroups(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Groups []string `json:"groups"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := s.actOn(r, rc, "user.groups", true)
	if err != nil {
		return err
	}
	for i, g := range req.Groups {
		req.Groups[i] = strings.ToLower(strings.TrimSpace(g))
	}
	err = s.Store.SetUserCustomGroups(r.Context(), u.ID, req.Groups)
	switch {
	case errors.Is(err, store.ErrBuiltinGroup):
		s.audit(r, rc, "user.groups", u.Username, false, errBuiltinGroups.Code)
		return errBuiltinGroups
	case errors.Is(err, store.ErrNotFound):
		return fieldError("groups", "no such Group")
	case err != nil:
		return err
	}
	s.audit(r, rc, "user.groups", u.Username, true, strings.Join(req.Groups, ","))
	s.applyAsync(rc.actor(), "groups of "+u.Username+" changed", true, false)
	return s.handleUserGet(w, r, rc)
}
