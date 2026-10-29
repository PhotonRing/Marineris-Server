package api

import (
	"context"
	"embed"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

// Passkeys that sign in to everything (apps, portal, UI) are Authelia's: a
// passkey belongs to the host it is created on, and Authelia's is the
// portal's. The UI adds them without Authelia's pages (which want a one-time
// code sent by e-mail) through a small Marineris page served on the portal's
// host (caddy.PortalPasskeyPath):
//
//  1. the UI (step-up) asks for a ticket: one passkey, one user, one portal
//     host, a few minutes;
//  2. the browser opens the page on the portal's host, with the ticket in the
//     URL fragment (never sent to a server, nor logged);
//  3. the page runs the WebAuthn ceremony with Marineris, which verifies the
//     passkey and registers it in Authelia, then returns to the UI.

// AutheliaPasskeyStore manages the passkeys kept by Authelia.
type AutheliaPasskeyStore interface {
	AutheliaPasskeys(ctx context.Context, username string) ([]configmgr.AutheliaPasskey, error)
	AutheliaUserHandle(ctx context.Context, rpid, username string) (string, error)
	AddAutheliaPasskey(ctx context.Context, p configmgr.NewAutheliaPasskey) error
	DeleteAutheliaPasskey(ctx context.Context, username string, id int64) error
	// DeleteAutheliaUser removes everything Authelia keeps for a deleted
	// User (passkeys, authenticator).
	DeleteAutheliaUser(ctx context.Context, username string) error
	// DeleteAutheliaSecondFactors removes a User's authenticator and
	// passkeys from Authelia (a second-factor Reset link).
	DeleteAutheliaSecondFactors(ctx context.Context, username string) error
}

const portalTicketTTL = 5 * time.Minute

// ticketHeader carries the ticket: kept out of URLs, so out of access logs.
const ticketHeader = "X-Marineris-Ticket"

type portalTicket struct {
	userID    int64
	username  string
	rpid      string // the portal's host
	name      string
	handle    string // the user's WebAuthn ID in Authelia
	returnURL string
	ceremony  string
	expires   time.Time
}

//go:embed portalpage
var portalPage embed.FS

// portalPageHandler serves a page on the portal's host under prefix (edge
// listener only): the page adding a passkey, or the Invite and Reset link
// page (links_page.go).
func portalPageHandler(prefix string, files map[string]string) http.Handler {
	types := map[string]string{".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := files[strings.TrimPrefix(r.URL.Path, prefix)]
		if !ok || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		b, err := portalPage.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", types[name[strings.LastIndex(name, "."):]])
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
}

// portalHosts lists the hosts of the Authelia portal, one per domain.
func (s *Server) portalHosts(ctx context.Context, st store.Settings) (map[string]bool, error) {
	domains, err := s.Store.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, d := range domains {
		out[st.AuthSubdomain+"."+d.Name] = true
	}
	return out, nil
}

// checkPortalOrigin requires requests of the page to come from a portal
// host; for the link page, from the portal of the UI's domain only, the one
// Caddy serves it on.
func (s *Server) checkPortalOrigin(ctx context.Context, settings store.Settings, r *http.Request) *Error {
	bad := &Error{Status: http.StatusForbidden, Code: "bad_origin", Message: "cross-origin request refused"}
	u, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return bad
	}
	var hosts map[string]bool
	if strings.HasPrefix(r.URL.Path, caddy.PortalLinkPath+"/") {
		var host string
		host, err = s.linkPortalHost(ctx, settings)
		hosts = map[string]bool{host: host != ""}
	} else {
		hosts, err = s.portalHosts(ctx, settings)
	}
	if err != nil || !hosts[u.Host] {
		return bad
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return bad
	}
	return nil
}

func (s *Server) handleAutheliaPasskeyList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	list, err := s.AutheliaPasskeys.AutheliaPasskeys(r.Context(), rc.user.Username)
	if err != nil {
		return err
	}
	return ok(w, list)
}

