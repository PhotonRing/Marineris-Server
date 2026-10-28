package authelia

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/photonring/marineris-server/internal/store"
)

func input() Input {
	st := store.DefaultSettings()
	return Input{
		Settings: st,
		Domains:  []string{"friend.org", "example.com"},
		Protected: []Resource{
			{Host: "media.example.com"}, {Host: "marineris.example.com", AdminOnly: true}, {Host: "media.example.com"},
		},
		Users: []User{{
			Username:     "admin",
			Email:        "ops@example.com",
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$7HaZUTElEKhzWhfeyxxt0w$T4isZDdylQMx/ZZO5CmOPmqFw2Ak6II5wAxIq9BcZNU",
			Groups:       []string{"admins"},
		}},
	}
}

func TestRenderSecureDefaults(t *testing.T) {
	cfg, users, err := Render(input())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(cfg, &doc); err != nil {
		t.Fatal(err)
	}
	ac := doc["access_control"].(map[string]any)
	if ac["default_policy"] != "deny" {
		t.Fatal("default policy must be deny")
	}
	rules := ac["rules"].([]any)
	if len(rules) != 3 {
		t.Fatalf("want admins-allow, deny and two_factor rules, got %v", rules)
	}
	for _, r := range rules {
		if r.(map[string]any)["policy"] == "bypass" || r.(map[string]any)["policy"] == "one_factor" {
			t.Fatalf("weak rule %v", r)
		}
	}
	if !strings.Contains(string(cfg), "enable_passkey_login: true") {
		t.Fatal("passkey login not enabled")
	}
	for _, secret := range []string{"jwt_secret", "encryption_key", "secret:"} {
		if strings.Contains(string(cfg), secret) {
			t.Fatalf("secret %q rendered into config; it must come from *_FILE env", secret)
		}
	}
	if !strings.Contains(string(cfg), "authelia_url: https://auth.example.com") || !strings.Contains(string(cfg), "authelia_url: https://auth.friend.org") {
		t.Fatal("session cookies missing")
	}
	if !strings.Contains(string(users), "admin:") || !strings.Contains(string(users), "$argon2id$") {
		t.Fatal("users file incomplete")
	}
}

func TestRenderPlaceholderBeforeSetup(t *testing.T) {
	in := input()
	in.Domains, in.Protected, in.Users = nil, nil, nil
	cfg, _, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), PlaceholderDomain) {
		t.Fatal("placeholder cookie domain missing")
	}
	_, users, _ := Render(in)
	var doc struct {
		Users map[string]struct {
			Disabled bool   `yaml:"disabled"`
			Password string `yaml:"password"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(users, &doc); err != nil {
		t.Fatal(err)
	}
	p, ok := doc.Users[PlaceholderUser]
	if len(doc.Users) != 1 || !ok || !p.Disabled {
		t.Fatalf("expected exactly one disabled placeholder user, got %+v", doc.Users)
	}
}

func TestRenderRejectsInjection(t *testing.T) {
	for _, mut := range []func(*Input){
		func(in *Input) { in.Domains = []string{"example.com\npolicy: bypass"} },
		func(in *Input) { in.Protected = []Resource{{Host: "*.example.com"}} },
		func(in *Input) { in.Protected = []Resource{{Host: "a.example.com", PathPrefix: "/x$|.*"}} },
		func(in *Input) { in.Users[0].PasswordHash = "" },
		func(in *Input) { in.Settings.AuthSubdomain = "a b" },
	} {
		in := input()
		mut(&in)
		if _, _, err := Render(in); err == nil {
			t.Error("invalid input accepted")
		}
	}
}

// TestAdminOnlyRules checks rule order: specific paths before whole hosts,
// and an admin-only resource denies everyone outside the admins group before
// a broader two-factor rule of the same host can let them in.
func TestAdminOnlyRules(t *testing.T) {
	in := input()
	in.Protected = []Resource{
		{Host: "apps.example.com"},
		{Host: "apps.example.com", PathPrefix: "/admin", AdminOnly: true},
		{Host: "apps.example.com", PathPrefix: "/admin/public"},
		{Host: "ops.example.com", AdminOnly: true},
		{Host: "ops.example.com"}, // the stricter duplicate wins
	}
	cfg, _, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		AccessControl struct {
			Rules []struct {
				Domain    []string `yaml:"domain"`
				Resources []string `yaml:"resources"`
				Subject   []string `yaml:"subject"`
				Policy    string   `yaml:"policy"`
			} `yaml:"rules"`
		} `yaml:"access_control"`
	}
	if err := yaml.Unmarshal(cfg, &doc); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range doc.AccessControl.Rules {
		got = append(got, strings.Join(r.Domain, ",")+" "+strings.Join(r.Resources, ",")+" "+strings.Join(r.Subject, ",")+" "+r.Policy)
	}
	want := []string{
		`apps.example.com ^/admin/public([/?#].*)?$  two_factor`,
		`apps.example.com ^/admin([/?#].*)?$ group:admins two_factor`,
		`apps.example.com ^/admin([/?#].*)?$  deny`,
		`apps.example.com   two_factor`,
		`ops.example.com  group:admins two_factor`,
		`ops.example.com   deny`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rules:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestAutheliaValidateConfig runs the real `authelia validate-config` in
// Docker when MARINERIS_TEST_DOCKER=1 (CI integration job).
func TestAutheliaValidateConfig(t *testing.T) {
	if os.Getenv("MARINERIS_TEST_DOCKER") != "1" {
		t.Skip("MARINERIS_TEST_DOCKER not set")
	}
	image := os.Getenv("MARINERIS_AUTHELIA_IMAGE")
	if image == "" {
		image = "authelia/authelia:4.39"
	}
	for name, in := range map[string]Input{"configured": input(), "placeholder": {Settings: store.DefaultSettings()}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cfg, users, err := Render(in)
			if err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, "configuration.yml"), cfg, 0o644)
			_ = os.WriteFile(filepath.Join(dir, "users_database.yml"), users, 0o644)
			sec := filepath.Join(dir, "secrets")
			_ = os.Mkdir(sec, 0o755)
			for _, f := range []string{"jwt", "session", "storage"} {
				_ = os.WriteFile(filepath.Join(sec, f), []byte(strings.Repeat("x", 64)), 0o644)
			}
			out, err := exec.Command("docker", append([]string{"run", "--rm",
				"-v", dir + ":/config:ro", "-v", sec + ":/secrets:ro"},
				append(EnvArgs("/secrets"), image, "authelia", "validate-config", "--config", "/config/configuration.yml")...)...).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "without errors") {
				t.Fatalf("authelia rejected config: %v\n%s\n%s", err, out, cfg)
			}
		})
	}
}
