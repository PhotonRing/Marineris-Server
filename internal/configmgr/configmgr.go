// Package configmgr renders and applies the configuration of every managed
// component with the same safety cycle:
//
//	render → validate → snapshot → apply → health-check → (auto-rollback)
//
// Every attempt is recorded in the config history, sealed because generated
// configurations contain credentials.
package configmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/photonring/marineris-server/internal/authelia"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/config"
	"github.com/photonring/marineris-server/internal/dnsprofiles"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/store"
)

// Components.
const (
	Caddy    = "caddy"
	Authelia = "authelia"
)

// HistoryKeep is the number of versions kept per component.
const HistoryKeep = 50

// CaddyAPI is the subset of the Caddy client used here.
type CaddyAPI interface {
	Load(ctx context.Context, cfg []byte) error
	Healthy(ctx context.Context) error
}

// DockerAPI is the subset of the Docker client used here.
type DockerAPI interface {
	Run(ctx context.Context, spec docker.RunSpec) (int64, string, error)
	Action(ctx context.Context, ref, action string) error
}

// Manager applies configurations.
type Manager struct {
	cfg    *config.Config
	store  *store.Store
	box    *secrets.Box
	caddy  CaddyAPI
	docker DockerAPI
	log    *slog.Logger

	// AutheliaHealth is polled after an Authelia restart.
	AutheliaHealth func(ctx context.Context) error
	// HealthTimeout bounds post-apply health checks.
	HealthTimeout time.Duration

	mu sync.Mutex
}

// New returns a manager.
func New(cfg *config.Config, st *store.Store, box *secrets.Box, cd CaddyAPI, dk DockerAPI, log *slog.Logger) *Manager {
	m := &Manager{cfg: cfg, store: st, box: box, caddy: cd, docker: dk, log: log, HealthTimeout: 60 * time.Second}
	m.AutheliaHealth = m.autheliaHealth
	return m
}

// ApplyError is returned when a configuration could not be applied. The
// previous configuration is still (or again) running.
type ApplyError struct {
	Component  string
	Stage      string // render, validate, apply, health
	Err        error
	RolledBack bool
}

func (e *ApplyError) Error() string {
	s := fmt.Sprintf("%s: %s failed: %v", e.Component, e.Stage, e.Err)
	if e.RolledBack {
		s += " (previous configuration restored)"
	}
	return s
}

func (e *ApplyError) Unwrap() error { return e.Err }

func sealCtx(component string) string { return "config-history:" + component }

func (m *Manager) record(ctx context.Context, component, actor, reason string, content []byte, status, errMsg string) (*store.ConfigVersion, error) {
	sealed, err := m.box.Seal(content, sealCtx(component))
	if err != nil {
		return nil, err
	}
	v := &store.ConfigVersion{Component: component, Actor: actor, Reason: reason, Content: sealed, Status: status, Error: errMsg}
	if err := m.store.AddConfigVersion(ctx, v); err != nil {
		return nil, err
	}
	_ = m.store.PruneConfigHistory(ctx, component, HistoryKeep)
	return v, nil
}

// Content returns the decrypted content of a history version.
func (m *Manager) Content(v *store.ConfigVersion) ([]byte, error) {
	return m.box.Open(v.Content, sealCtx(v.Component))
}

