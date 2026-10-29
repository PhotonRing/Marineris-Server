package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// Invites and Reset links, the Administrator's side: create one (its link is
// shown once), list the pending ones, revoke one. The person it is for opens
// the link on the portal's host (links_page.go).

// linkView is a link as the Users page shows it; URL only when created.
type linkView struct {
	*store.Link
	URL string `json:"url,omitempty"`
}

// linkPortal returns the address of the Authelia portal of the UI's domain,
// where links open, or "" when the UI is on no managed domain.
func (s *Server) linkPortal(ctx context.Context, st store.Settings) (string, error) {
	if st.UIHost == "" {
		return "", nil
	}
	domains, err := s.Store.ListDomains(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range domains {
		if validate.HostInZone(st.UIHost, d.Name) {
			return "https://" + st.AuthSubdomain + "." + d.Name, nil
		}
	}
	return "", nil
}

var errNoPortal = &Error{Status: http.StatusConflict, Code: "no_portal",
	Message: "links open on the Authelia portal of the UI's domain: add the UI's domain first"}

// newLinkToken returns a 256-bit random token and the hash that is stored.
func newLinkToken() (token, hash string) {
	token = secrets.Token(32)
	return token, auth.HashToken(token)
}

func (s *Server) handleInviteCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Email       string   `json:"email"`
		Groups      []string `json:"groups"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	username, err := cleanUsername(req.Username)
	if err != nil {
		return fieldError("username", err.Error())
	}
	display := username
	if strings.TrimSpace(req.DisplayName) != "" {
		if display, err = cleanDisplayName(req.DisplayName); err != nil {
			return fieldError("display_name", err.Error())
		}
	}
	email, err := cleanEmail(req.Email)
	if err != nil {
		return fieldError("email", err.Error())
	}
	for i, g := range req.Groups {
		req.Groups[i] = strings.ToLower(strings.TrimSpace(g))
	}
	ids, builtin, err := s.Store.GroupIDs(ctx, req.Groups)
	if errors.Is(err, store.ErrNotFound) {
		return fieldError("groups", "no such Group")
	} else if err != nil {
		return err
	}
	if builtin {
		// Invites carry custom Groups only; the Owner makes Administrators.
		s.audit(r, rc, "invite.create", username, false, errBuiltinGroups.Code)
		return errBuiltinGroups
	}
	portal, err := s.linkPortal(ctx, rc.settings)
	if err != nil {
		return err
	}
	if portal == "" {
		return errNoPortal
	}
	token, hash := newLinkToken()
	l := &store.Link{Username: username, DisplayName: display, Email: email, GroupIDs: ids, CreatedBy: rc.actor()}
	switch err := s.Store.CreateInvite(ctx, l, hash); {
	case errors.Is(err, store.ErrUsernameTaken):
		return fieldError("username", "is already taken")
	case errors.Is(err, store.ErrUsernameReserved):
		return fieldError("username", "is reserved by a pending Invite")
	case err != nil:
		return err
	}
	l, err = s.Store.GetPendingLink(ctx, l.ID)
	if err != nil {
		return err
	}
	s.audit(r, rc, "invite.create", username, true, strings.Join(l.Groups, ","))
	return ok(w, linkView{Link: l, URL: portal + caddy.PortalLinkPath + "/#" + token})
}

func (s *Server) handleLinkList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	links, err := s.Store.PendingLinks(r.Context())
	if err != nil {
		return err
	}
	out := make([]linkView, 0, len(links))
	for _, l := range links {
		out = append(out, linkView{Link: l})
	}
	return ok(w, out)
}

func (s *Server) handleLinkRevoke(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	l, err := s.Store.GetPendingLink(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.RevokeLink(ctx, l.ID); err != nil {
		return err
	}
	s.audit(r, rc, l.Kind+".revoke", l.Username, true, "")
	return ok(w, map[string]bool{"ok": true})
}
