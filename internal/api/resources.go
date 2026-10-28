package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/configmgr"
	"github.com/photonring/marineris-server/internal/dnsprofiles"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// Which components a change affects.
type affects struct{ authelia, caddy bool }

var (
	affectsCaddy = affects{caddy: true}
	affectsBoth  = affects{authelia: true, caddy: true}
)

// precheck validates the full candidate Caddy configuration that a change
// would produce, before anything is saved.
func (s *Server) precheck(ctx context.Context, mutate func(*caddy.Input)) error {
	if s.Configs == nil {
		return nil
	}
	in, err := s.Configs.CaddyInput(ctx)
	if err != nil {
		return err
	}
	mutate(&in)
	if !in.Settings.SetupComplete || in.Settings.ACMEEmail == "" {
		return nil
	}
	return caddy.Validate(in)
}

// commit applies the configurations affected by a saved change. When
// applying fails, undo restores the previous database state and the
// previous configuration is re-applied, so the database never drifts from
// what is actually running.
func (s *Server) commit(ctx context.Context, rc *reqCtx, reason string, a affects, undo func(context.Context) error) error {
	if s.Configs == nil {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	var err error
	if a.authelia {
		err = s.Configs.ApplyAuthelia(ctx, rc.actor(), reason)
	}
	if err == nil && a.caddy {
		err = s.Configs.ApplyCaddy(ctx, rc.actor(), reason)
	}
	if err == nil {
		return nil
	}
	if undo != nil {
		if uerr := undo(ctx); uerr != nil {
			s.Log.Error("undo after failed apply", "reason", reason, "err", uerr)
			return err
		}
		if a.authelia {
			_ = s.Configs.ApplyAuthelia(ctx, rc.actor(), "revert: "+reason)
		}
		if a.caddy {
			_ = s.Configs.ApplyCaddy(ctx, rc.actor(), "revert: "+reason)
		}
	}
	return err
}

// --- Overview, settings, audit ---------------------------------------------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	routes, err := s.Store.ListRoutes(ctx)
	if err != nil {
		return err
	}
	domains, err := s.Store.ListDomains(ctx)
	if err != nil {
		return err
	}
	profiles, err := s.Store.ListDNSProfiles(ctx)
	if err != nil {
		return err
	}
	stks, err := s.Store.ListStacks(ctx)
	if err != nil {
		return err
	}
	type alert struct {
		Level   string `json:"level"`
		Message string `json:"message"`
		Link    string `json:"link,omitempty"`
	}
	alerts := []alert{}
	st := rc.settings
	if st.ACMEStaging && st.ACMEDirectory == "" {
		alerts = append(alerts, alert{"warning", "Certificates come from the Let's Encrypt staging CA (not trusted by browsers). Switch to production in Settings once everything works.", "/settings"})
	}
	if len(domains) == 0 {
		alerts = append(alerts, alert{"info", "Add your first domain to start serving routes.", "/domains"})
	}
	if st.UIHost == "" {
		alerts = append(alerts, alert{"info", "The Marineris UI is only reachable through the SSH tunnel. Set a public UI host in Settings.", "/settings"})
	} else {
		inside := false
		for _, d := range domains {
			inside = inside || validate.HostInZone(st.UIHost, d.Name)
		}
		if !inside {
			alerts = append(alerts, alert{"warning", "The public UI host " + st.UIHost + " is not served yet: add its domain.", "/domains"})
		}
	}
	if !st.UIProtectWithAuthelia {
		alerts = append(alerts, alert{"warning", "The public Marineris UI is not protected by Authelia.", "/settings"})
	}
	for _, p := range profiles {
		if p.LastTestAt == nil {
			alerts = append(alerts, alert{"info", "DNS profile \"" + p.Name + "\" has never been tested.", "/dns"})
		} else if !p.LastTestOK {
			alerts = append(alerts, alert{"error", "DNS profile \"" + p.Name + "\" failed its last test.", "/dns"})
		}
	}
	for _, comp := range []string{configmgr.Caddy, configmgr.Authelia} {
		if vs, err := s.Store.ListConfigVersions(ctx, comp, 1); err == nil && len(vs) == 1 && vs[0].Status != store.ConfigApplied {
			alerts = append(alerts, alert{"error", "The last " + comp + " configuration change failed: " + vs[0].Error, "/config"})
		}
	}
	containers := map[string]int{"total": 0, "running": 0}
	dockerOK := false
	if s.Docker != nil {
		if list, err := s.Docker.List(ctx); err == nil {
			dockerOK = true
			for _, c := range list {
				containers["total"]++
				if c.State == "running" {
					containers["running"]++
				}
			}
		}
	}
	if !dockerOK {
		alerts = append(alerts, alert{"error", "Docker is not reachable through the socket proxy.", ""})
	}
	enabled := 0
	for _, rt := range routes {
		if rt.Enabled {
			enabled++
		}
	}
	return ok(w, map[string]any{
		"version":      s.Version,
		"containers":   containers,
		"docker_ok":    dockerOK,
		"routes":       map[string]int{"total": len(routes), "enabled": enabled},
		"domains":      len(domains),
		"dns_profiles": len(profiles),
		"stacks":       len(stks),
		"acme_staging": st.ACMEStaging,
		"alerts":       alerts,
	})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	return ok(w, rc.settings)
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		ACMEEmail             string          `json:"acme_email"`
		ACMEStaging           bool            `json:"acme_staging"`
		ACMEDirectory         string          `json:"acme_directory"`
		ACMETrustedRootFile   string          `json:"acme_trusted_root_file"`
		DNSResolvers          []string        `json:"dns_resolvers"`
		DNSPropagationTimeout string          `json:"dns_propagation_timeout"`
		UIHost                string          `json:"ui_host"`
		UIProtectWithAuthelia bool            `json:"ui_protect_with_authelia"`
		AuthSubdomain         string          `json:"auth_subdomain"`
		CrowdSecEnabled       bool            `json:"crowdsec_enabled"`
		DefaultRateLimit      store.RateLimit `json:"default_rate_limit"`
		LoginRateLimit        store.RateLimit `json:"login_rate_limit"`
		HomePage              string          `json:"home_page"`
		HubTitle              string          `json:"hub_title"`
		HubWidgets            []string        `json:"hub_widgets"`
		HubBackground         bool            `json:"hub_background"`
		// Round-tripped from GET but changed through PUT /api/branding.
		BrandName   string `json:"brand_name"`
		AccentColor string `json:"accent_color"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	email := strings.TrimSpace(req.ACMEEmail)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return fieldError("acme_email", "must be a valid e-mail address")
	}
	next := rc.settings
	next.ACMEEmail = email
	next.ACMEStaging = req.ACMEStaging
	next.ACMEDirectory = strings.TrimSpace(req.ACMEDirectory)
	next.ACMETrustedRootFile = strings.TrimSpace(req.ACMETrustedRootFile)
	next.DNSResolvers = req.DNSResolvers
	next.DNSPropagationTimeout = strings.TrimSpace(req.DNSPropagationTimeout)
	next.UIHost = strings.ToLower(strings.TrimSpace(req.UIHost))
	next.UIProtectWithAuthelia = req.UIProtectWithAuthelia
	next.AuthSubdomain = strings.ToLower(strings.TrimSpace(req.AuthSubdomain))
	next.CrowdSecEnabled = req.CrowdSecEnabled
	next.DefaultRateLimit = req.DefaultRateLimit
	next.LoginRateLimit = req.LoginRateLimit
	if req.HomePage != "" {
		if req.HomePage != "hub" && req.HomePage != "dashboard" {
			return fieldError("home_page", "must be hub or dashboard")
		}
		next.HomePage = req.HomePage
	}
	if req.HubTitle != "" || req.HubWidgets != nil {
		title := strings.TrimSpace(req.HubTitle)
		if utf8.RuneCountInString(title) > 64 {
			return fieldError("hub_title", "is limited to 64 characters")
		}
		next.HubTitle = title
		widgets := []string{}
		for _, w := range req.HubWidgets {
			if !validWidgets[w] {
				return fieldError("hub_widgets", "unknown widget "+w)
			}
			widgets = append(widgets, w)
		}
		next.HubWidgets = widgets
		next.HubBackground = req.HubBackground
	}
	ctx := r.Context()
	if next.UIHost != "" {
		if err := validate.Hostname(next.UIHost); err != nil {
			return fieldError("ui_host", err.Error())
		}
		ds, err := s.Store.ListDomains(ctx)
		if err != nil {
			return err
		}
		inside := false
		for _, d := range ds {
			inside = inside || validate.HostInZone(next.UIHost, d.Name)
		}
		if !inside {
			return fieldError("ui_host", "must belong to one of your domains (add the domain first)")
		}
	}
	if err := s.precheck(ctx, func(in *caddy.Input) { in.Settings = next }); err != nil {
		return err
	}
	prev := rc.settings
	if err := s.Store.PutSettings(ctx, next); err != nil {
		return err
	}
	s.audit(r, rc, "settings.update", "", true, settingsDiff(prev, next))
	if err := s.commit(ctx, rc, "settings changed", affectsBoth, func(ctx context.Context) error {
		return s.Store.PutSettings(ctx, prev)
	}); err != nil {
		return err
	}
	return ok(w, next)
}

func settingsDiff(a, b store.Settings) string {
	var out []string
	if a.ACMEStaging != b.ACMEStaging {
		if b.ACMEStaging {
			out = append(out, "ACME: staging")
		} else {
			out = append(out, "ACME: production")
		}
	}
	if a.UIHost != b.UIHost {
		out = append(out, "UI host: "+b.UIHost)
	}
	if a.UIProtectWithAuthelia != b.UIProtectWithAuthelia {
		out = append(out, "UI Authelia protection changed")
	}
	if a.CrowdSecEnabled != b.CrowdSecEnabled {
		out = append(out, "CrowdSec toggled")
	}
	return strings.Join(out, "; ")
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	entries, err := s.Store.ListAudit(r.Context(), 300)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []*store.AuditEntry{}
	}
	return ok(w, entries)
}

// handleAutheliaNotification shows the last message Authelia's filesystem
// notifier wrote (one-time codes used when enrolling 2FA in Authelia).
func (s *Server) handleAutheliaNotification(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	p := filepath.Join(s.Config.DataDir, "authelia-notify", "notification.txt")
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return ok(w, map[string]any{"present": false})
	}
	if err != nil {
		return err
	}
	f, err := os.Open(p) //nolint:gosec // fixed path below the data directory
	if err != nil {
		return err
	}
	defer f.Close()
	const max = 8 << 10
	if st.Size() > max {
		_, _ = f.Seek(st.Size()-max, 0)
	}
	b := make([]byte, max)
	n, _ := f.Read(b)
	return ok(w, map[string]any{"present": true, "modified_at": st.ModTime().UTC(), "content": string(b[:n])})
}

// --- DNS profiles ----------------------------------------------------------

func (s *Server) handleDNSProviders(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	return ok(w, dnsprofiles.Providers)
}

type dnsProfileView struct {
	*store.DNSProfile
	Config  map[string]any `json:"config"`
	Domains []string       `json:"domains"`
}

func (s *Server) dnsView(ctx context.Context, p *store.DNSProfile, domains []*store.Domain) dnsProfileView {
	v := dnsProfileView{DNSProfile: p, Domains: []string{}}
	if cfg, err := dnsprofiles.Open(s.Box, p.ID, p.Config); err == nil {
		v.Config = dnsprofiles.Redacted(p.Provider, cfg)
	}
	for _, d := range domains {
		if d.DNSProfileID != nil && *d.DNSProfileID == p.ID {
			v.Domains = append(v.Domains, d.Name)
		}
	}
	return v
}

func (s *Server) handleDNSList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	ps, err := s.Store.ListDNSProfiles(ctx)
	if err != nil {
		return err
	}
	ds, err := s.Store.ListDomains(ctx)
	if err != nil {
		return err
	}
	out := []dnsProfileView{}
	for _, p := range ps {
		out = append(out, s.dnsView(ctx, p, ds))
	}
	return ok(w, out)
}

type dnsRequest struct {
	Name     string             `json:"name"`
	Provider string             `json:"provider"`
	Config   dnsprofiles.Config `json:"config"`
}

func (s *Server) handleDNSCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req dnsRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validate.DisplayName(req.Name); err != nil {
		return fieldError("name", err.Error())
	}
	cfg, err := dnsprofiles.Normalize(req.Provider, req.Config, nil)
	if err != nil {
		return fieldError("config", err.Error())
	}
	p := &store.DNSProfile{Name: req.Name, Provider: req.Provider}
	if err := s.Store.CreateDNSProfile(r.Context(), p, func(id int64) (string, error) { return dnsprofiles.Seal(s.Box, id, cfg) }); err != nil {
		return err
	}
	s.audit(r, rc, "dns_profile.create", p.Name, true, p.Provider)
	return ok(w, s.dnsView(r.Context(), p, nil))
}

func (s *Server) handleDNSUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req dnsRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	p, err := s.Store.GetDNSProfile(ctx, id)
	if err != nil {
		return err
	}
	if req.Provider != "" && req.Provider != p.Provider {
		return fieldError("provider", "cannot be changed; create a new profile")
	}
	prevCfg, err := dnsprofiles.Open(s.Box, p.ID, p.Config)
	if err != nil {
		return err
	}
	cfg, err := dnsprofiles.Normalize(p.Provider, req.Config, prevCfg)
	if err != nil {
		return fieldError("config", err.Error())
	}
	name := strings.TrimSpace(req.Name)
	if err := validate.DisplayName(name); err != nil {
		return fieldError("name", err.Error())
	}
	sealed, err := dnsprofiles.Seal(s.Box, p.ID, cfg)
	if err != nil {
		return err
	}
	prev := *p
	p.Name, p.Config = name, sealed
	if err := s.Store.UpdateDNSProfile(ctx, p); err != nil {
		return err
	}
	s.audit(r, rc, "dns_profile.update", p.Name, true, "")
	if err := s.commit(ctx, rc, "DNS profile "+p.Name+" updated", affectsCaddy, func(ctx context.Context) error {
		return s.Store.UpdateDNSProfile(ctx, &prev)
	}); err != nil {
		return err
	}
	return ok(w, s.dnsView(ctx, p, nil))
}

func (s *Server) handleDNSDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteDNSProfile(r.Context(), id); err != nil {
		return err
	}
	s.audit(r, rc, "dns_profile.delete", r.PathValue("id"), true, "")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleDNSTest(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req struct {
		Zone string `json:"zone"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	zone := strings.ToLower(strings.TrimSpace(req.Zone))
	if err := validate.Zone(zone); err != nil {
		return fieldError("zone", err.Error())
	}
	ctx := r.Context()
	p, err := s.Store.GetDNSProfile(ctx, id)
	if err != nil {
		return err
	}
	cfg, err := dnsprofiles.Open(s.Box, p.ID, p.Config)
	if err != nil {
		return err
	}
	res := dnsprofiles.Test(ctx, p.Provider, cfg, zone)
	msg := strings.Join(res.Steps, "\n")
	if res.Error != "" {
		msg += "\n" + res.Error
	}
	if err := s.Store.RecordDNSTest(context.WithoutCancel(ctx), p.ID, res.OK, msg); err != nil {
		return err
	}
	s.audit(r, rc, "dns_profile.test", p.Name+" / "+zone, res.OK, res.Error)
	return ok(w, res)
}

