package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/photonring/marineris-server/internal/store"
)

// The Owner's powers: membership of the built-in Groups and ownership
// handover. Their routes are Owner and step-up in the route table; the rules
// on who may be in which built-in Group are the store's (AddBuiltinMember,
// RemoveBuiltinMember, TransferOwnership).

var (
	errNotAdministrator = &Error{Status: http.StatusConflict, Code: "not_administrator",
		Message: "only Administrators can be Proxy & SSO Administrators or the Owner"}
	errLastAdministrator = &Error{Status: http.StatusConflict, Code: "last_administrator",
		Message: "admins cannot lose its last member"}
	errOwnerAdministrator = &Error{Status: http.StatusConflict, Code: "owner_administrator",
		Message: "the Owner stays an Administrator; hand ownership over first"}
	errUserDisabled = &Error{Status: http.StatusConflict, Code: "user_disabled",
		Message: "a Disabled User cannot become the Owner"}
	errOwnerChanged = &Error{Status: http.StatusConflict, Code: "owner_changed",
		Message: "ownership changed meanwhile; reload and try again"}
)

// ownerRefusal maps the store's built-in Group and ownership rules to API
// errors (nil when err is not one of them).
func ownerRefusal(err error) *Error {
	switch {
	case errors.Is(err, store.ErrNotAdministrator):
		return errNotAdministrator
	case errors.Is(err, store.ErrLastAdministrator):
		return errLastAdministrator
	case errors.Is(err, store.ErrOwnerAdministrator):
		return errOwnerAdministrator
	case errors.Is(err, store.ErrDisabled):
		return errUserDisabled
	case errors.Is(err, store.ErrConflict):
		return errOwnerChanged
	}
	return nil
}

// handleBuiltinMember adds (add) or removes the User {id} to or from the
// built-in group. Both are idempotent.
func (s *Server) handleBuiltinMember(group string, add bool) func(http.ResponseWriter, *http.Request, *reqCtx) error {
	action := "group.member_remove"
	if add {
		action = "group.member_add"
	}
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
		u, err := s.targetUser(r)
		if err != nil {
			return err
		}
		if add {
			err = s.Store.AddBuiltinMember(r.Context(), u.ID, group)
		} else {
			err = s.Store.RemoveBuiltinMember(r.Context(), u.ID, group)
		}
		if refusal := ownerRefusal(err); refusal != nil {
			return s.auditRefusal(r, rc, action, group, u.Username+": ", refusal)
		}
		if err != nil {
			return err
		}
		s.audit(r, rc, action, group, true, u.Username)
		s.applyAsync(rc.actor(), action+" "+group+" "+u.Username, true, false)
		g, err := s.Store.GetGroup(r.Context(), group)
		if err != nil {
			return err
		}
		return ok(w, g)
	}
}

// handleOwnerTransfer hands ownership to another Administrator; the Owner
// stays an Administrator. The session endpoint of both Users reflects it on
// their next request.
func (s *Server) handleOwnerTransfer(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.UserID == rc.user.ID {
		return errSelf
	}
	u, err := s.Store.GetUser(r.Context(), req.UserID)
	if errors.Is(err, store.ErrNotFound) {
		s.audit(r, rc, "owner.transfer", fmt.Sprintf("#%d", req.UserID), false, "not_found")
	}
	if err != nil {
		return err
	}
	err = s.Store.TransferOwnership(r.Context(), rc.user.ID, u.ID)
	if refusal := ownerRefusal(err); refusal != nil {
		return s.auditRefusal(r, rc, "owner.transfer", u.Username, "", refusal)
	}
	if err != nil {
		return err
	}
	s.audit(r, rc, "owner.transfer", u.Username, true, "from "+rc.user.Username)
	return ok(w, map[string]bool{"ok": true})
}
