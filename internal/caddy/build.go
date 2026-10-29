// Package caddy renders the Caddy JSON configuration from Marineris state and
// applies it through Caddy's admin API.
package caddy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/photonring/marineris-server/internal/dnsprofiles"
	"github.com/photonring/marineris-server/internal/store"
	"github.com/photonring/marineris-server/internal/validate"
)

// ACME directories.
const (
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStaging    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Headers copied from Authelia's forward-auth response to the upstream.
// PortalPasskeyPath is the path, on the Authelia portal's hosts, of the
// Marineris page that adds Authelia passkeys.
const PortalPasskeyPath = "/marineris-passkey"

// PortalLinkPath is the path, on the Authelia portal of the UI's domain
// (UIPortal), of the Marineris page that opens Invites and Reset links: the
// invited person is not signed in, and the UI's host may be behind Authelia.
const PortalLinkPath = "/marineris-link"

var forwardAuthHeaders = []string{"Remote-User", "Remote-Groups", "Remote-Email", "Remote-Name"}

// Domain is a managed domain with its (decrypted) DNS profile, if any.
type Domain struct {
	Name     string
	Wildcard bool
	DNS      *DNS
}

// DNS is the provider configuration of a domain.
type DNS struct {
	Provider string
	Config   dnsprofiles.Config
}

// Env describes the deployment: where the other services are reachable.
type Env struct {
	AdminListen       string // e.g. unix//run/caddy-admin/admin.sock
	ProbeListen       string // e.g. :2020
	AccessLog         string // e.g. /var/log/caddy/access.log
	MarinerisUpstream string // e.g. marineris:8080
	AutheliaUpstream  string // e.g. authelia:9091
	CrowdSecURL       string // e.g. http://crowdsec:8080
	BouncerKey        string
	StaticRoot        string // static folders root inside the Caddy container
}

// Input is everything the configuration is rendered from.
type Input struct {
	Env      Env
	Settings store.Settings
	Domains  []Domain
	Routes   []*store.Route
}

// ValidationError lists every problem found in the input.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

// obj is a JSON object.
type obj = map[string]any

// Build validates the input and renders Caddy's JSON configuration. The output
// is deterministic for a given input.
func Build(in Input) ([]byte, error) {
	if err := Validate(in); err != nil {
		return nil, err
	}
	cfg := obj{
		"admin": obj{
			"listen": in.Env.AdminListen,
			"config": obj{"persist": true},
		},
		"logging": logging(in.Env),
		"apps":    apps(in),
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// Validate checks the input for consistency.
func Validate(in Input) error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if in.Env.AdminListen == "" || in.Env.MarinerisUpstream == "" || in.Env.AutheliaUpstream == "" {
		add("deployment environment incomplete")
	}
	if in.Settings.ACMEEmail == "" {
		add("ACME e-mail is not set")
	}
	if d := in.Settings.ACMEDirectory; d != "" && !strings.HasPrefix(d, "https://") {
		add("ACME directory must be an https:// URL")
	}
	if f := in.Settings.ACMETrustedRootFile; f != "" && (!path.IsAbs(f) || path.Clean(f) != f || strings.ContainsAny(f, "{}\"\\ ")) {
		add("ACME trusted root must be a clean absolute path")
	}
	for _, r := range in.Settings.DNSResolvers {
		if _, port, err := net.SplitHostPort(r); err != nil || port == "" {
			add("DNS resolver %q must be host:port", r)
		}
	}
	if t := in.Settings.DNSPropagationTimeout; t != "" && t != "-1" && validate.Duration(t) != nil {
		add("DNS propagation timeout must be a duration or -1")
	}
	for _, rl := range []store.RateLimit{in.Settings.DefaultRateLimit, in.Settings.LoginRateLimit} {
		if err := checkRateLimit(rl); err != nil {
			add("rate limit: %v", err)
		}
	}
	if err := validate.Hostname(in.Settings.AuthSubdomain + ".example.com"); err != nil || strings.Contains(in.Settings.AuthSubdomain, ".") {
		add("auth subdomain must be a single DNS label")
	}

	zones := map[string]bool{}
	for _, d := range in.Domains {
		if err := validate.Zone(d.Name); err != nil {
			add("domain %q: %v", d.Name, err)
		}
		if zones[d.Name] {
			add("domain %q listed twice", d.Name)
		}
		zones[d.Name] = true
		if d.DNS != nil {
			if _, err := dnsprofiles.Normalize(d.DNS.Provider, d.DNS.Config, nil); err != nil {
				add("domain %q: DNS profile: %v", d.Name, err)
			}
		} else if d.Wildcard {
			add("domain %q: wildcard certificates need a DNS profile", d.Name)
		}
	}
	for _, d := range in.Domains {
		for _, o := range in.Domains {
			if o.Name != d.Name && validate.HostInZone(d.Name, o.Name) {
				add("domain %q is nested in %q; manage only one of them", d.Name, o.Name)
			}
		}
	}
	inZone := func(h string) bool {
		for z := range zones {
			if validate.HostInZone(h, z) {
				return true
			}
		}
		return false
	}

	reserved := map[string]string{}
	if h := in.Settings.UIHost; h != "" {
		// A UI host outside every managed domain is not an error: it is
		// set during first-run setup, before any domain exists, and the
		// UI route is simply not served until its domain is added.
		if err := validate.Hostname(h); err != nil {
			add("UI host: %v", err)
		}
		reserved[h] = "the Marineris UI"
	}
	for z := range zones {
		reserved[in.Settings.AuthSubdomain+"."+z] = "the Authelia portal"
	}

	seen := map[string]string{}
	for _, r := range in.Routes {
		if !r.Enabled {
			continue
		}
		where := fmt.Sprintf("route %q", r.Name)
		if err := ValidateRoute(r); err != nil {
			add("%s: %v", where, err)
			continue
		}
		if !inZone(r.Host) {
			add("%s: host %q is not inside a managed domain", where, r.Host)
		}
		if what, ok := reserved[r.Host]; ok {
			add("%s: host %q is reserved for %s", where, r.Host, what)
		}
		key := r.Host + r.PathPrefix
		if other, dup := seen[key]; dup {
			add("%s: same host and path as route %q", where, other)
		}
		seen[key] = r.Name
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// ValidateRoute checks a single route.
func ValidateRoute(r *store.Route) error {
	if err := validate.DisplayName(r.Name); err != nil {
		return fmt.Errorf("name %w", err)
	}
	if err := validate.Hostname(r.Host); err != nil {
		return fmt.Errorf("host %w", err)
	}
	if err := validate.PathPrefix(r.PathPrefix); err != nil {
		return fmt.Errorf("path prefix %w", err)
	}
	switch r.Auth {
	case store.AuthNone, store.AuthAuthelia, store.AuthAdmins:
	default:
		return fmt.Errorf("unknown access policy %q", r.Auth)
	}
	if r.RateLimit != nil {
		if err := checkRateLimit(*r.RateLimit); err != nil {
			return fmt.Errorf("rate limit: %w", err)
		}
	}
	t := r.Target
	switch r.Kind {
	case store.RouteContainer:
		if err := validate.ContainerName(t.Container); err != nil {
			return fmt.Errorf("container %w", err)
		}
		if err := validate.Port(t.Port); err != nil {
			return fmt.Errorf("port %w", err)
		}
		if t.URL != "" || t.Folder != "" || t.TLSInsecureSkipVerify {
			return errors.New("container routes only take a container, port and upstream TLS flag")
		}
	case store.RouteExternal:
		if _, _, err := validate.UpstreamURL(t.URL); err != nil {
			return fmt.Errorf("upstream URL %w", err)
		}
		if t.Container != "" || t.Folder != "" || t.Port != 0 {
			return errors.New("external routes only take a URL")
		}
	case store.RouteStatic, store.RouteSPA:
		if err := validate.FolderName(t.Folder); err != nil {
			return fmt.Errorf("folder %w", err)
		}
		if t.Container != "" || t.URL != "" || t.Port != 0 || t.UpstreamTLS || t.TLSInsecureSkipVerify {
			return errors.New("static routes only take a folder")
		}
	default:
		return fmt.Errorf("unknown route kind %q", r.Kind)
	}
	return nil
}

func checkRateLimit(rl store.RateLimit) error {
	if rl.Events == 0 {
		return nil
	}
	if rl.Events < 1 || rl.Events > 1_000_000 {
		return errors.New("events must be between 1 and 1000000")
	}
	return validate.Duration(rl.Window)
}

func logging(env Env) obj {
	logs := obj{
		"default": obj{"level": "INFO", "exclude": []string{"http.log.access.access"}},
	}
	if env.AccessLog != "" {
		logs["access"] = obj{
			"writer":  obj{"output": "file", "filename": env.AccessLog, "mode": "0640", "roll_size_mb": 50, "roll_keep": 5},
			"encoder": obj{"format": "json"},
			"include": []string{"http.log.access.access"},
		}
	}
	return obj{"logs": logs}
}

func apps(in Input) obj {
	a := obj{
		"http": obj{"servers": servers(in)},
		"tls":  tlsApp(in),
	}
	if crowdsecOn(in) {
		a["crowdsec"] = obj{
			"api_url":          in.Env.CrowdSecURL,
			"api_key":          in.Env.BouncerKey,
			"ticker_interval":  "15s",
			"enable_streaming": true,
		}
	}
	return a
}

func crowdsecOn(in Input) bool {
	return in.Settings.CrowdSecEnabled && in.Env.CrowdSecURL != "" && in.Env.BouncerKey != ""
}

func servers(in Input) obj {
	var routes []obj
	// Marineris UI.
	if h := in.Settings.UIHost; UIServed(in) {
		var chain []obj
		chain = append(chain, shield(in, "marineris_ui", nil)...)
		if rl := in.Settings.LoginRateLimit; rl.Events > 0 {
			chain = append(chain, obj{
				"handler": "subroute",
				"routes": []obj{{
					"match":  []obj{{"path": []string{"/api/auth/*", "/api/setup/*"}}},
					"handle": []obj{rateLimit("marineris_login", rl)},
				}},
			})
		}
		// Marineris trusts these headers as the Authelia sign-in: they only
		// ever come from Authelia's answer (forwardAuth deletes the client's).
		if in.Settings.UIProtectWithAuthelia {
			chain = append(chain, forwardAuth(in.Env.AutheliaUpstream))
		} else {
			chain = append(chain, obj{"handler": "headers", "request": obj{"delete": forwardAuthHeaders}})
		}
		chain = append(chain, obj{"handler": "reverse_proxy", "upstreams": []obj{{"dial": in.Env.MarinerisUpstream}}})
		routes = append(routes, hostRoute([]string{h}, "", chain))
	}
	// Authelia portal, one per domain.
	var portals []string
	for _, d := range in.Domains {
		portals = append(portals, in.Settings.AuthSubdomain+"."+d.Name)
	}
	if len(portals) > 0 {
		sort.Strings(portals)
		// The page that adds an Authelia passkey from the Marineris UI is
		// Marineris': a passkey belongs to the host it is created on.
		page := func() []obj {
			p := shield(in, "authelia_portal", nil)
			return append(p,
				obj{"handler": "headers", "request": obj{"delete": forwardAuthHeaders}},
				obj{"handler": "reverse_proxy", "upstreams": []obj{{"dial": in.Env.MarinerisUpstream}}})
		}
		if UIServed(in) && in.Settings.UIProtectWithAuthelia {
			routes = append(routes, hostRoute(portals, PortalPasskeyPath, page()))
		}
		// Invites and Reset links open on the portal of the UI's domain,
		// whether or not Authelia protects the UI.
		names := make([]string, 0, len(in.Domains))
		for _, d := range in.Domains {
			names = append(names, d.Name)
		}
		if p := UIPortal(in.Settings, names); p != "" {
			routes = append(routes, hostRoute([]string{p}, PortalLinkPath, page()))
		}
		chain := shield(in, "authelia_portal", nil)
		chain = append(chain, obj{"handler": "reverse_proxy", "upstreams": []obj{{"dial": in.Env.AutheliaUpstream}}})
		routes = append(routes, hostRoute(portals, "", chain))
	}
	// User routes: most specific path first within a host.
	rs := make([]*store.Route, 0, len(in.Routes))
	for _, r := range in.Routes {
		if r.Enabled {
			rs = append(rs, r)
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Host != rs[j].Host {
			return rs[i].Host < rs[j].Host
		}
		return len(rs[i].PathPrefix) > len(rs[j].PathPrefix)
	})
	for _, r := range rs {
		chain := shield(in, fmt.Sprintf("route_%d", r.ID), r.RateLimit)
		if store.UsesAuthelia(r.Auth) {
			chain = append(chain, forwardAuth(in.Env.AutheliaUpstream))
		}
		chain = append(chain, target(in.Env, r)...)
		routes = append(routes, hostRoute([]string{r.Host}, r.PathPrefix, chain))
	}
	// Unknown hosts and paths get a plain 404.
	routes = append(routes, obj{"handle": []obj{{"handler": "static_response", "status_code": 404}}, "terminal": true})

	edge := obj{
		"listen": []string{":443"},
		"routes": routes,
	}
	// Hosts covered by a managed wildcard certificate must not get their own
	// certificate: skip them explicitly rather than relying on the wildcard
	// already being loaded (it is not, on first start).
	if skip := coveredHosts(in, portals); len(skip) > 0 {
		edge["automatic_https"] = obj{"skip_certificates": skip}
	}
	if in.Env.AccessLog != "" {
		edge["logs"] = obj{"default_logger_name": "access"}
	}
	out := obj{"edge": edge}
	if in.Env.ProbeListen != "" {
		out["probe"] = obj{
			"listen":          []string{in.Env.ProbeListen},
			"automatic_https": obj{"disable": true},
			"routes": []obj{{
				"handle": []obj{{"handler": "static_response", "status_code": 200, "body": "ok"}},
			}},
		}
	}
	return out
}

// UIServed reports whether the public UI route is rendered: a UI host is
// set and belongs to a managed domain.
func UIServed(in Input) bool {
	h := in.Settings.UIHost
	if h == "" {
		return false
	}
	for _, d := range in.Domains {
		if validate.HostInZone(h, d.Name) {
			return true
		}
	}
	return false
}

// UIPortal returns the host of the Authelia portal of the UI's domain (the
// most specific managed domain holding the UI's host), where Invites and
// Reset links open; "" when the UI is on no managed domain.
func UIPortal(st store.Settings, domains []string) string {
	zone := ""
	for _, d := range domains {
		if st.UIHost != "" && validate.HostInZone(st.UIHost, d) && len(d) > len(zone) {
			zone = d
		}
	}
	if zone == "" {
		return ""
	}
	return st.AuthSubdomain + "." + zone
}

// coveredHosts lists the served hosts that a managed wildcard (or apex)
// certificate already covers.
func coveredHosts(in Input, portals []string) []string {
	hosts := map[string]bool{}
	if UIServed(in) {
		hosts[in.Settings.UIHost] = true
	}
	for _, p := range portals {
		hosts[p] = true
	}
	for _, r := range in.Routes {
		if r.Enabled {
			hosts[r.Host] = true
		}
	}
	var out []string
	for h := range hosts {
		for _, d := range in.Domains {
			if d.DNS != nil && d.Wildcard && (h == d.Name || validate.IsDirectChild(h, d.Name)) {
				out = append(out, h)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// shield returns the CrowdSec and rate-limit handlers for a route. Rate-limit
// zones are global in Caddy, so each route gets its own zone name.
func shield(in Input, zone string, override *store.RateLimit) []obj {
	var chain []obj
	if crowdsecOn(in) {
		chain = append(chain, obj{"handler": "crowdsec"})
	}
	rl := in.Settings.DefaultRateLimit
	if override != nil {
		rl = *override
	}
	if rl.Events > 0 {
		chain = append(chain, rateLimit(zone, rl))
	}
	return chain
}

func rateLimit(zone string, rl store.RateLimit) obj {
	return obj{
		"handler": "rate_limit",
		"rate_limits": obj{
			zone: obj{"key": "{http.request.remote.host}", "window": rl.Window, "max_events": rl.Events},
		},
	}
}

func hostRoute(hosts []string, prefix string, chain []obj) obj {
	m := obj{"host": hosts}
	if prefix != "" {
		m["path"] = []string{prefix, prefix + "/*"}
	}
	return obj{
		"match":    []obj{m},
		"handle":   []obj{{"handler": "subroute", "routes": []obj{{"handle": chain}}}},
		"terminal": true,
	}
}

// forwardAuth mirrors Caddy's forward_auth directive: ask Authelia whether the
// request is allowed; on 2xx copy the identity headers to the upstream request,
// otherwise return Authelia's response (redirect to the portal).
//
// The identity headers are always deleted from the client request first, so
// a client can never inject its own Remote-User.
func forwardAuth(authelia string) obj {
	var copyRoutes []obj
	copyRoutes = append(copyRoutes, obj{
		"handle": []obj{{"handler": "headers", "request": obj{"delete": forwardAuthHeaders}}},
	})
	for _, h := range forwardAuthHeaders {
		ph := "{http.reverse_proxy.header." + h + "}"
		copyRoutes = append(copyRoutes, obj{
			"match":  []obj{{"not": []obj{{"vars": obj{ph: []string{""}}}}}},
			"handle": []obj{{"handler": "headers", "request": obj{"set": obj{h: []string{ph}}}}},
		})
	}
	return obj{
		"handler":   "reverse_proxy",
		"upstreams": []obj{{"dial": authelia}},
		"rewrite":   obj{"method": "GET", "uri": "/api/authz/forward-auth"},
		"headers": obj{"request": obj{"set": obj{
			"X-Forwarded-Method": []string{"{http.request.method}"},
			"X-Forwarded-Uri":    []string{"{http.request.uri}"},
		}}},
		"handle_response": []obj{{
			"match":  obj{"status_code": []int{2}},
			"routes": copyRoutes,
		}},
	}
}

func target(env Env, r *store.Route) []obj {
	t := r.Target
	switch r.Kind {
	case store.RouteContainer:
		h := obj{"handler": "reverse_proxy", "upstreams": []obj{{"dial": net.JoinHostPort(strings.TrimPrefix(t.Container, "/"), strconv.Itoa(t.Port))}}}
		if t.UpstreamTLS {
			h["transport"] = obj{"protocol": "http", "tls": obj{}}
		}
		return []obj{h}
	case store.RouteExternal:
		dial, tls, _ := validate.UpstreamURL(t.URL)
		h := obj{
			"handler":   "reverse_proxy",
			"upstreams": []obj{{"dial": dial}},
			"headers":   obj{"request": obj{"set": obj{"Host": []string{"{http.reverse_proxy.upstream.hostport}"}}}},
		}
		if tls {
			tr := obj{}
			if t.TLSInsecureSkipVerify {
				tr["insecure_skip_verify"] = true
			}
			h["transport"] = obj{"protocol": "http", "tls": tr}
		}
		return []obj{h}
	case store.RouteStatic, store.RouteSPA:
		root := path.Join(env.StaticRoot, t.Folder)
		fs := obj{"handler": "file_server", "root": root, "hide": []string{".*"}}
		if r.Kind == store.RouteStatic {
			return []obj{fs}
		}
		return []obj{{
			"handler": "subroute",
			"routes": []obj{
				{
					"match": []obj{{"file": obj{
						"root":      root,
						"try_files": []string{"{http.request.uri.path}", "{http.request.uri.path}/", "/index.html"},
					}}},
					"handle": []obj{{"handler": "rewrite", "uri": "{http.matchers.file.relative}"}},
				},
				{"handle": []obj{fs}},
			},
		}}
	}
	return nil
}

func tlsApp(in Input) obj {
	ca := LetsEncryptProduction
	if in.Settings.ACMEStaging {
		ca = LetsEncryptStaging
	}
	if in.Settings.ACMEDirectory != "" {
		ca = in.Settings.ACMEDirectory
	}
	issuer := func(dns *DNS) obj {
		is := obj{"module": "acme", "ca": ca, "email": in.Settings.ACMEEmail}
		if f := in.Settings.ACMETrustedRootFile; f != "" {
			is["trusted_roots_pem_files"] = []string{f}
		}
		if dns != nil {
			provider, _ := dnsprofiles.CaddyProvider(dns.Provider, dns.Config)
			ch := obj{"provider": provider}
			if len(in.Settings.DNSResolvers) > 0 {
				ch["resolvers"] = in.Settings.DNSResolvers
			}
			switch t := in.Settings.DNSPropagationTimeout; t {
			case "":
			case "-1":
				ch["propagation_timeout"] = -1
			default:
				ch["propagation_timeout"] = t
			}
			is["challenges"] = obj{"dns": ch}
		}
		return is
	}
	var policies []obj
	var automate []string
	for _, d := range in.Domains {
		if d.DNS == nil {
			continue
		}
		policies = append(policies, obj{
			"subjects": []string{d.Name, "*." + d.Name},
			"issuers":  []obj{issuer(d.DNS)},
		})
		if d.Wildcard {
			automate = append(automate, d.Name, "*."+d.Name)
		}
	}
	// Everything else (domains without a DNS profile, deeper subdomains)
	// goes through HTTP-01 / TLS-ALPN-01.
	policies = append(policies, obj{"issuers": []obj{issuer(nil)}})
	out := obj{"automation": obj{"policies": policies}}
	if len(automate) > 0 {
		out["certificates"] = obj{"automate": automate}
	}
	return out
}
