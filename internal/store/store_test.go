package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDatabaseFilePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("db perms %o, want 600", st.Mode().Perm())
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s, err := Open(ctx, p)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		s.Close()
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	ctx := context.Background()
	s, err := Open(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE schema_version SET version = 999`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(ctx, p); err == nil {
		t.Fatal("expected refusal of newer schema")
	}
}

func TestSettingsDefaultsAndUpdate(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ACMEStaging || st.SetupComplete || !st.UIProtectWithAuthelia || !st.CrowdSecEnabled {
		t.Fatalf("unsafe defaults: %+v", st)
	}
	_, err = s.UpdateSettings(ctx, func(st *Settings) error {
		st.ACMEEmail = "ops@example.com"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, _ = s.GetSettings(ctx)
	if st.ACMEEmail != "ops@example.com" || !st.ACMEStaging {
		t.Fatalf("update lost fields: %+v", st)
	}
	boom := errors.New("boom")
	if _, err := s.UpdateSettings(ctx, func(st *Settings) error { st.ACMEEmail = "x"; return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	st, _ = s.GetSettings(ctx)
	if st.ACMEEmail != "ops@example.com" {
		t.Fatal("failed update was persisted")
	}
}

func TestUsersAndTOTPReplay(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &User{Username: "Admin", PasswordHash: "h", WebAuthnID: []byte("handle"), Role: "admin"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, &User{Username: "admin", PasswordHash: "h", WebAuthnID: []byte("x"), Role: "admin"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("usernames must be case-insensitively unique, got %v", err)
	}
	got, err := s.GetUserByName(ctx, "ADMIN")
	if err != nil || got.ID != u.ID {
		t.Fatalf("lookup failed: %v", err)
	}
	if _, err := s.GetUserByName(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	ok, _ := s.AdvanceTOTPStep(ctx, u.ID, 100)
	if !ok {
		t.Fatal("first use must succeed")
	}
	for _, step := range []int64{100, 99} {
		if ok, _ := s.AdvanceTOTPStep(ctx, u.ID, step); ok {
			t.Fatalf("step %d replay accepted", step)
		}
	}
}

func TestTOTPStepConcurrentSingleUse(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &User{Username: "a", PasswordHash: "h", WebAuthnID: []byte("h"), Role: "admin"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := s.AdvanceTOTPStep(ctx, u.ID, 5); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("code accepted %d times", wins)
	}
}

func TestRecoveryCodesSingleUse(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &User{Username: "a", PasswordHash: "h", WebAuthnID: []byte("h"), Role: "admin"}
	_ = s.CreateUser(ctx, u)
	_ = s.SetRecoveryCodes(ctx, u.ID, []string{"h1", "h2"})
	match := func(h string) bool { return h == "h1" }
	if ok, _ := s.ConsumeRecoveryCode(ctx, u.ID, match); !ok {
		t.Fatal("first use failed")
	}
	if ok, _ := s.ConsumeRecoveryCode(ctx, u.ID, match); ok {
		t.Fatal("recovery code reused")
	}
	got, _ := s.GetUser(ctx, u.ID)
	if len(got.RecoveryCodes) != 1 || got.RecoveryCodes[0] != "h2" {
		t.Fatalf("codes %v", got.RecoveryCodes)
	}
}

func TestSessionsLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := &User{Username: "a", PasswordHash: "h", WebAuthnID: []byte("h"), Role: "admin"}
	_ = s.CreateUser(ctx, u)
	now := time.Now().UTC()
	for _, id := range []string{"s1", "s2"} {
		if err := s.CreateSession(ctx, &Session{IDHash: id, UserID: u.ID, CSRFToken: "c", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), AuthAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteUserSessions(ctx, u.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "s2"); !errors.Is(err, ErrNotFound) {
		t.Fatal("other session survived")
	}
	if _, err := s.GetSession(ctx, "s1"); err != nil {
		t.Fatal("kept session removed")
	}
	n, _ := s.PurgeExpiredSessions(ctx, now.Add(2*time.Hour), 30*time.Minute)
	if n != 1 {
		t.Fatalf("purged %d", n)
	}
}

func TestDNSProfileInUse(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	p := &DNSProfile{Name: "OVH main", Provider: "ovh"}
	if err := s.CreateDNSProfile(ctx, p, func(id int64) (string, error) { return "sealed", nil }); err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 || p.Config != "sealed" {
		t.Fatalf("%+v", p)
	}
	d := &Domain{Name: "example.com", DNSProfileID: &p.ID, Wildcard: true}
	if err := s.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDNSProfile(ctx, p.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected ErrInUse, got %v", err)
	}
	missing := int64(999)
	if err := s.CreateDomain(ctx, &Domain{Name: "b.com", DNSProfileID: &missing}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for dangling profile, got %v", err)
	}
	if err := s.DeleteDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDNSProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDNSProfileSealFailureRollsBack(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	err := s.CreateDNSProfile(ctx, &DNSProfile{Name: "x", Provider: "ovh"}, func(int64) (string, error) { return "", errors.New("no") })
	if err == nil {
		t.Fatal("expected error")
	}
	ps, _ := s.ListDNSProfiles(ctx)
	if len(ps) != 0 {
		t.Fatal("profile with empty config persisted")
	}
}

func TestRoutesRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := &Route{Name: "app", Host: "app.example.com", Kind: RouteContainer, Target: RouteTarget{Container: "web", Port: 80}, Auth: AuthAuthelia, Enabled: true, RateLimit: &RateLimit{Events: 10, Window: "1m"}}
	if err := s.CreateRoute(ctx, r); err != nil {
		t.Fatal(err)
	}
	dup := *r
	dup.Name = "other"
	if err := s.CreateRoute(ctx, &dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("host+path must be unique, got %v", err)
	}
	got, err := s.GetRoute(ctx, r.ID)
	if err != nil || got.Target.Port != 80 || got.RateLimit == nil || got.RateLimit.Events != 10 {
		t.Fatalf("%+v %v", got, err)
	}
	got.RateLimit = nil
	if err := s.UpdateRoute(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetRoute(ctx, r.ID)
	if got.RateLimit != nil {
		t.Fatal("rate limit not cleared")
	}
}

func TestConfigHistoryPrune(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = s.AddConfigVersion(ctx, &ConfigVersion{Component: "caddy", Actor: "t", Reason: "r", Content: "c", Status: ConfigApplied})
	}
	_ = s.AddConfigVersion(ctx, &ConfigVersion{Component: "authelia", Actor: "t", Reason: "r", Content: "c", Status: ConfigApplied})
	if err := s.PruneConfigHistory(ctx, "caddy", 2); err != nil {
		t.Fatal(err)
	}
	vs, _ := s.ListConfigVersions(ctx, "caddy", 100)
	if len(vs) != 2 || vs[0].ID != 5 {
		t.Fatalf("got %d versions", len(vs))
	}
	vs, _ = s.ListConfigVersions(ctx, "authelia", 100)
	if len(vs) != 1 {
		t.Fatal("prune touched other component")
	}
	last, err := s.LastAppliedConfig(ctx, "caddy")
	if err != nil || last.ID != 5 {
		t.Fatal(err)
	}
}