// --- Domains & certificates ------------------------------------------------

type domainRequest struct {
	Name         string `json:"name"`
	DNSProfileID *int64 `json:"dns_profile_id"`
	Wildcard     bool   `json:"wildcard"`
}

func (s *Server) handleDomainList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ds, err := s.Store.ListDomains(r.Context())
	if err != nil {
		return err
	}
	if ds == nil {
		ds = []*store.Domain{}
	}
	return ok(w, ds)
}

func (s *Server) domainDNS(ctx context.Context, d *store.Domain) (*caddy.DNS, error) {
	if d.DNSProfileID == nil {
		return nil, nil
	}
	p, err := s.Store.GetDNSProfile(ctx, *d.DNSProfileID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fieldError("dns_profile_id", "no such DNS profile")
	}
	if err != nil {
		return nil, err
	}
	cfg, err := dnsprofiles.Open(s.Box, p.ID, p.Config)
	if err != nil {
		return nil, err
	}
	return &caddy.DNS{Provider: p.Provider, Config: cfg}, nil
}

func (s *Server) handleDomainCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req domainRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	d := &store.Domain{Name: strings.ToLower(strings.TrimSpace(req.Name)), DNSProfileID: req.DNSProfileID, Wildcard: req.Wildcard && req.DNSProfileID != nil}
	if err := validate.Zone(d.Name); err != nil {
		return fieldError("name", err.Error())
	}
	ctx := r.Context()
	dns, err := s.domainDNS(ctx, d)
	if err != nil {
		return err
	}
	if err := s.precheck(ctx, func(in *caddy.Input) {
		in.Domains = append(in.Domains, caddy.Domain{Name: d.Name, Wildcard: d.Wildcard, DNS: dns})
	}); err != nil {
		return err
	}
	if err := s.Store.CreateDomain(ctx, d); err != nil {
		return err
	}
	s.audit(r, rc, "domain.create", d.Name, true, "")
	if err := s.commit(ctx, rc, "domain "+d.Name+" added", affectsBoth, func(ctx context.Context) error {
		return s.Store.DeleteDomain(ctx, d.ID)
	}); err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) handleDomainUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req domainRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	d, err := s.Store.GetDomain(ctx, id)
	if err != nil {
		return err
	}
	if req.Name != "" && !strings.EqualFold(req.Name, d.Name) {
		return fieldError("name", "cannot be changed; add a new domain")
	}
	prev := *d
	d.DNSProfileID, d.Wildcard = req.DNSProfileID, req.Wildcard && req.DNSProfileID != nil
	dns, err := s.domainDNS(ctx, d)
	if err != nil {
		return err
	}
	if err := s.precheck(ctx, func(in *caddy.Input) {
		for i := range in.Domains {
			if in.Domains[i].Name == d.Name {
				in.Domains[i] = caddy.Domain{Name: d.Name, Wildcard: d.Wildcard, DNS: dns}
			}
		}
	}); err != nil {
		return err
	}
	if err := s.Store.UpdateDomain(ctx, d); err != nil {
		return err
	}
	s.audit(r, rc, "domain.update", d.Name, true, "")
	if err := s.commit(ctx, rc, "domain "+d.Name+" updated", affectsCaddy, func(ctx context.Context) error {
		return s.Store.UpdateDomain(ctx, &prev)
	}); err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) handleDomainDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	d, err := s.Store.GetDomain(ctx, id)
	if err != nil {
		return err
	}
	if err := s.precheck(ctx, func(in *caddy.Input) {
		var keep []caddy.Domain
		for _, x := range in.Domains {
			if x.Name != d.Name {
				keep = append(keep, x)
			}
		}
		in.Domains = keep
	}); err != nil {
		return err
	}
	if err := s.Store.DeleteDomain(ctx, id); err != nil {
		return err
	}
	s.audit(r, rc, "domain.delete", d.Name, true, "")
	if err := s.commit(ctx, rc, "domain "+d.Name+" removed", affectsBoth, func(ctx context.Context) error {
		return s.Store.CreateDomain(ctx, d)
	}); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	hosts := []string{}
	if rc.settings.UIHost != "" {
		hosts = append(hosts, rc.settings.UIHost)
	}
	ds, err := s.Store.ListDomains(ctx)
	if err != nil {
		return err
	}
	for _, d := range ds {
		hosts = append(hosts, rc.settings.AuthSubdomain+"."+d.Name)
	}
	rs, err := s.Store.ListRoutes(ctx)
	if err != nil {
		return err
	}
	for _, rt := range rs {
		if rt.Enabled {
			hosts = append(hosts, rt.Host)
		}
	}
	if s.Certs == nil {
		return ok(w, []any{})
	}
	return ok(w, s.Certs.CheckAll(ctx, hosts))
}

