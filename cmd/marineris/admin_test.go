package main

import (
	"testing"
	"time"

	"github.com/photonring/marineris-server/internal/store"
)

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
