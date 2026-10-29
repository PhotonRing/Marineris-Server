// Package api implements the Marineris HTTP API and serves the web UI.
//
// Two listeners share the same route table:
//
//   - the edge listener, reachable only through Caddy (and usually behind
//     Authelia), which serves nothing but a "setup pending" answer until
//     first-run setup is complete;
//   - the local listener, published on the host's loopback interface only,
//     used for first-run setup and break-glass access over an SSH tunnel.
//
// Every route declares its access level in one table (routes.go); the
// security test suite checks every route of that table.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/authelia"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/certs"
	"github.com/photonring/marineris-server/internal/config"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/icons"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/sysinfo"
)

// Listener names.
const (
	Edge  = "edge"
	Local = "local"
)

// StepUpWindow is how recent a password/TOTP or passkey proof must be for
// sensitive actions.
const StepUpWindow = 10 * time.Minute

// MaxBody bounds request bodies.
const MaxBody = 1 << 20

// DockerAPI is the subset of the Docker client used by the API.
type DockerAPI interface {
	Ping(ctx context.Context) error
	List(ctx context.Context) ([]docker.Container, error)
	Inspect(ctx context.Context, ref string) (*docker.Detail, error)
	Action(ctx context.Context, ref, action string) error
	Remove(ctx context.Context, ref string) error
	Logs(ctx context.Context, ref string, tail int) (string, error)
	ConnectToApps(ctx context.Context, ref string) error
	Networks(ctx context.Context) ([]docker.Network, error)
	Volumes(ctx context.Context) ([]docker.Volume, error)
	RemoveVolume(ctx context.Context, name string) error
}

// StacksAPI is the subset of the stack manager used by the API.
type StacksAPI interface {
	Check(ctx context.Context, name string, composeYAML, env []byte) ([]stacks.Violation, error)
	Deploy(ctx context.Context, name string, composeYAML, env []byte, unsafeAck bool) error
	Recreate(ctx context.Context, name string, composeYAML, env []byte, unsafeAck bool, services []string, pull bool) error
	Down(ctx context.Context, name string, removeVolumes bool) error
	Remove(name string) error
	BindRoots() []string
}

// Applier applies generated configurations.
type Applier interface {
	ApplyCaddy(ctx context.Context, actor, reason string) error
	ApplyAuthelia(ctx context.Context, actor, reason string) error
	RollbackCaddy(ctx context.Context, actor string, versionID int64) error
	RollbackAuthelia(ctx context.Context, actor string, versionID int64) error
	CaddyInput(ctx context.Context) (caddy.Input, error)
	AutheliaInput(ctx context.Context) (authelia.Input, error)
	RenderCaddy(ctx context.Context) ([]byte, error)
	Content(v *store.ConfigVersion) ([]byte, error)
	Redact(ctx context.Context, content []byte) []byte
}

// StatsSource provides host metrics for the hub widgets.
type StatsSource interface {
	Snapshot() sysinfo.Stats
}

// Deps are the dependencies of the API server.
type Deps struct {
	Config   *config.Config
	Store    *store.Store
	Box      *secrets.Box
	Setup    *setup.Service
	Sessions *auth.Sessions
	Passkeys *auth.Passkeys
	// AutheliaPasskeys: the passkeys kept by Authelia.
	AutheliaPasskeys AutheliaPasskeyStore
	Throttle         *auth.Throttle
	Configs          Applier
	Docker           DockerAPI
	Stacks           StacksAPI
	Certs            *certs.Checker
	Icons            *icons.Fetcher
	Stats            StatsSource
	Log              *slog.Logger
	Version          string
	UI               http.Handler
}

// Server is the API server.
type Server struct {
	Deps
	routes []Route

	mfaMu  sync.Mutex
	mfa    map[string]*mfaPending
	totpMu sync.Mutex
	// totpEnrol holds a pending TOTP re-enrolment per user.
	totpEnrol map[int64]*totpPending
	ticketMu  sync.Mutex
	// tickets of the page adding Authelia passkeys (portal_passkeys.go).
	tickets map[string]*portalTicket

	// bg tracks background applies (see applyAsync).
	bg sync.WaitGroup
}

// Wait blocks until background work (configuration applies) has finished.
func (s *Server) Wait() { s.bg.Wait() }

// New creates the API server.
func New(d Deps) *Server {
	s := &Server{Deps: d, mfa: map[string]*mfaPending{}, totpEnrol: map[int64]*totpPending{}, tickets: map[string]*portalTicket{}}
	s.routes = s.table()
	mustValidRoutes(s.routes)
	return s
}

// Routes returns the route table (used by the security tests).
func (s *Server) Routes() []Route { return s.routes }

// Handler returns the HTTP handler for a listener (Edge or Local).
func (s *Server) Handler(listener string) http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes {
		rt := rt
		if (rt.LocalOnly && listener != Local) || (rt.Access == Ticket && listener != Edge) {
			continue
		}
		mux.Handle(rt.Method+" "+rt.Pattern, s.wrap(listener, rt))
	}
	if listener == Edge {
		mux.Handle(caddy.PortalPasskeyPath+"/", portalPageHandler())
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such endpoint"})
	})
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}
	return s.common(listener, mux)
}