// BouncerKey reads the CrowdSec bouncer key; empty when not provisioned.
func (m *Manager) BouncerKey() string {
	b, err := secrets.ReadGroupSecretFile(m.cfg.BouncerKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// CaddyInput collects the state the Caddy config is rendered from.
func (m *Manager) CaddyInput(ctx context.Context) (caddy.Input, error) {
	st, err := m.store.GetSettings(ctx)
	if err != nil {
		return caddy.Input{}, err
	}
	domains, err := m.store.ListDomains(ctx)
	if err != nil {
		return caddy.Input{}, err
	}
	routes, err := m.store.ListRoutes(ctx)
	if err != nil {
		return caddy.Input{}, err
	}
	in := caddy.Input{
		Env: caddy.Env{
			AdminListen:       caddyListen(m.cfg.CaddyAdmin),
			ProbeListen:       ":" + portOf(m.cfg.CaddyProbeAddr),
			AccessLog:         "/var/log/caddy/access.log",
			MarinerisUpstream: "marineris:" + portOf(m.cfg.EdgeAddr),
			AutheliaUpstream:  hostPort(m.cfg.AutheliaURL),
			CrowdSecURL:       m.cfg.CrowdSecURL,
			BouncerKey:        m.BouncerKey(),
			StaticRoot:        m.cfg.StaticDir,
		},
		Settings: st,
		Routes:   routes,
	}
	for _, d := range domains {
		cd := caddy.Domain{Name: d.Name, Wildcard: d.Wildcard}
		if d.DNSProfileID != nil {
			p, err := m.store.GetDNSProfile(ctx, *d.DNSProfileID)
			if err != nil {
				return caddy.Input{}, fmt.Errorf("domain %s: %w", d.Name, err)
			}
			pc, err := dnsprofiles.Open(m.box, p.ID, p.Config)
			if err != nil {
				return caddy.Input{}, fmt.Errorf("domain %s: cannot decrypt DNS profile %q: %w", d.Name, p.Name, err)
			}
			cd.DNS = &caddy.DNS{Provider: p.Provider, Config: pc}
		} else {
			cd.Wildcard = false
		}
		in.Domains = append(in.Domains, cd)
	}
	return in, nil
}

// RenderCaddy renders (and validates) the Caddy configuration.
func (m *Manager) RenderCaddy(ctx context.Context) ([]byte, error) {
	in, err := m.CaddyInput(ctx)
	if err != nil {
		return nil, err
	}
	return caddy.Build(in)
}

// ApplyCaddy renders and applies the Caddy configuration.
func (m *Manager) ApplyCaddy(ctx context.Context, actor, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, err := m.RenderCaddy(ctx)
	if err != nil {
		return &ApplyError{Component: Caddy, Stage: "validate", Err: err}
	}
	return m.applyCaddyLocked(ctx, actor, reason, cfg)
}

func (m *Manager) applyCaddyLocked(ctx context.Context, actor, reason string, cfg []byte) error {
	prev, perr := m.store.LastAppliedConfig(ctx, Caddy)
	if perr != nil && !errors.Is(perr, store.ErrNotFound) {
		return perr
	}
	if err := m.caddy.Load(ctx, cfg); err != nil {
		// Caddy keeps running the previous config when a load fails.
		_, _ = m.record(ctx, Caddy, actor, reason, cfg, store.ConfigFailed, err.Error())
		return &ApplyError{Component: Caddy, Stage: "apply", Err: err}
	}
	if err := m.waitHealthy(ctx, m.caddy.Healthy); err != nil {
		rolled := false
		if prev != nil {
			if old, oerr := m.Content(prev); oerr == nil {
				if lerr := m.caddy.Load(ctx, old); lerr == nil {
					rolled = true
				} else {
					m.log.Error("caddy rollback failed", "err", lerr)
				}
			}
		}
		status := store.ConfigFailed
		if rolled {
			status = store.ConfigRolledBack
		}
		_, _ = m.record(ctx, Caddy, actor, reason, cfg, status, err.Error())
		return &ApplyError{Component: Caddy, Stage: "health", Err: err, RolledBack: rolled}
	}
	if prev != nil {
		_ = m.store.SetConfigStatus(ctx, prev.ID, store.ConfigSuperseded, "")
	}
	_, err := m.record(ctx, Caddy, actor, reason, cfg, store.ConfigApplied, "")
	return err
}

// RollbackCaddy re-applies a previous Caddy configuration version.
func (m *Manager) RollbackCaddy(ctx context.Context, actor string, versionID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.store.GetConfigVersion(ctx, versionID)
	if err != nil {
		return err
	}
	if v.Component != Caddy {
		return errors.New("not a Caddy configuration")
	}
	cfg, err := m.Content(v)
	if err != nil {
		return err
	}
	return m.applyCaddyLocked(ctx, actor, fmt.Sprintf("rollback to version %d", versionID), cfg)
}

func (m *Manager) waitHealthy(ctx context.Context, check func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, m.HealthTimeout)
	defer cancel()
	var last error
	for {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		last = check(cctx)
		ccancel()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not healthy after %s: %w", m.HealthTimeout, last)
		case <-time.After(time.Second):
		}
	}
}

