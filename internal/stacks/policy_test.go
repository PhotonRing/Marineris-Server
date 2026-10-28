package stacks

import (
	"context"
	"strings"
	"testing"
)

func manager(t *testing.T) *Manager {
	t.Helper()
	return NewManager("/opt/marineris/data/stacks", "tcp://socket-proxy:2375", []string{"/srv", "/mnt"}, []string{"marineris", "marineris-caddy"})
}

func TestSafeStackPasses(t *testing.T) {
	m := manager(t)
	_, vs, err := m.Load(context.Background(), "media", []byte(`
services:
  jellyfin:
    image: jellyfin/jellyfin:10.10
    environment:
      TZ: ${TZ}
    volumes:
      - config:/config
      - ./cache:/cache
      - /srv/media:/media:ro
    cap_add: [CHOWN]
volumes:
  config: {}
`), []byte("TZ=Europe/Paris\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("unexpected violations: %+v", vs)
	}
}

func TestInterpolationDoesNotSeeProcessEnv(t *testing.T) {
	t.Setenv("MARINERIS_SECRET_PROBE", "leaked-value")
	m := manager(t)
	p, vs, err := m.Load(context.Background(), "probe", []byte(`
services:
  app:
    image: alpine:3
    environment:
      X: ${MARINERIS_SECRET_PROBE:-unset}
`), nil)
	if err != nil || len(vs) != 0 {
		t.Fatalf("%v %+v", err, vs)
	}
	if v := p.Services["app"].Environment["X"]; v == nil || *v != "unset" {
		t.Fatalf("process environment leaked into stack: %v", v)
	}
}

func TestDangerousStacksAreFlagged(t *testing.T) {
	cases := map[string]struct {
		yaml        string
		rule        string
		overridable bool
	}{
		"privileged":    {"services: {a: {image: x, privileged: true}}", "privileged", true},
		"host network":  {"services: {a: {image: x, network_mode: host}}", "network_mode", true},
		"host pid":      {"services: {a: {image: x, pid: host}}", "pid", true},
		"sys_admin":     {"services: {a: {image: x, cap_add: [SYS_ADMIN]}}", "cap_add", true},
		"cap_all":       {"services: {a: {image: x, cap_add: [ALL]}}", "cap_add", true},
		"devices":       {"services: {a: {image: x, devices: ['/dev/sda:/dev/sda']}}", "devices", true},
		"unconfined":    {"services: {a: {image: x, security_opt: ['seccomp:unconfined']}}", "security_opt", true},
		"docker socket": {"services: {a: {image: x, volumes: ['/var/run/docker.sock:/var/run/docker.sock']}}", "bind_mount", false},
		"root bind":     {"services: {a: {image: x, volumes: ['/:/host']}}", "bind_mount", false},
		"etc bind":      {"services: {a: {image: x, volumes: ['/etc:/etc']}}", "bind_mount", false},
		"outside roots": {"services: {a: {image: x, volumes: ['/opt/other:/data']}}", "bind_mount", false},
		"traversal":     {"services: {a: {image: x, volumes: ['./../../secrets:/s']}}", "bind_mount", false},
		"marineris dir": {"services: {a: {image: x, volumes: ['/opt/marineris/data:/d']}}", "bind_mount", false},
		"build":         {"services: {a: {build: .}}", "build", false},
		"reserved name": {"services: {a: {image: x, container_name: marineris}}", "container_name", false},
		"volumes_from":  {"services: {a: {image: x}, b: {image: x, volumes_from: [a]}}", "volumes_from", true},
		"system label":  {"services: {a: {image: x, labels: {io.marineris.system: 'true'}}}", "label", false},
		"system volume": {"services: {a: {image: x}}\nvolumes: {v: {labels: [io.marineris.system=true]}}", "label", false},
	}
	m := manager(t)
	for name, c := range cases {
		_, vs, err := m.Load(context.Background(), "s", []byte(c.yaml), nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		found := false
		for _, v := range vs {
			if v.Rule == c.rule {
				found = true
				if v.Overridable != c.overridable {
					t.Errorf("%s: overridable=%v, want %v", name, v.Overridable, c.overridable)
				}
			}
		}
		if !found {
			t.Errorf("%s: expected rule %s, got %+v", name, c.rule, vs)
		}
	}
}

func TestPreScanBlocksFileReadsOutsideStack(t *testing.T) {
	m := manager(t)
	for name, y := range map[string]string{
		"include":         "include: [/etc/compose.yaml]\nservices: {a: {image: x}}",
		"extends file":    "services: {a: {image: x, extends: {file: /etc/x.yaml, service: y}}}",
		"env_file abs":    "services: {a: {image: x, env_file: /opt/marineris/data/secrets/master.key}}",
		"env_file dotdot": "services: {a: {image: x, env_file: [../../secrets/master.key]}}",
		"env_file obj":    "services: {a: {image: x, env_file: [{path: /etc/shadow}]}}",
		"env_file var":    "services: {a: {image: x, env_file: ${HOME}/.env}}",
		"secret file":     "services: {a: {image: x}}\nsecrets: {s: {file: /etc/shadow}}",
		"config file":     "services: {a: {image: x}}\nconfigs: {c: {file: ../x}}",
		"unknown key":     "services: {a: {image: x}}\nmodels: {}",
		"no services":     "volumes: {}",
		"not yaml":        "{{{",
	} {
		_, vs, err := m.Load(context.Background(), "s", []byte(y), nil)
		if err == nil && len(vs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBlocking(t *testing.T) {
	vs := []Violation{{Rule: "privileged", Overridable: true}, {Rule: "bind_mount"}}
	if len(Blocking(vs, false)) != 2 || len(Blocking(vs, true)) != 1 {
		t.Fatal("unsafe ack must only lift overridable findings")
	}
}

func TestStackNameValidated(t *testing.T) {
	m := manager(t)
	for _, n := range []string{"", "../x", "a/b", "UPPER", strings.Repeat("a", 64)} {
		if _, _, err := m.Load(context.Background(), n, []byte("services: {a: {image: x}}"), nil); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}

func TestParseEnv(t *testing.T) {
	env, err := parseEnv([]byte("# comment\nA=1\nexport B=\"two words\"\nC='x'\n\n"))
	if err != nil || env["A"] != "1" || env["B"] != "two words" || env["C"] != "x" {
		t.Fatalf("%v %v", env, err)
	}
	for _, bad := range []string{"NOEQUALS", "=x", "A B=1", "$X=1"} {
		if _, err := parseEnv([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzPreScan(f *testing.F) {
	f.Add("services: {a: {image: x}}")
	f.Add("include: [a]")
	f.Fuzz(func(t *testing.T, y string) {
		_ = PreScan([]byte(y)) // must not panic
	})
}