// common applies security headers, client IP resolution and body limits to
// every request, API or UI.
func (s *Server) common(listener string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Embedder-Policy", "require-corp")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if listener == Edge {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		r = auth.WithClientIP(r, clientIP(listener, r))
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
		next.ServeHTTP(w, r)
	})
}

// clientIP resolves the client address. On the edge listener the request
// comes from Caddy, which overwrites X-Forwarded-For with the real client
// address (it trusts no upstream proxy), so the last entry is used.
func clientIP(listener string, r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if listener == Edge {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}

// reqCtx carries per-request state to handlers.
type reqCtx struct {
	listener string
	session  *store.Session
	user     *store.User
	settings store.Settings
}

func (rc *reqCtx) actor() string {
	if rc.user != nil {
		return rc.user.Username
	}
	return "anonymous"
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// wrap enforces the route's access level.
func (s *Server) wrap(listener string, rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx := r.Context()
		settings, err := s.Store.GetSettings(ctx)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		rc := &reqCtx{listener: listener, settings: settings}

		// Before setup, the edge listener answers nothing but health/meta.
		if listener == Edge && !settings.SetupComplete && (rt.Access != Public || r.Method != http.MethodGet) {
			writeError(w, &Error{Status: http.StatusServiceUnavailable, Code: "setup_pending",
				Message: "first-run setup has not been completed; complete it through the local listener (SSH tunnel)"})
			return
		}
		if rt.Access == Ticket {
			if err := s.checkPortalOrigin(ctx, settings, r); err != nil {
				writeError(w, err)
				return
			}
		} else if isUnsafe(r.Method) {
			if err := s.checkOrigin(listener, settings, r); err != nil {
				writeError(w, err)
				return
			}
		}
		switch rt.Access {
		case Public, Ticket:
		case SetupOnly:
			if settings.SetupComplete {
				writeError(w, &Error{Status: http.StatusConflict, Code: "setup_complete", Message: "setup has already been completed"})
				return
			}
		case Authed, StepUp:
			se, u, err := s.Sessions.Load(ctx, r)
			if listener == Edge && settings.UIProtectWithAuthelia {
				se, u, err = s.autheliaSession(w, r, rc, se, u, err)
			}
			var apiErr *Error
			if errors.As(err, &apiErr) {
				writeError(w, apiErr)
				return
			}
			if errors.Is(err, auth.ErrNoSession) {
				auth.ClearCookie(w)
				writeError(w, &Error{Status: http.StatusUnauthorized, Code: "unauthenticated", Message: "please log in"})
				return
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if refusal := signInRefused(u); refusal != nil {
				// Sessions end when the User is disabled; one that
				// survived anyway ends now.
				if err := s.Sessions.Destroy(ctx, w, r); err != nil {
					s.fail(w, r, err)
					return
				}
				writeError(w, refusal)
				return
			}
			if isUnsafe(r.Method) && !secrets.Equal(r.Header.Get("X-CSRF-Token"), se.CSRFToken) {
				writeError(w, &Error{Status: http.StatusForbidden, Code: "csrf", Message: "missing or invalid CSRF token"})
				return
			}
			// Checked before step-up: nobody is asked to confirm their
			// identity for something they may not do anyway.
			who := rt.Who
			if listener == Local && who < Administrator {
				// The break-glass listener is for Administrators only.
				who = Administrator
			}
			if allowed, err := s.allowed(ctx, u, who); err != nil {
				s.fail(w, r, err)
				return
			} else if !allowed {
				writeError(w, errNotAllowed)
				return
			}
			if rt.Access == StepUp && !s.Sessions.RecentlyAuthenticated(se, StepUpWindow) {
				writeError(w, &Error{Status: http.StatusForbidden, Code: "reauth_required", Message: "confirm your identity to continue"})
				return
			}
			rc.session, rc.user = se, u
		default:
			writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "route misconfigured"})
			return
		}
		if err := rt.Handler(w, r, rc); err != nil {
			s.fail(w, r, err)
		}
	})
}

// errNotAllowed is the answer to a signed-in User lacking the rights a route
// or a setting needs; the UI recognises it by its code.
var errNotAllowed = &Error{Status: http.StatusForbidden, Code: "not_allowed", Message: "you are not allowed to do this"}

// allowed reports whether the User may call a route needing who.
func (s *Server) allowed(ctx context.Context, u *store.User, who Who) (bool, error) {
	switch who {
	case Anyone, AnyUser:
		return true, nil
	case Administrator:
		return u.IsAdministrator(), nil
	case ProxyAdmin, Owner:
		if who == ProxyAdmin && u.IsProxyAdmin() {
			return true, nil
		}
		owner, err := s.Store.OwnerID(ctx)
		return err == nil && owner == u.ID, err
	}
	return false, nil
}