// handleAutheliaPasskeyAdd returns the address of the page that adds the
// passkey, with its ticket.
func (s *Server) handleAutheliaPasskeyAdd(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 || len(name) > 64 {
		return fieldError("name", "between 1 and 64 characters")
	}
	portal := ""
	if rc.listener == Edge {
		portal = s.portalURL(r.Context(), rc.settings)
	}
	if portal == "" {
		return &Error{Status: http.StatusConflict, Code: "no_portal",
			Message: "passkeys for Authelia are added on the public address, when Authelia protects it"}
	}
	rpid := strings.TrimPrefix(portal, "https://")
	ctx := r.Context()
	existing, err := s.AutheliaPasskeys.AutheliaPasskeys(ctx, rc.user.Username)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Host == rpid && strings.EqualFold(p.Name, name) {
			return fieldError("name", "you already have a passkey with this name")
		}
	}
	handle, err := s.AutheliaPasskeys.AutheliaUserHandle(ctx, rpid, rc.user.Username)
	if errors.Is(err, configmgr.ErrAutheliaNotReady) {
		return &Error{Status: http.StatusServiceUnavailable, Code: "authelia_not_ready", Message: "Authelia has not started yet"}
	}
	if err != nil {
		return err
	}
	id := secrets.Token(32)
	t := &portalTicket{
		userID: rc.user.ID, username: rc.user.Username, rpid: rpid, name: name, handle: handle,
		returnURL: "https://" + rc.settings.UIHost + "/account?passkey=added",
		expires:   time.Now().Add(portalTicketTTL),
	}
	s.ticketMu.Lock()
	for k, v := range s.tickets {
		if time.Now().After(v.expires) {
			delete(s.tickets, k)
		}
	}
	if len(s.tickets) >= 1000 {
		s.ticketMu.Unlock()
		return &Error{Status: http.StatusTooManyRequests, Code: "busy", Message: "too many passkeys being added; try again in a few minutes"}
	}
	s.tickets[id] = t
	s.ticketMu.Unlock()
	return ok(w, map[string]string{"url": portal + caddy.PortalPasskeyPath + "/#" + id})
}

func (s *Server) handleAutheliaPasskeyDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	if err := s.AutheliaPasskeys.DeleteAutheliaPasskey(r.Context(), rc.user.Username, id); err != nil {
		return err
	}
	s.audit(r, rc, "passkey.delete", r.PathValue("id"), true, "authelia")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

var errTicket = &Error{Status: http.StatusForbidden, Code: "invalid_ticket",
	Message: "this link has expired or was already used: add the passkey again from your account page"}

// ticket returns the request's ticket, valid for its origin. take removes it.
func (s *Server) ticket(r *http.Request, take bool) (*portalTicket, auth.RP, error) {
	id := r.Header.Get(ticketHeader)
	s.ticketMu.Lock()
	t, found := s.tickets[id]
	if found && take {
		delete(s.tickets, id)
	}
	s.ticketMu.Unlock()
	if id == "" || !found || time.Now().After(t.expires) {
		return nil, auth.RP{}, errTicket
	}
	rp := auth.RP{ID: t.rpid, Origin: "https://" + t.rpid}
	if r.Header.Get("Origin") != rp.Origin {
		return nil, auth.RP{}, errTicket
	}
	return t, rp, nil
}

func (s *Server) handlePortalPasskeyBegin(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	t, rp, err := s.ticket(r, false)
	if err != nil {
		return err
	}
	ctx := r.Context()
	existing, err := s.AutheliaPasskeys.AutheliaPasskeys(ctx, t.username)
	if err != nil {
		return err
	}
	var exclude [][]byte
	for _, p := range existing {
		if p.Host == t.rpid {
			if kid, err := decodeKID(p.KID); err == nil {
				exclude = append(exclude, kid)
			}
		}
	}
	id, opts, err := s.Passkeys.BeginExternalRegistration(rp, t.username, []byte(t.handle), exclude)
	if err != nil {
		return err
	}
	s.ticketMu.Lock()
	t.ceremony = id
	s.ticketMu.Unlock()
	return ok(w, map[string]any{"options": opts, "username": t.username, "name": t.name, "return_url": t.returnURL})
}

func (s *Server) handlePortalPasskeyFinish(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	t, rp, err := s.ticket(r, true)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	u, err := s.Store.GetUser(r.Context(), t.userID)
	if err != nil {
		return err
	}
	rc.user = u
	s.ticketMu.Lock()
	ceremony := t.ceremony
	s.ticketMu.Unlock()
	cred, discoverable, err := s.Passkeys.FinishExternalRegistration(rp, t.username, []byte(t.handle), ceremony, body)
	if err != nil {
		s.audit(r, rc, "passkey.register", t.name, false, "authelia")
		if errors.Is(err, auth.ErrPasskey) {
			return &Error{Status: http.StatusBadRequest, Code: "invalid_passkey", Message: "the passkey could not be registered"}
		}
		return err
	}
	if err := s.AutheliaPasskeys.AddAutheliaPasskey(r.Context(), configmgr.NewAutheliaPasskey{
		RPID: t.rpid, Username: t.username, UserHandle: t.handle, Name: t.name,
		Credential: cred, Discoverable: discoverable,
	}); err != nil {
		s.audit(r, rc, "passkey.register", t.name, false, "authelia")
		return err
	}
	s.audit(r, rc, "passkey.register", t.name, true, "authelia")
	return ok(w, map[string]string{"return_url": t.returnURL})
}

// decodeKID decodes a credential ID as Authelia stores it.
func decodeKID(kid string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(kid)
}
