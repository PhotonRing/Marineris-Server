package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/store"
)

func TestHashAndVerify(t *testing.T) {
	ctx := context.Background()
	h, err := HashPassword(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, err := VerifyPassword(ctx, h, "correct horse battery"); !ok || err != nil {
		t.Fatal("valid password rejected")
	}
	if ok, _ := VerifyPassword(ctx, h, "correct horse batterY"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword(ctx, "correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestVerifyRejectsMalformedAndDangerousHashes(t *testing.T) {
	ctx := context.Background()
	for _, h := range []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=65536,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=16$m=65536,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=19$m=99999999,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=19$m=65536,t=9999,p=4$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=19$m=65536,t=3,p=0$c29tZXNhbHRzb21lc2FsdA$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=19$m=65536,t=3,p=4$!!$Uq9eMkL8ySGvcnUzODJxqPf9hD2gQ3XaXbF1pLMa8jI",
		"$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHRzb21lc2FsdA$",
	} {
		if ok, err := VerifyPassword(ctx, h, "x"); ok || err == nil {
			t.Errorf("hash %q: ok=%v err=%v", h, ok, err)
		}
	}
}

func TestDummyHashIsValid(t *testing.T) {
	if _, err := parsePHC(dummyHash); err != nil {
		t.Fatalf("dummy hash must parse so timing matches real checks: %v", err)
	}
}

func FuzzVerifyPassword(f *testing.F) {
	f.Add(dummyHash, "pw")
	f.Add("$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA", "x")
	f.Fuzz(func(t *testing.T, hash, pw string) {
		p, err := parsePHC(hash)
		if err != nil {
			return
		}
		// Parsed parameters must always be inside the safe bounds.
		if p.memory > maxArgonMemory || p.time > maxArgonTime || p.threads == 0 {
			t.Fatalf("unsafe params accepted: %+v", p)
		}
	})
}

func TestPasswordPolicy(t *testing.T) {
	cases := map[string]bool{
		"short":                       false,
		"aaaaaaaaaaaaaaaa":            false,
		"password1234":                false,
		"admin-is-my-name-ok":         false, // contains username
		"Tr0ub4dor&3-ochre-dust":      true,
		strings.Repeat("ab12cd", 50):  false, // too long
		"red planet canyon 7":         true,
		"\xff\xfe invalid utf8 chars": false,
	}
	for pw, want := range cases {
		err := CheckPasswordPolicy("admin", pw)
		if (err == nil) != want {
			t.Errorf("%q: got err=%v, want ok=%v", pw, err, want)
		}
	}
}

// RFC 6238 Appendix B vectors (SHA-1), truncated to 6 digits.
func TestTOTPRFCVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	vectors := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	}
	for ts, want := range vectors {
		got, err := TOTPCode(secret, ts/30)
		if err != nil || got != want {
			t.Errorf("t=%d: got %s want %s (%v)", ts, got, want, err)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	step := TOTPStep(now)
	for d, want := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code, _ := TOTPCode(secret, step+d)
		got, ok := VerifyTOTP(secret, code, now)
		if ok != want {
			t.Errorf("offset %d: ok=%v want %v", d, ok, want)
		}
		if ok && got != step+d {
			t.Errorf("offset %d: matched step %d", d, got)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12 34 5a"} {
		if _, ok := VerifyTOTP(secret, bad, now); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	code, _ := TOTPCode(secret, step)
	if _, ok := VerifyTOTP(secret, code[:3]+" "+code[3:], now); !ok {
		t.Error("spaces inside the code should be tolerated")
	}
}

func TestTOTPURI(t *testing.T) {
	u := TOTPURI("Marineris", "admin", "ABC")
	if !strings.HasPrefix(u, "otpauth://totp/Marineris:admin?") || !strings.Contains(u, "secret=ABC") {
		t.Fatal(u)
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := NewRecoveryCodes()
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatal("wrong count")
	}
	seen := map[string]bool{}
	for i, c := range codes {
		if seen[c] {
			t.Fatal("duplicate code")
		}
		seen[c] = true
		if HashRecoveryCode(strings.ToUpper(c)) != hashes[i] || HashRecoveryCode(strings.ReplaceAll(c, "-", "")) != hashes[i] {
			t.Fatal("normalisation broken")
		}
		if strings.ContainsAny(c, "0O1lI") {
			t.Fatalf("ambiguous character in %q", c)
		}
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle()
	now := time.Unix(0, 0)
	th.SetClock(func() time.Time { return now })
	for i := 0; i < 5; i++ {
		if ok, _ := th.Allowed("ip:1"); !ok {
			t.Fatalf("locked after %d failures", i)
		}
		th.Fail("ip:1")
	}
	ok, wait := th.Allowed("ip:1")
	if !ok || wait != 0 {
		t.Fatal("5 failures must still be allowed")
	}
	th.Fail("ip:1")
	ok, wait = th.Allowed("ip:1", "user:x")
	if ok || wait != time.Minute {
		t.Fatalf("expected 1m lock, got ok=%v wait=%v", ok, wait)
	}
	now = now.Add(time.Minute)
	th.Fail("ip:1")
	if _, wait = th.Allowed("ip:1"); wait != 2*time.Minute {
		t.Fatalf("expected backoff doubling, got %v", wait)
	}
	for i := 0; i < 20; i++ {
		th.Fail("ip:1")
	}
	if _, wait = th.Allowed("ip:1"); wait != time.Hour {
		t.Fatalf("expected cap at 1h, got %v", wait)
	}
	if ok, _ := th.Allowed("ip:2"); !ok {
		t.Fatal("other keys must not be affected")
	}
	th.Succeed("ip:1")
	if ok, _ := th.Allowed("ip:1"); !ok {
		t.Fatal("success must clear")
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u := &store.User{Username: "a", PasswordHash: "h", WebAuthnID: []byte("h")}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sm := NewSessions(st, 30*time.Minute, 12*time.Hour)
	sm.SetClock(func() time.Time { return now })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	se, err := sm.Create(ctx, rec, req, u.ID, "password+totp")
	if err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != SessionCookie || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("insecure cookie: %+v", cookie)
	}
	if se.IDHash == cookie.Value || strings.Contains(se.IDHash, cookie.Value) {
		t.Fatal("raw token stored")
	}

	authed := func() error {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(cookie)
		_, _, err := sm.Load(ctx, r)
		return err
	}
	if err := authed(); err != nil {
		t.Fatal(err)
	}
	// Idle timeout.
	now = now.Add(31 * time.Minute)
	if err := authed(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session accepted: %v", err)
	}
	// Forged / malformed cookies.
	for _, v := range []string{"", "short", strings.Repeat("a", 43), strings.Repeat("x", 500)} {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: v})
		if _, _, err := sm.Load(ctx, r); !errors.Is(err, ErrNoSession) {
			t.Fatalf("cookie %q accepted", v)
		}
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	st, _ := store.OpenMemory(ctx)
	defer st.Close()
	u := &store.User{Username: "a", PasswordHash: "h", WebAuthnID: []byte("h")}
	_ = st.CreateUser(ctx, u)
	now := time.Now().UTC()
	sm := NewSessions(st, 30*time.Minute, time.Hour)
	sm.SetClock(func() time.Time { return now })
	rec := httptest.NewRecorder()
	if _, err := sm.Create(ctx, rec, httptest.NewRequest("POST", "/", nil), u.ID, "x"); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	// Keep the session active, but past its absolute lifetime.
	for i := 0; i < 5; i++ {
		now = now.Add(15 * time.Minute)
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(cookie)
		_, _, err := sm.Load(ctx, r)
		if i < 3 && err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if i >= 3 && !errors.Is(err, ErrNoSession) {
			t.Fatalf("step %d: session outlived its maximum lifetime", i)
		}
	}
}

func TestPasskeyCeremoniesAreSingleUseAndTyped(t *testing.T) {
	p := NewPasskeys(nil, "Marineris")
	rp := RP{ID: "localhost", Origin: "http://localhost:8081"}
	id, _, err := p.BeginLogin(rp)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.take(id, "register"); ok {
		t.Fatal("login ceremony accepted as registration")
	}
	if _, ok := p.take(id, "login"); ok {
		t.Fatal("ceremony must be consumed by the first take, even of the wrong kind")
	}
	id, _, _ = p.BeginLogin(rp)
	now := time.Now()
	p.now = func() time.Time { return now.Add(ceremonyTTL + time.Second) }
	if _, ok := p.take(id, "login"); ok {
		t.Fatal("expired ceremony accepted")
	}
}
