package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(bytes.Repeat([]byte{3}, 32))
	tokPath := filepath.Join(t.TempDir(), "secrets", "setup-token")
	return New(st, box, tokPath), st, tokPath
}

func validRequest(t *testing.T, s *Service, token string) Request {
	t.Helper()
	secret, uri, err := s.Begin(context.Background(), token, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, secret) {
		t.Fatal("uri must contain secret")
	}
	code, _ := auth.TOTPCode(secret, auth.TOTPStep(time.Now()))
	return Request{Token: token, Username: "admin", Password: "Valles-Marineris-2026", TOTPCode: code, ACMEEmail: "ops@example.com", UIHost: "marineris.example.com"}
}

func TestSetupHappyPath(t *testing.T) {
	ctx := context.Background()
	s, st, tokPath := newService(t)
	tok, err := s.EnsureToken(ctx)
	if err != nil || len(tok) < 32 {
		t.Fatalf("token %q %v", tok, err)
	}
	if fi, _ := os.Stat(tokPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file perms %o", fi.Mode().Perm())
	}
	tok2, _ := s.EnsureToken(ctx)
	if tok2 != tok {
		t.Fatal("token must be stable until used")
	}
	res, err := s.Finish(ctx, validRequest(t, s, tok))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RecoveryCodes) != auth.RecoveryCodeCount {
		t.Fatal("recovery codes missing")
	}
	if _, err := os.Stat(tokPath); !os.IsNotExist(err) {
		t.Fatal("setup token must be deleted after setup")
	}
	settings, _ := st.GetSettings(ctx)
	if !settings.SetupComplete || settings.ACMEEmail != "ops@example.com" || settings.UIHost != "marineris.example.com" {
		t.Fatalf("%+v", settings)
	}
	u, _ := st.GetUserByName(ctx, "admin")
	if strings.Contains(u.TOTPSecret, "=") || u.TOTPLastStep == 0 {
		t.Fatal("TOTP not sealed or replay counter not initialised")
	}
	if ok, _ := auth.VerifyPassword(ctx, u.PasswordHash, "Valles-Marineris-2026"); !ok {
		t.Fatal("password not stored correctly")
	}
	// Setup can never run twice.
	if _, _, err := s.Begin(ctx, tok, "x"); !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("begin after setup: %v", err)
	}
	if _, err := s.Finish(ctx, Request{Token: tok}); !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("finish after setup: %v", err)
	}
	if tok3, _ := s.EnsureToken(ctx); tok3 != "" {
		t.Fatal("no token may exist after setup")
	}
}

func TestSetupRejectsBadToken(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	tok, _ := s.EnsureToken(ctx)
	for _, bad := range []string{"", "x", tok + "x", strings.ToUpper(tok)} {
		if _, _, err := s.Begin(ctx, bad, "admin"); !errors.Is(err, ErrBadToken) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	req := validRequest(t, s, tok)
	req.Token = "wrong"
	if _, err := s.Finish(ctx, req); !errors.Is(err, ErrBadToken) {
		t.Fatal(err)
	}
}

func TestSetupTokenRotatesAfterGuessing(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	tok, _ := s.EnsureToken(ctx)
	for i := 0; i < 10; i++ {
		_, _, _ = s.Begin(ctx, "guess", "admin")
	}
	if _, _, err := s.Begin(ctx, tok, "admin"); !errors.Is(err, ErrBadToken) {
		t.Fatal("token must rotate after repeated failures")
	}
	newTok, _ := s.EnsureToken(ctx)
	if newTok == tok {
		t.Fatal("token unchanged")
	}
	if _, _, err := s.Begin(ctx, newTok, "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestSetupValidation(t *testing.T) {
	ctx := context.Background()
	mut := map[string]func(*Request){
		"username": func(r *Request) { r.Username = "Admin Root" },
		"password": func(r *Request) { r.Password = "short" },
		"pw-user":  func(r *Request) { r.Password = "admin-admin-admin" },
		"email":    func(r *Request) { r.ACMEEmail = "not-an-email" },
		"email2":   func(r *Request) { r.ACMEEmail = "Ops <ops@example.com>" },
		"ui_host":  func(r *Request) { r.UIHost = "http://x" },
		"totp":     func(r *Request) { r.TOTPCode = "000000" },
	}
	for name, m := range mut {
		s, st, _ := newService(t)
		tok, _ := s.EnsureToken(ctx)
		req := validRequest(t, s, tok)
		m(&req)
		if _, err := s.Finish(ctx, req); err == nil {
			t.Errorf("%s: invalid request accepted", name)
		}
		if n, _ := st.CountUsers(ctx); n != 0 {
			t.Errorf("%s: user created despite invalid request", name)
		}
	}
}

func TestSetupRequiresBegin(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t)
	tok, _ := s.EnsureToken(ctx)
	if _, err := s.Finish(ctx, Request{Token: tok, Username: "admin", Password: "Valles-Marineris-2026", ACMEEmail: "a@b.co", TOTPCode: "123456"}); !errors.Is(err, ErrNoPending) {
		t.Fatal(err)
	}
}
