// Package setup implements the first-run setup that creates the initial
// administrator.
//
// Until setup completes, the admin account does not exist and the public
// listener serves nothing but a "setup pending" notice. Setup is only
// reachable on the loopback-only local listener AND requires a one-time
// setup token that is written to the data directory and printed in the
// container logs, so only someone with access to the host can claim the
// instance.
package setup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// Errors returned by the setup service.
var (
	ErrAlreadyComplete = errors.New("setup: already complete")
	ErrBadToken        = errors.New("setup: invalid setup token")
	ErrNoPending       = errors.New("setup: start setup first")
	ErrBadTOTP         = errors.New("setup: invalid authenticator code")
)

// UsernameRE restricts admin usernames to a conservative character set that
// is safe in Authelia's users file, logs and URLs.
var UsernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// Service performs first-run setup.
type Service struct {
	store     *store.Store
	box       *secrets.Box
	tokenPath string
	now       func() time.Time

	mu      sync.Mutex
	pending *pending
	// attempts counts bad setup tokens; the token is rotated after too many.
	attempts int
}

type pending struct {
	secret  string
	expires time.Time
}

// New returns a setup service.
func New(st *store.Store, box *secrets.Box, tokenPath string) *Service {
	return &Service{store: st, box: box, tokenPath: tokenPath, now: time.Now}
}

// Complete reports whether setup has been completed.
func (s *Service) Complete(ctx context.Context) (bool, error) {
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return st.SetupComplete, nil
}

// EnsureToken creates the setup token file when setup is pending and returns
// the token, or "" when setup is already complete.
func (s *Service) EnsureToken(ctx context.Context) (string, error) {
	done, err := s.Complete(ctx)
	if err != nil {
		return "", err
	}
	if done {
		_ = os.Remove(s.tokenPath)
		return "", nil
	}
	b, err := secrets.ReadSecretFile(s.tokenPath)
	if err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b)), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return s.rotateToken()
}

func (s *Service) rotateToken() (string, error) {
	tok := secrets.Token(32)
	if err := secrets.WriteSecretFile(s.tokenPath, []byte(tok+"\n"), false); err != nil {
		return "", err
	}
	return tok, nil
}

func (s *Service) checkToken(token string) error {
	b, err := secrets.ReadSecretFile(s.tokenPath)
	if err != nil {
		return ErrBadToken
	}
	want := strings.TrimSpace(string(b))
	if want == "" || !secrets.Equal(want, strings.TrimSpace(token)) {
		s.attempts++
		if s.attempts >= 10 {
			// Invalidate the token after repeated guesses; the admin
			// reads the new one from the logs or the data directory.
			s.attempts = 0
			_, _ = s.rotateToken()
		}
		return ErrBadToken
	}
	return nil
}

// Begin validates the setup token and returns a fresh TOTP secret to enrol.
func (s *Service) Begin(ctx context.Context, token, username string) (secret, uri string, err error) {
	if done, err := s.Complete(ctx); err != nil {
		return "", "", err
	} else if done {
		return "", "", ErrAlreadyComplete
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkToken(token); err != nil {
		return "", "", err
	}
	if username == "" {
		username = "admin"
	}
	secret = auth.NewTOTPSecret()
	s.pending = &pending{secret: secret, expires: s.now().Add(30 * time.Minute)}
	return secret, auth.TOTPURI("Marineris", username, secret), nil
}

// Request is the input of Finish.
type Request struct {
	Token     string `json:"setup_token"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	TOTPCode  string `json:"totp_code"`
	ACMEEmail string `json:"acme_email"`
	UIHost    string `json:"ui_host"`
}

// Result is returned once setup completes.
type Result struct {
	User          *store.User
	RecoveryCodes []string
}

// FieldError reports an invalid input field.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

// Finish validates the request, creates the administrator and marks setup
// complete. It can succeed only once.
func (s *Service) Finish(ctx context.Context, req Request) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, err := s.Complete(ctx); err != nil {
		return nil, err
	} else if done {
		return nil, ErrAlreadyComplete
	}
	if err := s.checkToken(req.Token); err != nil {
		return nil, err
	}
	if s.pending == nil || s.now().After(s.pending.expires) {
		return nil, ErrNoPending
	}
	username := strings.TrimSpace(req.Username)
	if !UsernameRE.MatchString(username) {
		return nil, &FieldError{"username", "3-32 characters: lowercase letters, digits, '.', '_' or '-'"}
	}
	if err := auth.CheckPasswordPolicy(username, req.Password); err != nil {
		return nil, &FieldError{"password", err.Error()}
	}
	email := strings.TrimSpace(req.ACMEEmail)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return nil, &FieldError{"acme_email", "must be a valid e-mail address"}
	}
	uiHost := strings.ToLower(strings.TrimSpace(req.UIHost))
	if uiHost != "" {
		if err := validate.Hostname(uiHost); err != nil {
			return nil, &FieldError{"ui_host", err.Error()}
		}
	}
	step, ok := auth.VerifyTOTP(s.pending.secret, req.TOTPCode, s.now())
	if !ok {
		return nil, ErrBadTOTP
	}

	hash, err := auth.HashPassword(ctx, req.Password)
	if err != nil {
		return nil, err
	}
	handle := make([]byte, 64)
	if _, err := rand.Read(handle); err != nil {
		return nil, err
	}
	sealed, err := s.box.Seal([]byte(s.pending.secret), TOTPContext(handle))
	if err != nil {
		return nil, err
	}
	codes, hashes := auth.NewRecoveryCodes()
	u := &store.User{
		Username:      username,
		PasswordHash:  hash,
		TOTPSecret:    sealed,
		TOTPLastStep:  step,
		RecoveryCodes: hashes,
		WebAuthnID:    handle,
	}
	// The first user is the Owner and an Administrator.
	if err := s.store.CreateOwner(ctx, u); errors.Is(err, store.ErrConflict) {
		return nil, ErrAlreadyComplete
	} else if err != nil {
		return nil, err
	}
	if _, err := s.store.UpdateSettings(ctx, func(st *store.Settings) error {
		st.SetupComplete = true
		st.ACMEEmail = email
		st.UIHost = uiHost
		return nil
	}); err != nil {
		return nil, fmt.Errorf("setup: saving settings: %w", err)
	}
	s.pending = nil
	_ = os.Remove(s.tokenPath)
	return &Result{User: u, RecoveryCodes: codes}, nil
}

// TOTPContext is the encryption context of a user's sealed TOTP secret.
func TOTPContext(webauthnID []byte) string {
	return fmt.Sprintf("user-totp:%x", webauthnID)
}
