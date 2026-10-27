// Package config loads the process configuration of the Marineris server.
//
// Process configuration (where things live, how to reach the other services)
// comes from environment variables. Everything an administrator edits from the
// UI lives in the database instead (see package store).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the immutable process configuration.
type Config struct {
	// DataDir holds the database, secrets, generated configs and stacks.
	// In the container it must be mounted at the same absolute path as on
	// the host so that bind mounts in compose stacks resolve identically.
	DataDir string

	// EdgeAddr is the listener Caddy proxies the public UI to.
	EdgeAddr string
	// LocalAddr is the break-glass / first-run listener. It must only ever be
	// published on the host's loopback interface.
	LocalAddr string

	DockerHost        string
	CaddyAdmin        string
	CaddyProbeAddr    string
	CaddyTLSAddr      string
	AutheliaURL       string
	AutheliaContainer string
	AutheliaImage     string
	CrowdSecURL       string
	AppsNetwork       string
	StaticDir         string

	BrandingDir string
	LogLevel    string

	// SessionIdle and SessionMax bound the lifetime of an admin session.
	SessionIdle time.Duration
	SessionMax  time.Duration
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads the configuration through the given lookup function.
func LoadFrom(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	c := &Config{
		DataDir:           get("MARINERIS_DATA_DIR", "/opt/marineris/data"),
		EdgeAddr:          get("MARINERIS_EDGE_ADDR", ":8080"),
		LocalAddr:         get("MARINERIS_LOCAL_ADDR", ":8081"),
		DockerHost:        get("MARINERIS_DOCKER_HOST", "tcp://socket-proxy:2375"),
		CaddyAdmin:        get("MARINERIS_CADDY_ADMIN", "unix:///run/caddy-admin/admin.sock"),
		CaddyProbeAddr:    get("MARINERIS_CADDY_PROBE_ADDR", "caddy:2020"),
		CaddyTLSAddr:      get("MARINERIS_CADDY_TLS_ADDR", "caddy:443"),
		AutheliaURL:       get("MARINERIS_AUTHELIA_URL", "http://authelia:9091"),
		AutheliaContainer: get("MARINERIS_AUTHELIA_CONTAINER", "marineris-authelia"),
		AutheliaImage:     get("MARINERIS_AUTHELIA_IMAGE", "authelia/authelia:4.39"),
		CrowdSecURL:       get("MARINERIS_CROWDSEC_URL", "http://crowdsec:8080"),
		AppsNetwork:       get("MARINERIS_APPS_NETWORK", "marineris-apps"),
		StaticDir:         get("MARINERIS_STATIC_DIR", "/srv/static"),
		BrandingDir:       get("MARINERIS_BRANDING_DIR", ""),
		LogLevel:          get("MARINERIS_LOG_LEVEL", "info"),
	}
	var err error
	if c.SessionIdle, err = duration(get("MARINERIS_SESSION_IDLE", "30m")); err != nil {
		return nil, fmt.Errorf("MARINERIS_SESSION_IDLE: %w", err)
	}
	if c.SessionMax, err = duration(get("MARINERIS_SESSION_MAX", "12h")); err != nil {
		return nil, fmt.Errorf("MARINERIS_SESSION_MAX: %w", err)
	}
	if c.SessionIdle > c.SessionMax {
		return nil, errors.New("MARINERIS_SESSION_IDLE must not exceed MARINERIS_SESSION_MAX")
	}
	if !filepath.IsAbs(c.DataDir) {
		return nil, errors.New("MARINERIS_DATA_DIR must be an absolute path")
	}
	c.DataDir = filepath.Clean(c.DataDir)
	return c, nil
}

func duration(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("must be positive")
	}
	return d, nil
}

// Paths derived from DataDir.

func (c *Config) DBPath() string         { return filepath.Join(c.DataDir, "marineris.db") }
func (c *Config) SecretsDir() string     { return filepath.Join(c.DataDir, "secrets") }
func (c *Config) MasterKeyPath() string  { return filepath.Join(c.SecretsDir(), "master.key") }
func (c *Config) SetupTokenPath() string { return filepath.Join(c.SecretsDir(), "setup-token") }
func (c *Config) BouncerKeyPath() string {
	return filepath.Join(c.SecretsDir(), "crowdsec-bouncer.key")
}
func (c *Config) AutheliaDir() string     { return filepath.Join(c.DataDir, "authelia") }
func (c *Config) AutheliaSecrets() string { return filepath.Join(c.SecretsDir(), "authelia") }
func (c *Config) StacksDir() string       { return filepath.Join(c.DataDir, "stacks") }
func (c *Config) StaticRoot() string      { return filepath.Join(c.DataDir, "static") }
