package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/store"
)

const adminHelp = `Break-glass administration. Run inside the Marineris container, e.g.:

  docker exec -it marineris marineris admin <command>

Commands:
  users                       list administrators
  reset-password <user>       set a new random password (printed once) and revoke sessions
  reset-mfa <user>            enrol a new TOTP secret, issue new recovery codes, revoke sessions
  remove-passkeys <user>      delete every passkey of the user
  revoke-sessions <user>      log the user out everywhere
  setup-token                 print the first-run setup token (only before setup)
  disable-ui-authelia         stop protecting the public UI with Authelia (if Authelia is broken)
`

func admin(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" {
		fmt.Print(adminHelp)
		return nil
	}
	ctx := context.Background()
	a, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer a.store.Close()

	user := func() (*store.User, error) {
		if len(args) < 2 {
			return nil, errors.New("missing <user>")
		}
		u, err := a.store.GetUserByName(ctx, args[1])
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("no such user %q", args[1])
		}
		return u, err
	}
	audit := func(action, target string) {
		_ = a.store.Audit(ctx, store.AuditEntry{Actor: "cli", IP: "local", Action: action, Target: target, Success: true})
	}

	switch args[0] {
	case "users":
		us, err := a.store.ListUsers(ctx)
		if err != nil {
			return err
		}
		for _, u := range us {
			fmt.Printf("%s\t%s\tcreated %s\n", u.Username, u.Role, u.CreatedAt.Format(time.RFC3339))
		}
	case "reset-password":
		u, err := user()
		if err != nil {
			return err
		}
		pw := randomPassword()
		hash, err := auth.HashPassword(ctx, pw)
		if err != nil {
			return err
		}
		if err := a.store.SetPasswordHash(ctx, u.ID, hash); err != nil {
			return err
		}
		if err := a.store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
			return err
		}
		audit("cli.reset_password", u.Username)
		fmt.Printf("New password for %s (shown once, change it after logging in):\n\n  %s\n\n", u.Username, pw)
		fmt.Println("The Authelia users file is updated the next time Marineris applies its configuration (restart the marineris container to force it).")
	case "reset-mfa":
		u, err := user()
		if err != nil {
			return err
		}
		secret := auth.NewTOTPSecret()
		sealed, err := a.box.Seal([]byte(secret), setup.TOTPContext(u.WebAuthnID))
		if err != nil {
			return err
		}
		if err := a.store.SetTOTP(ctx, u.ID, sealed); err != nil {
			return err
		}
		codes, hashes := auth.NewRecoveryCodes()
		if err := a.store.SetRecoveryCodes(ctx, u.ID, hashes); err != nil {
			return err
		}
		if err := a.store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
			return err
		}
		audit("cli.reset_mfa", u.Username)
		fmt.Printf("New authenticator secret for %s — add it to your app:\n\n  %s\n  %s\n\nNew recovery codes (store them safely):\n", u.Username, secret, auth.TOTPURI("Marineris", u.Username, secret))
		for _, c := range codes {
			fmt.Println("  " + c)
		}
	case "remove-passkeys":
		u, err := user()
		if err != nil {
			return err
		}
		creds, err := a.store.ListWebAuthnCredentials(ctx, u.ID)
		if err != nil {
			return err
		}
		for _, c := range creds {
			if err := a.store.DeleteWebAuthnCredential(ctx, u.ID, c.ID); err != nil {
				return err
			}
		}
		audit("cli.remove_passkeys", u.Username)
		fmt.Printf("Removed %d passkey(s) of %s\n", len(creds), u.Username)
	case "revoke-sessions":
		u, err := user()
		if err != nil {
			return err
		}
		if err := a.store.DeleteUserSessions(ctx, u.ID, ""); err != nil {
			return err
		}
		audit("cli.revoke_sessions", u.Username)
		fmt.Println("All sessions revoked.")
	case "setup-token":
		tok, err := a.setup.EnsureToken(ctx)
		if err != nil {
			return err
		}
		if tok == "" {
			return errors.New("setup is already complete")
		}
		fmt.Println(tok)
	case "disable-ui-authelia":
		if _, err := a.store.UpdateSettings(ctx, func(s *store.Settings) error {
			s.UIProtectWithAuthelia = false
			return nil
		}); err != nil {
			return err
		}
		audit("cli.disable_ui_authelia", "")
		fmt.Println("The public UI no longer requires Authelia (the Marineris login still applies).")
		fmt.Println("Restart the marineris container to apply: docker restart marineris")
	default:
		fmt.Fprint(os.Stderr, adminHelp)
		return fmt.Errorf("unknown admin command %q", args[0])
	}
	return nil
}

func randomPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	buf := make([]byte, 1)
	for b.Len() < 24 {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		if int(buf[0]) < 256-256%len(alphabet) {
			b.WriteByte(alphabet[int(buf[0])%len(alphabet)])
			if b.Len()%6 == 5 && b.Len() < 23 {
				b.WriteByte('-')
			}
		}
	}
	return b.String()
}