// --- Routes ----------------------------------------------------------------

type routeRequest struct {
	Name           string            `json:"name"`
	Host           string            `json:"host"`
	PathPrefix     string            `json:"path_prefix"`
	Kind           string            `json:"kind"`
	Target         store.RouteTarget `json:"target"`
	Auth           string            `json:"auth"`
	RateLimit      *store.RateLimit  `json:"rate_limit"`
	Enabled        bool              `json:"enabled"`
	ConnectNetwork bool              `json:"connect_network"`
	Hub            *store.HubEntry   `json:"hub"`
}

func (req *routeRequest) route() *store.Route {
	return &store.Route{
		Name: strings.TrimSpace(req.Name), Host: strings.ToLower(strings.TrimSpace(req.Host)),
		PathPrefix: strings.TrimSpace(req.PathPrefix), Kind: req.Kind, Target: req.Target,
		Auth: req.Auth, RateLimit: req.RateLimit, Enabled: req.Enabled,
		Hub: store.HubEntry{Visible: true},
	}
}

// applyHub copies the hub fields of the request (or keeps prev's).
func (req *routeRequest) applyHub(rt *store.Route, prev *store.Route) error {
	switch {
	case req.Hub != nil:
		rt.Hub = store.HubEntry{Visible: req.Hub.Visible, Title: strings.TrimSpace(req.Hub.Title), Subtitle: strings.TrimSpace(req.Hub.Subtitle)}
	case prev != nil:
		rt.Hub = prev.Hub
	}
	if utf8.RuneCountInString(rt.Hub.Title) > 64 || utf8.RuneCountInString(rt.Hub.Subtitle) > 120 {
		return fieldError("hub", "title is limited to 64 characters and subtitle to 120")
	}
	for _, r := range rt.Hub.Title + rt.Hub.Subtitle {
		if unicode.IsControl(r) {
			return fieldError("hub", "must not contain control characters")
		}
	}
	return nil
}

