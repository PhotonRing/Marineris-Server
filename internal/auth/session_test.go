package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/store"
)

func TestStepUpWindowAndSignOut(t *testing.T) {
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
	se, err := sm.Create(ctx, rec, httptest.NewRequest("POST", "/", nil), u.ID, "password+totp")
	if err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	load := func() (*store.Session, error) {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(cookie)
		se, _, err := sm.Load(ctx, r)
		return se, err
	}

	// A fresh sign-in counts as a recent confirmation; ten minutes later it no longer does.
	if !sm.RecentlyAuthenticated(se, 5*time.Minute) {
		t.Fatal("fresh sign-in not recent")
	}
	now = now.Add(10 * time.Minute)
	if sm.RecentlyAuthenticated(se, 5*time.Minute) {
		t.Fatal("old sign-in still recent")
	}
	// A step-up makes it recent again, and the session keeps it.
	if err := sm.MarkAuth(ctx, se); err != nil {
		t.Fatal(err)
	}
	reloaded, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if !sm.RecentlyAuthenticated(reloaded, 5*time.Minute) {
		t.Fatal("step-up not kept by the session")
	}

	// Signing out ends the session and expires the cookie.
	out := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r.AddCookie(cookie)
	if err := sm.Destroy(ctx, out, r); err != nil {
		t.Fatal(err)
	}
	if c := out.Result().Cookies()[0]; c.Name != SessionCookie || c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("cookie not expired: %+v", c)
	}
	if _, err := load(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session survived sign-out: %v", err)
	}
	// Signing out without a session is not an error.
	if err := sm.Destroy(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	if got := ClientIP(r); got != "192.0.2.1:1234" {
		t.Fatalf("without a resolved IP: %q", got)
	}
	if got := ClientIP(WithClientIP(r, "198.51.100.7")); got != "198.51.100.7" {
		t.Fatalf("resolved IP ignored: %q", got)
	}
}

func TestThrottleForgetsOldFailures(t *testing.T) {
	th := NewThrottle()
	now := time.Unix(0, 0)
	th.SetClock(func() time.Time { return now })
	th.Fail("ip:1")
	th.Fail("ip:2")
	now = now.Add(th.Window / 2)
	th.GC()
	if len(th.entries) != 2 {
		t.Fatalf("failures inside the window dropped: %d left", len(th.entries))
	}
	now = now.Add(th.Window)
	th.GC()
	if len(th.entries) != 0 {
		t.Fatalf("failures outside the window kept: %d left", len(th.entries))
	}
}

func TestWeakPasswordSaysWhy(t *testing.T) {
	err := CheckPasswordPolicy("alice", "short")
	var weak ErrWeakPassword
	if !errors.As(err, &weak) || weak.Reason == "" || !strings.HasPrefix(err.Error(), "password rejected: ") {
		t.Fatalf("unhelpful rejection: %v", err)
	}
	// An unknown username costs the same work as a real check and never panics.
	BurnPasswordCheck(context.Background(), "whatever")
}
