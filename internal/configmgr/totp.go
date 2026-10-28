package configmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/photonring/marineris-server/internal/authelia"
	"github.com/photonring/marineris-server/internal/docker"
	firstrun "github.com/photonring/marineris-server/internal/setup"
)

// SyncAutheliaTOTP registers each user's authenticator secret in Authelia, so
// that one authenticator entry signs in to both Marineris and the Authelia
// portal: Authelia then never asks to register a device (which needs a
// one-time code sent by e-mail). Users whose current secret is already
// registered are skipped, so calling it often is cheap.
func (m *Manager) SyncAutheliaTOTP(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.syncAutheliaTOTPLocked(ctx)
}

func (m *Manager) syncAutheliaTOTPLocked(ctx context.Context) error {
	users, err := m.store.ListUsers(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, u := range users {
		if u.TOTPSecret == "" {
			continue
		}
		secret, err := m.box.Open(u.TOTPSecret, firstrun.TOTPContext(u.WebAuthnID))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", u.Username, err))
			continue
		}
		fp := totpFingerprint(u.Username, string(secret))
		if u.AutheliaTOTP == fp {
			continue
		}
		code, out, err := m.docker.Run(ctx, docker.RunSpec{
			Image: m.cfg.AutheliaImage,
			User:  helperUser(),
			Cmd: []string{"authelia", "storage", "user", "totp", "generate", u.Username,
				"--secret", string(secret), "--force", "--config", "/config/" + autheliaConfigFile},
			Env: authelia.Env("/secrets"),
			Binds: []string{
				m.cfg.AutheliaDir() + ":/config:ro",
				m.cfg.AutheliaSecrets() + ":/secrets:ro",
				m.cfg.AutheliaDataDir() + ":" + authelia.DataDir,
			},
		})
		if err == nil && code != 0 {
			// The output of a successful run holds the secret; a failed one
			// should not, but never let it reach a log.
			err = errors.New(strings.ReplaceAll(lastLines(out, 3), string(secret), "••••"))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("registering the authenticator of %s in Authelia: %w", u.Username, err))
			continue
		}
		if err := m.store.SetAutheliaTOTP(ctx, u.ID, fp); err != nil {
			errs = append(errs, err)
			continue
		}
		m.log.Info("authenticator registered in Authelia", "user", u.Username)
	}
	return errors.Join(errs...)
}

// totpFingerprint identifies a user's secret without revealing it.
func totpFingerprint(username, secret string) string {
	sum := sha256.Sum256([]byte("marineris-authelia-totp\x00" + username + "\x00" + secret))
	return hex.EncodeToString(sum[:16])
}
