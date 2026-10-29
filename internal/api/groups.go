package api

import (
	"net/http"

	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// The Groups page: Administrators create, rename and delete custom Groups
// and change their members. The built-in admins and proxy-admins are only
// listed here (errBuiltinGroups): their membership is the Owner's. Who may
// be put in or taken out of a Group, and so who may rename or delete one, is
// mayActOn's decision (people.go).
// Every change re-renders Authelia's users file, which Authelia re-reads
// without a restart.

func (s *Server) handleGroupList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	groups, err := s.Store.ListGroups(r.Context())
	if err != nil {
		return err
	}
	if groups == nil {
		groups = []*store.Group{}
	}
	return ok(w, groups)
}

// groupNameBody reads {"name": ...} and validates it as a custom Group name.
func groupNameBody(r *http.Request) (string, error) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return "", err
	}
	if err := validate.GroupName(req.Name); err != nil {
		return "", fieldError("name", err.Error())
	}
	return req.Name, nil
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	name, err := groupNameBody(r)
	if err != nil {
		return err
	}
	if err := s.Store.CreateGroup(r.Context(), name); err != nil {
		return err
	}
	s.audit(r, rc, "group.create", name, true, "")
	g, err := s.Store.GetGroup(r.Context(), name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, g)
	return nil
}

// customGroup loads the Group named by the {name} path value and refuses a
// built-in one (audited as action).
func (s *Server) customGroup(r *http.Request, rc *reqCtx, action string) (*store.Group, error) {
	g, err := s.Store.GetGroup(r.Context(), r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if g.Builtin {
		return nil, s.auditRefusal(r, rc, action, g.Name, "", errBuiltinGroups)
	}
	return g, nil
}

// wholeGroup is customGroup for renaming or deleting it, which changes the
// Groups of every member: rc.user must be allowed to act on each of them
// (mayActOn), so only the Owner touches a Group holding another
// Administrator or the Owner.
func (s *Server) wholeGroup(r *http.Request, rc *reqCtx, action string) (*store.Group, error) {
	g, err := s.customGroup(r, rc, action)
	if err != nil {
		return nil, err
	}
	for _, m := range g.Members {
		u, err := s.Store.GetUser(r.Context(), m.ID)
		if err != nil {
			return nil, err
		}
		if err := s.checkActOn(r, rc, u, action, g.Name, u.Username+": "); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (s *Server) handleGroupRename(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	name, err := groupNameBody(r)
	if err != nil {
		return err
	}
	g, err := s.wholeGroup(r, rc, "group.rename")
	if err != nil {
		return err
	}
	if err := s.Store.RenameGroup(r.Context(), g.Name, name); err != nil {
		return err
	}
	s.audit(r, rc, "group.rename", name, true, "from "+g.Name)
	s.applyAsync(rc.actor(), "group "+g.Name+" renamed to "+name, true, false)
	g, err = s.Store.GetGroup(r.Context(), name)
	if err != nil {
		return err
	}
	return ok(w, g)
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	g, err := s.wholeGroup(r, rc, "group.delete")
	if err != nil {
		return err
	}
	// Memberships go with the Group; its Users stay.
	if err := s.Store.DeleteGroup(r.Context(), g.Name); err != nil {
		return err
	}
	s.audit(r, rc, "group.delete", g.Name, true, "")
	s.applyAsync(rc.actor(), "group "+g.Name+" deleted", true, false)
	return ok(w, map[string]bool{"ok": true})
}

// handleGroupMember adds (add) or removes the User {id} to or from the
// Group {name}. Both are idempotent.
func (s *Server) handleGroupMember(add bool) func(http.ResponseWriter, *http.Request, *reqCtx) error {
	action := "group.member_remove"
	if add {
		action = "group.member_add"
	}
	return func(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
		g, err := s.customGroup(r, rc, action)
		if err != nil {
			return err
		}
		u, err := s.targetUser(r)
		if err != nil {
			return err
		}
		if err := s.checkActOn(r, rc, u, action, g.Name, u.Username+": "); err != nil {
			return err
		}
		if add {
			err = s.Store.AddGroupMember(r.Context(), u.ID, g.Name)
		} else {
			err = s.Store.RemoveGroupMember(r.Context(), u.ID, g.Name)
		}
		if err != nil {
			return err
		}
		s.audit(r, rc, action, g.Name, true, u.Username)
		s.applyAsync(rc.actor(), action+" "+g.Name+" "+u.Username, true, false)
		g, err = s.Store.GetGroup(r.Context(), g.Name)
		if err != nil {
			return err
		}
		return ok(w, g)
	}
}
