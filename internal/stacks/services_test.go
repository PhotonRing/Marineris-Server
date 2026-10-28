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

func TestMountsAndLabels(t *testing.T) {
	svcs, err := Services([]byte(mediaCompose))
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{{Type: "volume", Source: "config", Target: "/config"}, {Type: "bind", Source: "/srv/media", Target: "/media", ReadOnly: true}}
	if len(svcs[0].Mounts) != 2 || svcs[0].Mounts[0] != want[0] || svcs[0].Mounts[1] != want[1] {
		t.Fatalf("mounts: %+v", svcs[0].Mounts)
	}
	if m := svcs[1].Mounts; len(m) != 1 || m[0] != (Mount{Type: "bind", Source: "./data", Target: "/data"}) {
		t.Fatalf("long-syntax mount: %+v", m)
	}
	if ParseMount("/cache") != (Mount{Type: "volume", Target: "/cache"}) {
		t.Fatal("anonymous volume")
	}

	mounts := []Mount{
		{Type: "volume", Source: "config", Target: "/config"},
		{Type: "volume", Source: "photos", Target: "/photos", ReadOnly: true},
		{Type: "volume", Source: "media_shared", Target: "/shared", External: true},
		{Type: "bind", Source: "./cache", Target: "/cache"},
		{Type: "volume", Target: "/tmp/scratch"},
	}
	labels := []EnvVar{{Key: MainLabel, Value: "true"}, {Key: IconLabel, Value: "jellyfin"}}
	out, err := EditService([]byte(mediaCompose), "jellyfin", ServiceEdit{Mounts: &mounts, Labels: &labels})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, w := range []string{"- config:/config", "- photos:/photos:ro", "- ./cache:/cache", "- /tmp/scratch", "photos: {}", "media_shared: {external: true}", `io.marineris.icon: "jellyfin"`} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
	if strings.Count(s, "config: {}") != 1 {
		t.Errorf("config declared twice:\n%s", s)
	}
	if _, vs, err := manager(t).Load(context.Background(), "media", out, []byte("TZ=UTC\n")); err != nil || len(vs) != 0 {
		t.Fatalf("edited document: %v %+v", err, vs)
	}
	j, _ := Services(out)
	if j[0].Icon() != "jellyfin" || !j[0].Main() {
		t.Fatalf("labels: %+v", j[0].Labels)
	}

	for name, m := range map[string]Mount{
		"relative target": {Type: "volume", Source: "x", Target: "data"},
		"bad volume name": {Type: "volume", Source: "../etc", Target: "/x"},
		"bind not a path": {Type: "bind", Source: "etc", Target: "/x"},
		"colon injection": {Type: "bind", Source: "/srv/a:/etc", Target: "/x"},
		"unknown type":    {Type: "tmpfs", Target: "/x"},
		"ro anonymous":    {Type: "volume", Target: "/x", ReadOnly: true},
	} {
		if _, err := EditService([]byte(mediaCompose), "jellyfin", ServiceEdit{Mounts: &[]Mount{m}}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := EditService([]byte(mediaCompose), "jellyfin", ServiceEdit{Labels: &[]EnvVar{{Key: "a: b"}}}); err == nil {
		t.Error("bad label name accepted")
	}
}

func TestPinVolumeNames(t *testing.T) {
	in := "services:\n  a:\n    image: x\n    volumes: [data:/d, cache:/c, shared:/s, named:/n]\nvolumes:\n  data: {}\n  cache:\n  shared: {external: true}\n  named:\n    name: my-volume\n"
	out, err := PinVolumeNames([]byte(in), "media")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"name: media_data", "name: media_cache", "name: my-volume"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "media_shared") || strings.Contains(s, "media_named") {
		t.Errorf("external or named volume renamed:\n%s", s)
	}
	if _, vs, err := manager(t).Load(context.Background(), "films", out, nil); err != nil || len(vs) != 0 {
		t.Fatalf("pinned document: %v %+v", err, vs)
	}
	if same, _ := PinVolumeNames([]byte("services:\n  a:\n    image: x\n"), "m"); string(same) != "services:\n  a:\n    image: x\n" {
		t.Fatal("a file without volumes must be unchanged")
	}
}