func (s *Server) handleRouteList(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	rs, err := s.Store.ListRoutes(r.Context())
	if err != nil {
		return err
	}
	if rs == nil {
		rs = []*store.Route{}
	}
	return ok(w, rs)
}

func (s *Server) prepareRoute(ctx context.Context, req *routeRequest, rt *store.Route, replaceID int64) error {
	if err := caddy.ValidateRoute(rt); err != nil {
		return &Error{Status: http.StatusBadRequest, Code: "invalid", Message: err.Error()}
	}
	if err := s.precheck(ctx, func(in *caddy.Input) {
		var keep []*store.Route
		for _, x := range in.Routes {
			if x.ID != replaceID {
				keep = append(keep, x)
			}
		}
		in.Routes = append(keep, rt)
	}); err != nil {
		return err
	}
	if rt.Kind == store.RouteContainer && req.ConnectNetwork && s.Docker != nil {
		if err := s.Docker.ConnectToApps(ctx, rt.Target.Container); err != nil {
			return &Error{Status: http.StatusBadGateway, Code: "docker", Message: "could not attach the container to the apps network: " + err.Error()}
		}
	}
	return nil
}

func (s *Server) handleRouteCreate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req routeRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	rt := req.route()
	if err := req.applyHub(rt, nil); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.prepareRoute(ctx, &req, rt, 0); err != nil {
		return err
	}
	if err := s.Store.CreateRoute(ctx, rt); err != nil {
		return err
	}
	s.audit(r, rc, "route.create", rt.Name, true, rt.Host+rt.PathPrefix)
	a := affectsCaddy
	if store.UsesAuthelia(rt.Auth) {
		a = affectsBoth
	}
	if err := s.commit(ctx, rc, "route "+rt.Name+" added", a, func(ctx context.Context) error {
		return s.Store.DeleteRoute(ctx, rt.ID)
	}); err != nil {
		return err
	}
	return ok(w, rt)
}

