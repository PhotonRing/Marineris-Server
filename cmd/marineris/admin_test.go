package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/store"
)

// The break-glass CLI resets the Owner like any User, also an Owner who got
// ownership through a handover.
func TestBreakGlassResetsTheOwner(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MARINERIS_DATA_DIR", dir)
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "marineris.db"))
	if err != nil {
		t.Fatal(err)
	}
	first := &store.User{Username: "admin", PasswordHash: "old", WebAuthnID: []byte("h1")}
	bob := &store.User{Username: "bob", PasswordHash: "old", TOTPSecret: "old", WebAuthnID: []byte("h2")}
	if err := st.CreateOwner(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(ctx, bob); err != nil {
		t.Fatal(err)
	}
	if err := st.AddGroupMember(ctx, bob.ID, store.GroupAdmins); err != nil {
		t.Fatal(err)
	}
	if err := st.TransferOwnership(ctx, first.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	for _, cmd := range []string{"reset-password", "reset-mfa"} {
		if err := admin([]string{cmd, "bob"}); err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
	}

	st, err = store.Open(ctx, filepath.Join(dir, "marineris.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetUser(ctx, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash == "old" || got.TOTPSecret == "old" || len(got.RecoveryCodes) == 0 {
		t.Fatalf("bob not reset: hash changed=%v totp changed=%v codes=%d", got.PasswordHash != "old", got.TOTPSecret != "old", len(got.RecoveryCodes))
	}
	if owner, _ := st.OwnerID(ctx); owner != bob.ID {
		t.Fatalf("Owner is now %d, want bob (%d)", owner, bob.ID)
	}
}

func TestUserLineShowsGroupsOwnerAndState(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		u    store.User
		want string
	}{
		{store.User{ID: 1, Username: "admin", DisplayName: "Theo", Groups: []string{"admins", "family"}, CreatedAt: at},
			"admin\tTheo\tadmins,family\towner\tactive\tcreated 2026-01-02T03:04:05Z"},
		{store.User{ID: 2, Username: "carol", DisplayName: "carol", Groups: []string{}, Disabled: true, CreatedAt: at},
			"carol\tcarol\t-\t\tdisabled\tcreated 2026-01-02T03:04:05Z"},
	} {
		if got := userLine(&tc.u, 1); got != tc.want {
			t.Errorf("got  %q\nwant %q", got, tc.want)
		}
	}
}
