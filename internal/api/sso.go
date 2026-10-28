package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/store"
)

// errNoAccount: Authelia vouched for a user that Marineris does not know.
var errNoAccount = &Error{Status: http.StatusForbidden, Code: "no_account",
	Message: "you are signed in to Authelia, but have no Marineris account"}

// autheliaSession ties the UI session to the Authelia sign-in, when Authelia
// protects the UI: signing in once, on the portal or on any route, opens the
// UI too. Caddy only passes requests that Authelia allowed, and sets
// Remote-User from Authelia's answer, never from the client (it deletes the
// client's copy on the UI route whether or not Authelia protects it).
//
// se, u and err are the result of loading the request's own session.
func (s *Server) autheliaSession(w http.ResponseWriter, r *http.Request, rc *reqCtx, se *store.Session, u *store.User, err error) (*store.Session, *store.User, error) {
	if err != nil && !errors.Is(err, auth.ErrNoSession) {
		return nil, nil, err
	}
	name := strings.ToLower(strings.TrimSpace(r.Header.Get("Remote-User")))
	if name == "" {
		// Authelia vouched for nobody: no session, whatever the cookie says.
		return nil, nil, auth.ErrNoSession
	}
	if se != nil && u.Username == name {
		return se, u, nil
	}
	ctx := r.Context()
	if se != nil {
		// Another user signed in to Authelia: the old session ends.
		if err := s.Sessions.Destroy(ctx, w, r); err != nil {
			return nil, nil, err
		}
	}
	nu, err := s.Store.GetUserByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, errNoAccount
	}
	if err != nil {
		return nil, nil, err
	}
	ns, err := s.Sessions.Create(ctx, w, r, nu.ID, auth.MethodAuthelia)
	if err != nil {
		return nil, nil, err
	}
	rc.user = nu
	s.audit(r, rc, "login", nu.Username, true, auth.MethodAuthelia)
	return ns, nu, nil
}

// portalURL returns the address of the Authelia portal for the UI host
// ("" when the UI is not protected by Authelia or has no host).
func (s *Server) portalURL(ctx context.Context, st store.Settings) string {
	if !st.UIProtectWithAuthelia || st.UIHost == "" {
		return ""
	}
	domains, err := s.Store.ListDomains(ctx)
	if err != nil {
		return ""
	}
	for _, d := range domains {
		if st.UIHost == d.Name || strings.HasSuffix(st.UIHost, "."+d.Name) {
			return "https://" + st.AuthSubdomain + "." + d.Name
		}
	}
	return ""
}
