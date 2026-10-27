package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// RateLimit limits requests per client IP. Zero Events disables it.
type RateLimit struct {
	Events int    `json:"events"`
	Window string `json:"window"`
}

// Settings are the global, admin-editable settings.
type Settings struct {
	SetupComplete bool `json:"setup_complete"`

	// ACME / certificates
	ACMEEmail   string `json:"acme_email"`
	ACMEStaging bool   `json:"acme_staging"`
	// ACMEDirectory overrides the CA directory URL (advanced; used for tests
	// against Pebble or for a private ACME CA).
	ACMEDirectory string `json:"acme_directory"`
	// ACMETrustedRootFile is the path, inside the Caddy container, of an
	// extra PEM root to trust when talking to a custom ACME directory.
	ACMETrustedRootFile string `json:"acme_trusted_root_file"`
	// DNSResolvers used by Caddy to check DNS-01 propagation (host:port).
	DNSResolvers []string `json:"dns_resolvers"`
	// DNSPropagationTimeout, as a Go duration, or "-1" to skip the check.
	DNSPropagationTimeout string `json:"dns_propagation_timeout"`

	// Public UI
	UIHost                string `json:"ui_host"`
	UIProtectWithAuthelia bool   `json:"ui_protect_with_authelia"`
	AuthSubdomain         string `json:"auth_subdomain"`

	// Home page and hub
	HomePage      string   `json:"home_page"` // "hub" or "dashboard"
	HubTitle      string   `json:"hub_title"`
	HubWidgets    []string `json:"hub_widgets"`
	HubBackground bool     `json:"hub_background"`

	// Shield
	CrowdSecEnabled  bool      `json:"crowdsec_enabled"`
	DefaultRateLimit RateLimit `json:"default_rate_limit"`
	LoginRateLimit   RateLimit `json:"login_rate_limit"`
}

// DefaultSettings returns the settings of a fresh installation. Certificates
// come from the Let's Encrypt staging CA until the admin switches to
// production deliberately.
func DefaultSettings() Settings {
	return Settings{
		ACMEStaging:           true,
		DNSPropagationTimeout: "2m",
		UIProtectWithAuthelia: true,
		AuthSubdomain:         "auth",
		CrowdSecEnabled:       true,
		LoginRateLimit:        RateLimit{Events: 20, Window: "1m"},
		HomePage:              "hub",
		HubTitle:              "Marineris",
		HubWidgets:            []string{"clock", "cpu", "memory", "disk", "network", "uptime"},
	}
}

const settingsKey = "global"

// GetSettings returns the global settings (defaults when never saved).
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	st := DefaultSettings()
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return Settings{}, err
	}
	return st, nil
}

// PutSettings stores the global settings.
func (s *Store) PutSettings(ctx context.Context, st Settings) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingsKey, string(raw))
	return err
}

// UpdateSettings applies fn to the current settings and stores the result.
func (s *Store) UpdateSettings(ctx context.Context, fn func(*Settings) error) (Settings, error) {
	var out Settings
	err := s.tx(ctx, func(tx *sql.Tx) error {
		st := DefaultSettings()
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			if err := json.Unmarshal([]byte(raw), &st); err != nil {
				return err
			}
		}
		if err := fn(&st); err != nil {
			return err
		}
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			settingsKey, string(b)); err != nil {
			return err
		}
		out = st
		return nil
	})
	return out, err
}
