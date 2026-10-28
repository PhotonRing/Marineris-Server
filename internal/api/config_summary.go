package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/photonring/marineris-server/internal/authelia"
	"github.com/photonring/marineris-server/internal/caddy"
	"github.com/photonring/marineris-server/internal/store"
)

// The Proxy & SSO page shows what Caddy and Authelia are configured to do,
// in plain terms, next to the generated files. Each item links to the page
// that changes it: the configuration is generated from those pages.

type site struct {
	Address   string `json:"address"` // host + path prefix
	Role      string `json:"role"`    // "route", "ui" or "portal"
	Name      string `json:"name"`
	Target    string `json:"target"`
	Access    string `json:"access"` // none, authelia, admins
	RateLimit string `json:"rate_limit"`
	TLS       string `json:"tls"`
	Enabled   bool   `json:"enabled"`
	Link      string `json:"link"`
}

type accessRule struct {
	Domains   []string `json:"domains"`
	Resources []string `json:"resources"`
	Subjects  []string `json:"subjects"`
	Policy    string   `json:"policy"`
}

func rateText(rl *store.RateLimit) string {
	if rl == nil || rl.Events == 0 {
		return ""
	}
	return strconv.Itoa(rl.Events) + " / " + rl.Window
}

func routeTarget(rt *store.Route) string {
	switch rt.Kind {
	case store.RouteContainer:
		return "container " + rt.Target.Container + ":" + strconv.Itoa(rt.Target.Port)
	case store.RouteExternal:
		return rt.Target.URL
	case store.RouteSPA:
		return "single-page app · " + rt.Target.Folder
	}
	return "static folder · " + rt.Target.Folder
}

// certFor describes the certificate a host gets.
func certFor(host string, domains []caddy.Domain) string {
	best := caddy.Domain{}
	for _, d := range domains {
		if (host == d.Name || strings.HasSuffix(host, "."+d.Name)) && len(d.Name) > len(best.Name) {
			best = d
		}
	}
	switch {
	case best.Name == "":
		return "none (not in a managed domain)"
	case best.Wildcard && best.DNS != nil && host != best.Name && strings.Count(host, ".") == strings.Count(best.Name, ".")+1:
		return "wildcard *." + best.Name + " (DNS-01)"
	case best.DNS != nil:
		return "own certificate (DNS-01)"
	}
	return "own certificate (HTTP-01)"
}

func (s *Server) handleConfigSummary(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	ctx := r.Context()
	cin, err := s.Configs.CaddyInput(ctx)
	if err != nil {
		return err
	}
	st := cin.Settings
	ca := "Let's Encrypt"
	switch {
	case st.ACMEDirectory != "":
		ca = st.ACMEDirectory
	case st.ACMEStaging:
		ca = "Let's Encrypt staging (untrusted certificates)"
	}
	sites := []site{}
	if st.UIHost != "" {
		access := "none"
		if st.UIProtectWithAuthelia {
			access = store.AuthAdmins
		}
		sites = append(sites, site{Address: st.UIHost, Role: "ui", Name: "Marineris", Target: "Marineris web UI",
			Access: access, RateLimit: rateText(&st.LoginRateLimit) + " (login)", TLS: certFor(st.UIHost, cin.Domains), Enabled: true, Link: "/settings"})
	}
	for _, d := range cin.Domains {
		host := st.AuthSubdomain + "." + d.Name
		sites = append(sites, site{Address: host, Role: "portal", Name: "Authelia portal", Target: "Authelia login portal",
			Access: "none", TLS: certFor(host, cin.Domains), Enabled: true, Link: "/domains"})
	}
	routes := append([]*store.Route(nil), cin.Routes...)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Host+routes[i].PathPrefix < routes[j].Host+routes[j].PathPrefix })
	for _, rt := range routes {
		rl := rt.RateLimit
		if rl == nil {
			rl = &st.DefaultRateLimit
		}
		sites = append(sites, site{Address: rt.Host + rt.PathPrefix, Role: "route", Name: rt.Name, Target: routeTarget(rt),
			Access: rt.Auth, RateLimit: rateText(rl), TLS: certFor(rt.Host, cin.Domains), Enabled: rt.Enabled, Link: "/routes"})
	}

	ain, err := s.Configs.AutheliaInput(ctx)
	if err != nil {
		return err
	}
	cfg, _, err := authelia.Render(ain)
	if err != nil {
		return err
	}
	var doc struct {
		AccessControl struct {
			DefaultPolicy string `yaml:"default_policy"`
			Rules         []struct {
				Domain    []string `yaml:"domain"`
				Resources []string `yaml:"resources"`
				Subject   []string `yaml:"subject"`
				Policy    string   `yaml:"policy"`
			} `yaml:"rules"`
		} `yaml:"access_control"`
		Session struct {
			Expiration string `yaml:"expiration"`
			Inactivity string `yaml:"inactivity"`
			Cookies    []struct {
				Domain string `yaml:"domain"`
				URL    string `yaml:"authelia_url"`
			} `yaml:"cookies"`
		} `yaml:"session"`
		Regulation struct {
			MaxRetries int    `yaml:"max_retries" json:"max_retries"`
			FindTime   string `yaml:"find_time" json:"find_time"`
			BanTime    string `yaml:"ban_time" json:"ban_time"`
		} `yaml:"regulation"`
	}
	if err := yaml.Unmarshal(cfg, &doc); err != nil {
		return err
	}
	rules := []accessRule{}
	for _, ru := range doc.AccessControl.Rules {
		if len(ru.Domain) == 1 && ru.Domain[0] == "deny."+authelia.PlaceholderDomain {
			continue // placeholder keeping the configuration valid
		}
		rules = append(rules, accessRule{Domains: ru.Domain, Resources: nonNil(ru.Resources), Subjects: nonNil(ru.Subject), Policy: ru.Policy})
	}
	portals := []map[string]string{}
	for _, c := range doc.Session.Cookies {
		if c.Domain != authelia.PlaceholderDomain {
			portals = append(portals, map[string]string{"domain": c.Domain, "url": c.URL})
		}
	}
	users := []map[string]any{}
	for _, u := range ain.Users {
		users = append(users, map[string]any{"username": u.Username, "groups": nonNil(u.Groups), "disabled": u.Disabled})
	}
	return ok(w, map[string]any{
		"caddy": map[string]any{
			"ca": ca, "acme_email": st.ACMEEmail, "crowdsec": st.CrowdSecEnabled,
			"default_rate_limit": rateText(&st.DefaultRateLimit), "sites": sites,
		},
		"authelia": map[string]any{
			"default_policy": doc.AccessControl.DefaultPolicy, "rules": rules, "portals": portals, "users": users,
			"second_factors": []string{"TOTP", "Passkeys (WebAuthn)"},
			"session":        map[string]string{"expiration": doc.Session.Expiration, "inactivity": doc.Session.Inactivity},
			"regulation":     doc.Regulation,
		},
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
