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
		Settings:       st,
		Domains:        []string{"friend.org", "example.com"},
		ProtectedHosts: []string{"media.example.com", "marineris.example.com", "media.example.com"},
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
	rule := rules[0].(map[string]any)
	if rule["policy"] != "two_factor" || len(rule["domain"].([]any)) != 2 {
		t.Fatalf("unexpected rule %v", rule)
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
	in.Domains, in.ProtectedHosts, in.Users = nil, nil, nil
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
		func(in *Input) { in.ProtectedHosts = []string{"*.example.com"} },
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
