// Command marineris is the Marineris server.
//
//	marineris serve                 run the server (default)
//	marineris init                  prepare the data directory (init container)
//	marineris healthcheck           probe the local server (Docker HEALTHCHECK)
//	marineris admin <command>       break-glass administration, see `marineris admin help`
//	marineris version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/photonring/marineris-server/internal/api"
	"github.com/photonring/marineris-server/internal/auth"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/certs"
	"github.com/photonring/marineris-server/internal/config"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/docker"
	"github.com/photonring/marineris-server/internal/icons"
	"github.com/photonring/marineris-server/internal/secrets"
	"github.com/photonring/marineris-server/internal/setup"
	"github.com/photonring/marineris-server/internal/stacks"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/sysinfo"
	"github.com/photonring/marineris-server/internal/webui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

// systemContainers are the containers of the Marineris stack itself.
var systemContainers = []string{
	"marineris", "marineris-init", "marineris-caddy", "marineris-authelia",
	"marineris-crowdsec", "marineris-socket-proxy",
}

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "init":
		err = initData()
	case "healthcheck":
		err = healthcheck()
	case "admin":
		err = admin(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve, init, healthcheck, admin, version)\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	_ = l.UnmarshalText([]byte(level))
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l}))
}

// prepare creates the data directory layout and every generated secret. It
// is idempotent and never rotates existing secrets.
func prepare(cfg *config.Config) error {
	for _, d := range []string{cfg.DataDir, cfg.SecretsDir(), cfg.StacksDir(), cfg.StaticRoot()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if err := os.Chmod(cfg.SecretsDir(), 0o700); err != nil { //nolint:gosec // directory: needs the x bit
		return err
	}
	// Authelia (same UID) writes its notifications and database here;
	// Marineris reads the notifications.
	for _, d := range []string{"authelia-notify", "authelia-data"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, d), 0o700); err != nil {
			return err
		}
	}
	if _, err := secrets.LoadOrCreateKey(cfg.MasterKeyPath()); err != nil {
		return err
	}
	// The bouncer key is shared with CrowdSec, which reads it through the
	// 65532 group (group_add in compose.yml): mode 0640.
	if _, err := secrets.ReadGroupSecretFile(cfg.BouncerKeyPath()); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := secrets.WriteFileAtomic(cfg.BouncerKeyPath(), []byte(secrets.HexToken(32)), 0o640, true); err != nil {
			return err
		}
	}
	return nil
}

type app struct {
	cfg   *config.Config
	log   *slog.Logger
	store *store.Store
	box   *secrets.Box
	cmgr  *configmgr.Manager
	setup *setup.Service
}

func openApp(ctx context.Context) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := newLogger(cfg.LogLevel)
	if err := prepare(cfg); err != nil {
		return nil, fmt.Errorf("preparing data directory: %w", err)
	}
	key, err := secrets.LoadOrCreateKey(cfg.MasterKeyPath())
	if err != nil {
		return nil, err
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return nil, err
	}
	return &app{cfg: cfg, log: log, store: st, box: box, setup: setup.New(st, box, cfg.SetupTokenPath())}, nil
}