// AutheliaInput collects the state the Authelia config is rendered from.
func (m *Manager) AutheliaInput(ctx context.Context) (authelia.Input, error) {
	st, err := m.store.GetSettings(ctx)
	if err != nil {
		return authelia.Input{}, err
	}
	in := authelia.Input{Settings: st}
	domains, err := m.store.ListDomains(ctx)
	if err != nil {
		return in, err
	}
	for _, d := range domains {
		in.Domains = append(in.Domains, d.Name)
	}
	routes, err := m.store.ListRoutes(ctx)
	if err != nil {
		return in, err
	}
	for _, r := range routes {
		if r.Enabled && store.UsesAuthelia(r.Auth) {
			in.Protected = append(in.Protected, authelia.Resource{Host: r.Host, PathPrefix: r.PathPrefix, AdminOnly: r.Auth == store.AuthAdmins})
		}
	}
	// Every User opens the UI (their Hub and Account); the API decides what
	// each may do there (routes.go).
	if st.UIHost != "" && st.UIProtectWithAuthelia {
		in.Protected = append(in.Protected, authelia.Resource{Host: st.UIHost})
	}
	users, err := m.store.ListUsers(ctx)
	if err != nil {
		return in, err
	}
	for _, u := range users {
		email := u.Email
		if email == "" {
			// Users without an e-mail of their own get the server's.
			email = st.ACMEEmail
		}
		in.Users = append(in.Users, authelia.User{
			Username: u.Username, DisplayName: u.DisplayName, Email: email, PasswordHash: u.PasswordHash,
			// A Reset link blocks the User until it is used or revoked.
			Groups: u.Groups, Disabled: u.Disabled || u.ResetPending,
		})
	}
	return in, nil
}

const (
	autheliaConfigFile = "configuration.yml"
	autheliaUsersFile  = "users_database.yml"
)

// EnsureAutheliaFiles writes a deny-all placeholder configuration when none
// exists, so Authelia can start before setup completes.
func (m *Manager) EnsureAutheliaFiles(ctx context.Context) error {
	if err := authelia.EnsureSecrets(m.cfg.AutheliaSecrets()); err != nil {
		return err
	}
	// Before Authelia starts: its configuration names the assets folder.
	if err := m.writeAutheliaAssets(ctx); err != nil {
		return err
	}
	dir := m.cfg.AutheliaDir()
	if _, err := os.Stat(filepath.Join(dir, autheliaConfigFile)); err == nil {
		return nil
	}
	// Start from a deny-all placeholder; the real configuration is installed
	// by ApplyAuthelia, which validates it first.
	st, err := m.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	cfg, users, err := authelia.Render(authelia.Input{Settings: st})
	if err != nil {
		return err
	}
	if err := writePublic(filepath.Join(dir, autheliaUsersFile), users); err != nil {
		return err
	}
	return writePublic(filepath.Join(dir, autheliaConfigFile), cfg)
}

// writePublic writes a file Authelia (same UID) must read.
func writePublic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return secrets.WriteFileAtomic(path, data, 0o640, false)
}