// checkOrigin requires unsafe requests to come from the UI's own origin.
func (s *Server) checkOrigin(listener string, settings store.Settings, r *http.Request) *Error {
	origin := r.Header.Get("Origin")
	bad := &Error{Status: http.StatusForbidden, Code: "bad_origin", Message: "cross-origin request refused"}
	if origin == "" {
		return bad
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return bad
	}
	switch listener {
	case Edge:
		if u.Scheme != "https" || settings.UIHost == "" || u.Host != settings.UIHost {
			return bad
		}
	case Local:
		host := u.Hostname()
		if u.Scheme != "http" || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
			return bad
		}
		if u.Host != r.Host {
			return bad
		}
	default:
		return bad
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return bad
	}
	return nil
}

// rp returns the WebAuthn relying party for the request.
func (s *Server) rp(rc *reqCtx, r *http.Request) (auth.RP, error) {
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return auth.RP{}, &Error{Status: http.StatusBadRequest, Code: "bad_origin", Message: "missing origin"}
	}
	switch rc.listener {
	case Edge:
		if rc.settings.UIHost != "" && u.Scheme == "https" && u.Host == rc.settings.UIHost {
			return auth.RP{ID: rc.settings.UIHost, Origin: "https://" + rc.settings.UIHost}, nil
		}
	case Local:
		if u.Scheme == "http" && u.Hostname() == "localhost" {
			return auth.RP{ID: "localhost", Origin: "http://" + u.Host}, nil
		}
		return auth.RP{}, &Error{Status: http.StatusBadRequest, Code: "passkey_localhost",
			Message: "passkeys over the SSH tunnel need http://localhost (not an IP address)"}
	}
	return auth.RP{}, &Error{Status: http.StatusBadRequest, Code: "bad_origin", Message: "passkeys are not available on this origin"}
}

// Error is an API error returned to the client.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"error"`
	Details any    `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func badRequest(msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: msg}
}

func fieldError(field, msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid", Message: field + ": " + msg, Details: map[string]string{"field": field}}
}

func writeError(w http.ResponseWriter, e *Error) {
	writeJSON(w, e.Status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

// fail maps internal errors to API errors without leaking internals.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *Error
	var ve *caddy.ValidationError
	var pe *stacks.PolicyError
	var apErr *configmgr.ApplyError
	var mbe *http.MaxBytesError
	var fe *setup.FieldError
	switch {
	case errors.As(err, &ae):
		writeError(w, ae)
	case errors.As(err, &mbe):
		writeError(w, &Error{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "request body too large"})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, &Error{Status: http.StatusNotFound, Code: "not_found", Message: "not found"})
	case errors.Is(err, store.ErrConflict):
		writeError(w, &Error{Status: http.StatusConflict, Code: "conflict", Message: "an item with the same name already exists"})
	case errors.Is(err, store.ErrInUse):
		writeError(w, &Error{Status: http.StatusConflict, Code: "in_use", Message: "still in use"})
	case errors.As(err, &ve):
		writeError(w, &Error{Status: http.StatusBadRequest, Code: "invalid_config", Message: "the change would produce an invalid configuration", Details: ve.Problems})
	case errors.As(err, &pe):
		writeError(w, &Error{Status: http.StatusUnprocessableEntity, Code: "policy", Message: "the stack violates the security policy", Details: pe.Violations})
	case errors.As(err, &apErr):
		writeError(w, &Error{Status: http.StatusBadGateway, Code: "apply_failed", Message: apErr.Error()})
	case errors.As(err, &fe):
		writeError(w, fieldError(fe.Field, fe.Reason))
	case errors.Is(err, docker.ErrProtected):
		writeError(w, &Error{Status: http.StatusForbidden, Code: "protected", Message: err.Error()})
	case errors.Is(err, context.Canceled):
	default:
		s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error"})
	}
}

// decode reads a JSON body strictly: one object, no unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return badRequest("invalid JSON body: " + err.Error())
	}
	if dec.More() {
		return badRequest("invalid JSON body: trailing data")
	}
	if _, err := dec.Token(); err != io.EOF {
		return badRequest("invalid JSON body: trailing data")
	}
	return nil
}

func (s *Server) audit(r *http.Request, rc *reqCtx, action, target string, success bool, details string) {
	actor := "anonymous"
	if rc != nil {
		actor = rc.actor()
	}
	if err := s.Store.Audit(context.WithoutCancel(r.Context()), store.AuditEntry{
		Actor: actor, IP: auth.ClientIP(r), Action: action, Target: target, Success: success, Details: details,
	}); err != nil {
		s.Log.Error("audit write failed", "err", err)
	}
	s.Log.Info("audit", "actor", actor, "ip", auth.ClientIP(r), "action", action, "target", target, "success", success)
}

// requireDocker returns an error when Docker is not configured.
func (s *Server) requireDocker() error {
	if s.Docker == nil {
		return &Error{Status: http.StatusServiceUnavailable, Code: "docker_unavailable", Message: "Docker is not reachable"}
	}
	return nil
}

func idParam(r *http.Request) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(r.PathValue("id"), "%d", &id); err != nil || id <= 0 || fmt.Sprint(id) != r.PathValue("id") {
		return 0, badRequest("invalid id")
	}
	return id, nil
}
