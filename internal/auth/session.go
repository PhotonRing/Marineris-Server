package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

// SessionCookie is the name of the session cookie. The __Host- prefix makes
// browsers refuse it unless it is Secure, host-only and scoped to "/".
const SessionCookie = "__Host-marineris_session"

// ErrNoSession is returned when a request carries no valid session.
var ErrNoSession = errors.New("auth: no valid session")

// Sessions manages admin sessions.
type Sessions struct {
	store *store.Store
	idle  time.Duration
	max   time.Duration
	now   func() time.Time
}

// NewSessions returns a session manager.
func NewSessions(st *store.Store, idle, max time.Duration) *Sessions {
	return &Sessions{store: st, idle: idle, max: max, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the time source, for tests.
func (s *Sessions) SetClock(now func() time.Time) { s.now = now }

// HashToken returns the stored form of a session token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MethodAuthelia is the method of a session opened from the Authelia
// sign-in (see the api package): it never counts as a recent confirmation,
// so sensitive actions still ask for a step-up.
const MethodAuthelia = "authelia"

// Create starts a session for userID, sets the cookie and returns the
// session (with its CSRF token).
func (s *Sessions) Create(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64, method string) (*store.Session, error) {
	token := secrets.Token(32)
	now := s.now()
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	se := &store.Session{
		IDHash:     HashToken(token),
		UserID:     userID,
		CSRFToken:  secrets.Token(32),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.max),
		AuthAt:     now,
		IP:         ClientIP(r),
		UserAgent:  ua,
		Method:     method,
	}
	if method == MethodAuthelia {
		se.AuthAt = time.Time{}
	}
	if err := s.store.CreateSession(ctx, se); err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.max.Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return se, nil
}

// Load returns the session and user of the request.
func (s *Sessions) Load(ctx context.Context, r *http.Request) (*store.Session, *store.User, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || len(c.Value) < 32 || len(c.Value) > 128 {
		return nil, nil, ErrNoSession
	}
	se, err := s.store.GetSession(ctx, HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrNoSession
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	if !now.Before(se.ExpiresAt) || now.Sub(se.LastSeenAt) >= s.idle {
		_ = s.store.DeleteSession(ctx, se.IDHash)
		return nil, nil, ErrNoSession
	}
	u, err := s.store.GetUser(ctx, se.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrNoSession
	}
	if err != nil {
		return nil, nil, err
	}
	if now.Sub(se.LastSeenAt) > 30*time.Second {
		if err := s.store.TouchSession(ctx, se.IDHash, now); err != nil {
			return nil, nil, err
		}
		se.LastSeenAt = now
	}
	return se, u, nil
}

// Destroy ends the request's session and clears the cookie.
func (s *Sessions) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	ClearCookie(w)
	if c, err := r.Cookie(SessionCookie); err == nil {
		return s.store.DeleteSession(ctx, HashToken(c.Value))
	}
	return nil
}

// ClearCookie expires the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// RecentlyAuthenticated reports whether the session proved credentials
// within window (used to gate sensitive actions behind a step-up).
func (s *Sessions) RecentlyAuthenticated(se *store.Session, window time.Duration) bool {
	return s.now().Sub(se.AuthAt) < window
}

// MarkAuth records a successful step-up for the session.
func (s *Sessions) MarkAuth(ctx context.Context, se *store.Session) error {
	now := s.now()
	if err := s.store.MarkSessionAuth(ctx, se.IDHash, now); err != nil {
		return err
	}
	se.AuthAt = now
	return nil
}

type ctxKey int

const clientIPKey ctxKey = 1

// WithClientIP stores the resolved client IP in the request context.
func WithClientIP(r *http.Request, ip string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), clientIPKey, ip))
}

// ClientIP returns the client IP resolved by the API middleware, or the
// connection's remote address.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey).(string); ok {
		return ip
	}
	return r.RemoteAddr
}