// clearCandidate deletes the files of Authelia's candidate directory, but
// not the directory itself.
func clearCandidate(cand string) error {
	for _, f := range []string{autheliaConfigFile, autheliaUsersFile} {
		if err := os.Remove(filepath.Join(cand, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

type autheliaBundle struct {
	Config []byte `json:"config"`
	Users  []byte `json:"users"`
}

// ApplyAuthelia renders Authelia's files, validates them with the real
// Authelia binary, installs them and restarts Authelia when its
// configuration (not only its users) changed.
func (m *Manager) ApplyAuthelia(ctx context.Context, actor, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, err := m.AutheliaInput(ctx)
	if err != nil {
		return &ApplyError{Component: Authelia, Stage: "render", Err: err}
	}
	cfg, users, err := authelia.Render(in)
	if err != nil {
		return &ApplyError{Component: Authelia, Stage: "validate", Err: err}
	}
	if err := m.writeAutheliaAssets(ctx); err != nil {
		return &ApplyError{Component: Authelia, Stage: "render", Err: err}
	}
	if err := m.applyAutheliaLocked(ctx, actor, reason, cfg, users); err != nil {
		return err
	}
	// Authelia knows the users now: give them their authenticators. A failure
	// does not undo the configuration; housekeeping retries.
	if err := m.syncAutheliaTOTPLocked(ctx); err != nil {
		m.log.Warn("authenticators not registered in Authelia yet", "err", err)
	}
	return nil
}

func (m *Manager) applyAutheliaLocked(ctx context.Context, actor, reason string, cfg, users []byte) error {
	dir := m.cfg.AutheliaDir()
	curCfg, _ := os.ReadFile(filepath.Join(dir, autheliaConfigFile))  //nolint:gosec // path from process configuration
	curUsers, _ := os.ReadFile(filepath.Join(dir, autheliaUsersFile)) //nolint:gosec // path from process configuration
	bundle, _ := json.Marshal(autheliaBundle{Config: cfg, Users: users})
	if bytes.Equal(curCfg, cfg) && bytes.Equal(curUsers, users) {
		return nil
	}

	// Validate the candidate with the real Authelia binary. The candidate
	// directory is kept and only its files are replaced: Docker Desktop (WSL)
	// cannot bind-mount a directory deleted and created again at the same path.
	cand := filepath.Join(dir, ".candidate")
	if err := clearCandidate(cand); err != nil {
		return err
	}
	defer func() { _ = clearCandidate(cand) }()
	if err := writePublic(filepath.Join(cand, autheliaConfigFile), cfg); err != nil {
		return err
	}
	if err := writePublic(filepath.Join(cand, autheliaUsersFile), users); err != nil {
		return err
	}
	// The configuration names an assets folder, which must exist (its
	// content does not matter to the validation).
	if err := os.MkdirAll(filepath.Join(cand, autheliaAssetsDir), 0o750); err != nil {
		return err
	}
	code, out, err := m.docker.Run(ctx, docker.RunSpec{
		Image: m.cfg.AutheliaImage,
		// Same UID as Marineris and Authelia: the files are not readable by
		// anyone else, and the helper must not run as root.
		User:  helperUser(),
		Cmd:   []string{"authelia", "validate-config", "--config", "/config/" + autheliaConfigFile},
		Env:   authelia.Env("/secrets"),
		Binds: []string{cand + ":/config:ro", m.cfg.AutheliaSecrets() + ":/secrets:ro"},
	})
	if err != nil || code != 0 {
		if err == nil {
			err = fmt.Errorf("authelia validate-config: %s", validationErrors(out))
		}
		_, _ = m.record(ctx, Authelia, actor, reason, bundle, store.ConfigFailed, err.Error())
		return &ApplyError{Component: Authelia, Stage: "validate", Err: err}
	}

	prev, _ := m.store.LastAppliedConfig(ctx, Authelia)
	if err := writePublic(filepath.Join(dir, autheliaUsersFile), users); err != nil {
		return err
	}
	if err := writePublic(filepath.Join(dir, autheliaConfigFile), cfg); err != nil {
		return err
	}
	// Users are hot-reloaded by Authelia (watch: true); only a configuration
	// change needs a restart.
	if !bytes.Equal(curCfg, cfg) {
		if err := m.restartAuthelia(ctx); err != nil {
			rolled := m.restoreAuthelia(ctx, curCfg, curUsers)
			status := store.ConfigFailed
			if rolled {
				status = store.ConfigRolledBack
			}
			_, _ = m.record(ctx, Authelia, actor, reason, bundle, status, err.Error())
			return &ApplyError{Component: Authelia, Stage: "health", Err: err, RolledBack: rolled}
		}
	}
	if prev != nil {
		_ = m.store.SetConfigStatus(ctx, prev.ID, store.ConfigSuperseded, "")
	}
	_, err = m.record(ctx, Authelia, actor, reason, bundle, store.ConfigApplied, "")
	return err
}

func (m *Manager) restartAuthelia(ctx context.Context) error {
	if err := m.docker.Action(ctx, m.cfg.AutheliaContainer, "restart"); err != nil {
		return err
	}
	return m.waitHealthy(ctx, m.AutheliaHealth)
}

func (m *Manager) restoreAuthelia(ctx context.Context, cfg, users []byte) bool {
	if cfg == nil {
		return false
	}
	dir := m.cfg.AutheliaDir()
	if err := writePublic(filepath.Join(dir, autheliaUsersFile), users); err != nil {
		return false
	}
	if err := writePublic(filepath.Join(dir, autheliaConfigFile), cfg); err != nil {
		return false
	}
	if err := m.restartAuthelia(ctx); err != nil {
		m.log.Error("authelia rollback failed", "err", err)
		return false
	}
	return true
}

// RollbackAuthelia re-installs a previous Authelia configuration version.
func (m *Manager) RollbackAuthelia(ctx context.Context, actor string, versionID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.store.GetConfigVersion(ctx, versionID)
	if err != nil {
		return err
	}
	if v.Component != Authelia {
		return errors.New("not an Authelia configuration")
	}
	raw, err := m.Content(v)
	if err != nil {
		return err
	}
	var b autheliaBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return err
	}
	return m.applyAutheliaLocked(ctx, actor, fmt.Sprintf("rollback to version %d", versionID), b.Config, b.Users)
}

func (m *Manager) autheliaHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(m.cfg.AutheliaURL, "/")+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authelia health returned %d", resp.StatusCode)
	}
	return nil
}

// ApplyAll applies every component (Authelia first: Caddy routes depend on
// its portal).
func (m *Manager) ApplyAll(ctx context.Context, actor, reason string) error {
	var errs []error
	if err := m.ApplyAuthelia(ctx, actor, reason); err != nil {
		errs = append(errs, err)
	}
	if err := m.ApplyCaddy(ctx, actor, reason); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Redact replaces credential values in a rendered configuration with a
// marker, for display.
func (m *Manager) Redact(ctx context.Context, content []byte) []byte {
	out := content
	repl := func(v string) {
		if len(v) < 6 {
			return
		}
		out = bytes.ReplaceAll(out, []byte(v), []byte("••••redacted••••"))
		// Also catch the JSON-escaped form (e.g. & becomes \u0026).
		if enc, err := json.Marshal(v); err == nil {
			out = bytes.ReplaceAll(out, enc[1:len(enc)-1], []byte("••••redacted••••"))
		}
	}
	if k := m.BouncerKey(); k != "" {
		repl(k)
	}
	ps, _ := m.store.ListDNSProfiles(ctx)
	for _, p := range ps {
		cfg, err := dnsprofiles.Open(m.box, p.ID, p.Config)
		if err != nil {
			continue
		}
		prov, _ := dnsprofiles.Lookup(p.Provider)
		for _, f := range prov.Fields {
			if f.Secret {
				repl(cfg[f.Key])
			}
		}
	}
	return redactHashes(out)
}

// redactHashes hides password hashes in users files.
func redactHashes(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "$argon2id$"); idx >= 0 {
			lines[i] = l[:idx] + "••••redacted••••"
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func caddyListen(admin string) string {
	if strings.HasPrefix(admin, "unix://") {
		return "unix/" + strings.TrimPrefix(admin, "unix://")
	}
	return strings.TrimPrefix(admin, "http://")
}

func portOf(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

func hostPort(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
	return strings.TrimSuffix(u, "/")
}

// helperUser is the uid:gid of helper containers: Marineris' own (65532 in
// the official image), or 65532 if Marineris itself runs as root.
func helperUser() string {
	if os.Getuid() == 0 {
		return "65532:65532"
	}
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// validationErrors extracts the "- problem" lines of authelia's output.
func validationErrors(out string) string {
	var probs []string
	for _, l := range strings.Split(out, "\n") {
		// Docker log lines carry a timestamp prefix.
		if i := strings.Index(l, " - "); i >= 0 {
			probs = append(probs, strings.TrimSpace(l[i+3:]))
		}
	}
	if len(probs) == 0 {
		return lastLines(out, 5)
	}
	return strings.Join(probs, "; ")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
