package stacks

import (
	"context"
	"strings"
	"testing"
)

const mediaCompose = `# Media server
services:
  jellyfin:
    image: jellyfin/jellyfin:10.11 # pinned
    environment:
      TZ: ${TZ}
      PUID: "1000"
    volumes:
      - config:/config
      - /srv/media:/media:ro
    ports: ["8096:8096"]
    networks: [default, apps]
    labels:
      io.marineris.main: "true"
  helper:
    image: alpine:3
    environment:
      - DEBUG=1
      - TOKEN
    volumes:
      - type: bind
        source: ./data
        target: /data
    networks:
      default:
        aliases: [h]
volumes:
  config: {}
networks:
  apps:
    external: true
    name: marineris-apps
`

func TestServices(t *testing.T) {
	svcs, err := Services([]byte(mediaCompose))
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 || svcs[0].Name != "jellyfin" || svcs[1].Name != "helper" {
		t.Fatalf("services in file order: %+v", svcs)
	}
	j, h := svcs[0], svcs[1]
	if j.Image != "jellyfin/jellyfin:10.11" || !j.Main() || len(j.Environment) != 2 || j.Environment[0] != (EnvVar{Key: "TZ", Value: "${TZ}"}) {
		t.Fatalf("jellyfin: %+v", j)
	}
	if strings.Join(j.Volumes, ",") != "config:/config,/srv/media:/media:ro" || strings.Join(j.Ports, ",") != "8096:8096" || strings.Join(j.Networks, ",") != "default,apps" || len(j.LongSyntax) != 0 {
		t.Fatalf("jellyfin lists: %+v", j)
	}
	if h.Main() || h.Environment[1] != (EnvVar{Key: "TOKEN", Unset: true}) {
		t.Fatalf("helper env: %+v", h.Environment)
	}
	if strings.Join(h.LongSyntax, ",") != "volumes,networks" {
		t.Fatalf("helper long syntax: %v", h.LongSyntax)
	}
}

func TestEditService(t *testing.T) {
	img := "jellyfin/jellyfin:10.12"
	env := []EnvVar{{Key: "TZ", Value: "Europe/Paris"}, {Key: "SECRET", Value: `a "quoted" value: yes`}, {Key: "FROM_HOST", Unset: true}}
	ports := []string{"8096:8096", "8920:8920"}
	vols := []string{}
	out, err := EditService([]byte(mediaCompose), "jellyfin", ServiceEdit{Image: &img, Environment: &env, Ports: &ports, Volumes: &vols})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# Media server", "# pinned", "image: jellyfin/jellyfin:10.12", `"8920:8920"`, "aliases: [h]", "io.marineris.main"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	svcs, err := Services(out)
	if err != nil {
		t.Fatal(err)
	}
	j := svcs[0]
	if len(j.Volumes) != 0 || len(j.Ports) != 2 || j.Environment[1].Value != `a "quoted" value: yes` || !j.Environment[2].Unset {
		t.Fatalf("after edit: %+v", j)
	}
	// The edited document still loads and passes the policy.
	m := manager(t)
	if _, vs, err := m.Load(context.Background(), "media", out, []byte("TZ=UTC\n")); err != nil || len(vs) != 0 {
		t.Fatalf("edited document: %v %+v", err, vs)
	}

	for name, e := range map[string]ServiceEdit{
		"bad key":   {Environment: &[]EnvVar{{Key: "A B", Value: "x"}}},
		"duplicate": {Environment: &[]EnvVar{{Key: "A"}, {Key: "A"}}},
	} {
		if _, err := EditService([]byte(mediaCompose), "jellyfin", e); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := EditService([]byte(mediaCompose), "nope", ServiceEdit{Image: &img}); err == nil {
		t.Error("unknown service accepted")
	}
	// YAML injection through a value stays a value.
	inj := []string{"x\nprivileged: true"}
	out, err = EditService([]byte(mediaCompose), "jellyfin", ServiceEdit{Volumes: &inj})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "\n    privileged: true") {
		t.Fatalf("injection:\n%s", out)
	}
}
