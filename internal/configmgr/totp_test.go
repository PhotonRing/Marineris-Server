package configmgr

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/photonring/marineris-server/internal/auth"
	firstrun "github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
)

func enrolTOTP(t *testing.T, m *Manager, st *store.Store) string {
	t.Helper()
	ctx := context.Background()
	u, err := st.GetUserByName(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret := auth.NewTOTPSecret()
	sealed, err := m.box.Seal([]byte(secret), firstrun.TOTPContext(u.WebAuthnID))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(ctx, u.ID, sealed); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestSyncAutheliaTOTP(t *testing.T) {
	m, st, _, fd := setup(t)
	ctx := context.Background()

	// No authenticator yet: nothing to register.
	if err := m.SyncAutheliaTOTP(ctx); err != nil || len(fd.runs) != 0 {
		t.Fatalf("err %v, runs %d", err, len(fd.runs))
	}

	secret := enrolTOTP(t, m, st)
	if err := m.SyncAutheliaTOTP(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fd.runs) != 1 {
		t.Fatalf("expected one helper run, got %d", len(fd.runs))
	}
	run := fd.runs[0]
	want := []string{"authelia", "storage", "user", "totp", "generate", "admin", "--secret", secret, "--force"}
	if !slices.Equal(run.Cmd[:len(want)], want) {
		t.Fatalf("command %q", run.Cmd)
	}
	if !slices.Contains(run.Binds, m.cfg.AutheliaDataDir()+":/data") || run.User == "" || strings.HasPrefix(run.User, "0") {
		t.Fatalf("helper %+v", run)
	}

	// Registered: not again.
	if err := m.SyncAutheliaTOTP(ctx); err != nil || len(fd.runs) != 1 {
		t.Fatalf("err %v, runs %d", err, len(fd.runs))
	}

	// A new authenticator is registered again.
	secret = enrolTOTP(t, m, st)
	if err := m.SyncAutheliaTOTP(ctx); err != nil || len(fd.runs) != 2 || !slices.Contains(fd.runs[1].Cmd, secret) {
		t.Fatalf("err %v, runs %d", err, len(fd.runs))
	}
}

func TestSyncAutheliaTOTPFailureRetriesWithoutLeaking(t *testing.T) {
	m, st, _, fd := setup(t)
	ctx := context.Background()
	secret := enrolTOTP(t, m, st)
	fd.validateCode = 1
	fd.output = "error: cannot open database\nsecret was " + secret
	err := m.SyncAutheliaTOTP(ctx)
	if err == nil {
		t.Fatal("failure not reported")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("the secret leaked into the error")
	}
	// Not recorded as registered: the next call tries again.
	fd.validateCode, fd.output = 0, ""
	if err := m.SyncAutheliaTOTP(ctx); err != nil || len(fd.runs) != 2 {
		t.Fatalf("err %v, runs %d", err, len(fd.runs))
	}
}

func TestApplyAutheliaRegistersAuthenticators(t *testing.T) {
	m, st, _, fd := setup(t)
	ctx := context.Background()
	if err := m.EnsureAutheliaFiles(ctx); err != nil {
		t.Fatal(err)
	}
	enrolTOTP(t, m, st)
	if err := m.ApplyAuthelia(ctx, "admin", "test"); err != nil {
		t.Fatal(err)
	}
	var registered bool
	for _, r := range fd.runs {
		registered = registered || slices.Contains(r.Cmd, "generate")
	}
	if !registered {
		t.Fatal("ApplyAuthelia did not register the authenticator")
	}
}
