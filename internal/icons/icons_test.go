package icons

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

var png1x1, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func TestDetect(t *testing.T) {
	if ct, err := Detect(png1x1); err != nil || ct != "image/png" {
		t.Fatalf("png: %s %v", ct, err)
	}
	for _, svg := range []string{`<svg xmlns="http://www.w3.org/2000/svg"/>`, "<?xml version=\"1.0\"?>\n<svg/>", "\xef\xbb\xbf <svg/>"} {
		if ct, err := Detect([]byte(svg)); err != nil || ct != "image/svg+xml" {
			t.Errorf("%q: %s %v", svg, ct, err)
		}
	}
	for _, bad := range [][]byte{nil, []byte("<html><script>alert(1)</script>"), []byte("hello"), []byte("<?xml version='1.0'?><html/>"), bytes.Repeat([]byte{0x89}, MaxSize+1)} {
		if _, err := Detect(bad); err == nil {
			t.Errorf("accepted %q", bad[:min(len(bad), 20)])
		}
	}
}

func TestSlug(t *testing.T) {
	u, err := SlugURL("jellyfin")
	if err != nil || !strings.HasSuffix(u, "/png/jellyfin.png") {
		t.Fatal(u, err)
	}
	for _, bad := range []string{"", "Jelly", "../x", "a/b", "a.png", strings.Repeat("a", 70)} {
		if _, err := SlugURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for img, want := range map[string]string{
		"jellyfin/jellyfin:10.10":                          "jellyfin",
		"lscr.io/linuxserver/nextcloud:latest":             "nextcloud",
		"ghcr.io/home-assistant/home-assistant@sha256:abc": "home-assistant",
		"Weird_Name": "",
	} {
		if got := SuggestSlug(img); got != want {
			t.Errorf("%s: got %q want %q", img, got, want)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.20.0.5", "169.254.169.254", "100.64.1.1", "::1", "fd00::1", "fe80::1", "0.0.0.0", "::ffff:127.0.0.1", "224.0.0.1"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s considered public", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "151.101.1.1", "2606:4700::1111"} {
		if !publicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s considered private", s)
		}
	}
}

func TestFetchRefusesInternalTargets(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(png1x1) }))
	defer srv.Close()
	f := NewFetcher()
	// The test server listens on 127.0.0.1: the dialer must refuse it.
	if _, _, err := f.Fetch(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback fetch not refused: %v", err)
	}
	for _, u := range []string{"http://example.com/x.png", "file:///etc/passwd", "https://user:pw@example.com/x.png", "gopher://x"} {
		if _, _, err := f.Fetch(context.Background(), u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
}

func FuzzDetect(f *testing.F) {
	f.Add(png1x1)
	f.Add([]byte("<svg/>"))
	f.Fuzz(func(t *testing.T, b []byte) {
		ct, err := Detect(b)
		if err == nil && !strings.HasPrefix(ct, "image/") {
			t.Fatalf("non-image type %q", ct)
		}
	})
}
