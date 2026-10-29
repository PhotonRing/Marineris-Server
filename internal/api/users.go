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
	// State: active, disabled or reset pending (pending Invites are not
	// Users: they come with the links).
	State string `json:"state"`
	Owner bool   `json:"owner"`
	// Reset is the Reset link blocking the User, on the User's own page.
	Reset *store.Link `json:"reset,omitempty"`
}

func viewOf(u *store.User, owner int64) userView {
	state := "active"
	if u.Disabled {
		state = "disabled"
	} else if u.ResetPending {
		state = "reset pending"
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
	ctx := r.Context()
	owner, err := s.Store.OwnerID(ctx)
	if err != nil {
		return err
	}
	v := viewOf(u, owner)
	if u.ResetPending {
		if v.Reset, err = s.Store.UserReset(ctx, u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return ok(w, v)
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
	if u.ID == rc.user.ID && !self {
		return u, s.auditRefusal(r, rc, action, u.Username, "", errSelf)
	}
	return u, s.checkActOn(r, rc, u, action, u.Username, "")
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
		return err
	}
	email, err := cleanEmail(req.Email)
	if err != nil {
		return err
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
	err = s.Store.SetUserCustomGroups(r.Context(), u.ID, cleanGroupNames(req.Groups))
	switch {
	case errors.Is(err, store.ErrBuiltinGroup):
		return s.auditRefusal(r, rc, "user.groups", u.Username, "", errBuiltinGroups)
	case errors.Is(err, store.ErrNotFound):
		return fieldError("groups", "no such Group")
	case err != nil:
		return err
	}
	s.audit(r, rc, "user.groups", u.Username, true, strings.Join(req.Groups, ","))
	s.applyAsync(rc.actor(), "groups of "+u.Username+" changed", true, false)
	return s.handleUserGet(w, r, rc)
}