func (s *Server) handleRouteUpdate(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	var req routeRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	prev, err := s.Store.GetRoute(ctx, id)
	if err != nil {
		return err
	}
	rt := req.route()
	rt.ID = id
	if err := req.applyHub(rt, prev); err != nil {
		return err
	}
	if err := s.prepareRoute(ctx, &req, rt, id); err != nil {
		return err
	}
	if err := s.Store.UpdateRoute(ctx, rt); err != nil {
		return err
	}
	s.audit(r, rc, "route.update", rt.Name, true, rt.Host+rt.PathPrefix)
	a := affectsCaddy
	if rt.Auth != prev.Auth || rt.Host != prev.Host || rt.PathPrefix != prev.PathPrefix || rt.Enabled != prev.Enabled {
		a = affectsBoth
	}
	if err := s.commit(ctx, rc, "route "+rt.Name+" updated", a, func(ctx context.Context) error {
		return s.Store.UpdateRoute(ctx, prev)
	}); err != nil {
		return err
	}
	return ok(w, rt)
}

func (s *Server) handleRouteDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	rt, err := s.Store.GetRoute(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteRoute(ctx, id); err != nil {
		return err
	}
	s.audit(r, rc, "route.delete", rt.Name, true, rt.Host+rt.PathPrefix)
	if err := s.commit(ctx, rc, "route "+rt.Name+" removed", affectsBoth, func(ctx context.Context) error {
		return s.Store.CreateRoute(ctx, rt)
	}); err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

// --- Configuration history -------------------------------------------------

func component(r *http.Request) (string, error) {
	c := r.PathValue("component")
	if c != configmgr.Caddy && c != configmgr.Authelia {
		return "", &Error{Status: http.StatusNotFound, Code: "not_found", Message: "unknown component"}
	}
	return c, nil
}

func (s *Server) handleConfigCurrent(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	c, err := component(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var content []byte
	if c == configmgr.Caddy {
		content, err = s.Configs.RenderCaddy(ctx)
		if err != nil {
			return err
		}
	} else {
		cfgB, _ := os.ReadFile(filepath.Join(s.Config.AutheliaDir(), "configuration.yml"))
		usersB, _ := os.ReadFile(filepath.Join(s.Config.AutheliaDir(), "users_database.yml"))
		content = append(append(cfgB, []byte("\n---\n")...), usersB...)
	}
	return ok(w, map[string]string{"component": c, "content": string(s.Configs.Redact(ctx, content))})
}

func (s *Server) handleConfigHistory(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	c, err := component(r)
	if err != nil {
		return err
	}
	vs, err := s.Store.ListConfigVersions(r.Context(), c, configmgr.HistoryKeep)
	if err != nil {
		return err
	}
	if vs == nil {
		vs = []*store.ConfigVersion{}
	}
	return ok(w, vs)
}

func (s *Server) handleConfigVersion(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	v, err := s.Store.GetConfigVersion(ctx, id)
	if err != nil {
		return err
	}
	content, err := s.Configs.Content(v)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"version": v, "content": string(s.Configs.Redact(ctx, content))})
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	id, err := idParam(r)
	if err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	v, err := s.Store.GetConfigVersion(ctx, id)
	if err != nil {
		return err
	}
	if v.Component == configmgr.Caddy {
		err = s.Configs.RollbackCaddy(ctx, rc.actor(), id)
	} else {
		err = s.Configs.RollbackAuthelia(ctx, rc.actor(), id)
	}
	s.audit(r, rc, "config.rollback", v.Component, err == nil, "")
	if err != nil {
		return err
	}
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) handleConfigApply(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	err := s.commit(r.Context(), rc, "manual apply", affectsBoth, nil)
	s.audit(r, rc, "config.apply", "", err == nil, "")
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"ok": true, "at": time.Now().UTC()})
}