func initData() error {
	ctx := context.Background()
	a, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer a.store.Close()
	cmgr := configmgr.New(a.cfg, a.store, a.box, nil, nil, a.log)
	if err := cmgr.EnsureAutheliaFiles(ctx); err != nil {
		return err
	}
	tok, err := a.setup.EnsureToken(ctx)
	if err != nil {
		return err
	}
	if tok != "" {
		a.log.Info("setup pending: open the UI through an SSH tunnel and use the setup token from the server logs or " + a.cfg.SetupTokenPath())
	}
	a.log.Info("data directory ready", "dir", a.cfg.DataDir)
	return nil
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer a.store.Close()
	log := a.log

	caddyClient, err := caddy.NewClient(a.cfg.CaddyAdmin, a.cfg.CaddyProbeAddr)
	if err != nil {
		return err
	}
	dk, err := docker.New(a.cfg.DockerHost, a.cfg.AppsNetwork)
	if err != nil {
		return err
	}
	a.cmgr = configmgr.New(a.cfg, a.store, a.box, caddyClient, dk, log)
	if err := a.cmgr.EnsureAutheliaFiles(ctx); err != nil {
		return err
	}
	bindRoots := splitList(os.Getenv("MARINERIS_ALLOWED_BIND_ROOTS"), "/srv,/mnt,/media")
	stk := stacks.NewManager(a.cfg.StacksDir(), a.cfg.DockerHost, bindRoots, systemContainers)

	tok, err := a.setup.EnsureToken(ctx)
	if err != nil {
		return err
	}
	if tok != "" {
		log.Warn("FIRST-RUN SETUP PENDING. Open an SSH tunnel to the local listener and enter this setup token",
			"setup_token", tok, "local_listener", a.cfg.LocalAddr)
	}

	sampler := &sysinfo.Sampler{
		Proc:     "/proc",
		NetDev:   envOr("MARINERIS_HOST_NET_DEV", "/host/net-dev"),
		DiskPath: a.cfg.DataDir,
		Hostname: dk.Hostname,
	}
	go sampler.Start(ctx)

	srv := api.New(api.Deps{
		Config:           a.cfg,
		Store:            a.store,
		Box:              a.box,
		Setup:            a.setup,
		Sessions:         auth.NewSessions(a.store, a.cfg.SessionIdle, a.cfg.SessionMax),
		Passkeys:         auth.NewPasskeys(a.store, "Marineris"),
		AutheliaPasskeys: a.cmgr,
		Throttle:         auth.NewThrottle(),
		Configs:          a.cmgr,
		Docker:           dk,
		Stacks:           stk,
		Certs:            certs.NewChecker(a.cfg.CaddyTLSAddr),
		Icons:            icons.NewFetcher(),
		Stats:            sampler,
		Log:              log,
		Version:          version,
		UI:               webui.Handler(a.cfg.BrandingDir),
	})

	servers := []*http.Server{
		newHTTPServer(a.cfg.EdgeAddr, srv.Handler(api.Edge)),
		newHTTPServer(a.cfg.LocalAddr, srv.Handler(api.Local)),
	}
	errc := make(chan error, len(servers))
	for _, s := range servers {
		go func(s *http.Server) {
			log.Info("listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}(s)
	}

	go a.applyOnStartup(ctx)
	go a.housekeeping(ctx)
	go func() {
		// One-time data migrations that need Docker: retried until Docker
		// (the socket proxy) answers, for a while after startup.
		for range 20 {
			done, err := srv.MoveRouteIconsToServices(ctx)
			if err != nil {
				log.Warn("moving route icons to their services failed", "err", err)
			}
			if done || err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errc:
		log.Error("listener failed", "err", err)
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
	log.Info("stopped")
	return nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      15 * time.Minute, // stack deployments pull images
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// applyOnStartup pushes the configuration once setup is complete, retrying
// until Caddy and Authelia are reachable.
func (a *app) applyOnStartup(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		st, err := a.store.GetSettings(ctx)
		if err == nil && st.SetupComplete {
			err = a.cmgr.ApplyAll(ctx, "system", "startup")
			if err == nil {
				a.log.Info("configuration applied")
				return
			}
			a.log.Warn("startup apply failed, retrying", "err", err)
		} else if err == nil {
			return // nothing to apply before setup; setup triggers an apply
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(time.Duration(attempt+1)*5*time.Second, time.Minute)):
		}
	}
}

func (a *app) housekeeping(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := a.store.PurgeExpiredSessions(ctx, time.Now().UTC(), a.cfg.SessionIdle); err == nil && n > 0 {
				a.log.Info("purged expired sessions", "count", n)
			}
			// Authenticators changed while Authelia was unreachable, or by
			// "marineris admin", are registered in Authelia here.
			if err := a.cmgr.SyncAutheliaTOTP(ctx); err != nil {
				a.log.Warn("authenticators not registered in Authelia yet", "err", err)
			}
		}
	}
}

func healthcheck() error {
	addr := os.Getenv("MARINERIS_EDGE_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/health", nil) //nolint:gosec // fixed local health endpoint
	if err != nil {
		return err
	}
	c := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := c.Do(req) //nolint:gosec // fixed local health endpoint
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s, def string) []string {
	if strings.TrimSpace(s) == "" {
		s = def
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}
