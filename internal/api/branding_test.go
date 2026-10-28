package api

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// A 1×1 PNG.
const pngPixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestBranding(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.completeSetup()
	brandOf := func() map[string]any {
		t.Helper()
		r := e.localClient().do("GET", "/api/meta", nil)
		return r.Body["brand"].(map[string]any)
	}
	if b := brandOf(); b["name"] != "Marineris" || b["custom_name"] != false || b["logo"] != "" {
		t.Fatalf("defaults: %v", b)
	}

	for _, bad := range []map[string]string{
		{"name": strings.Repeat("x", 41)},
		{"name": "<script>"},
		{"accent": "red"},
		{"accent": "#12345"},
	} {
		if r := admin.do("PUT", "/api/branding", bad); r.Status != http.StatusBadRequest {
			t.Errorf("%v accepted: %d", bad, r.Status)
		}
	}
	if r := admin.do("PUT", "/api/branding", map[string]string{"name": "Olympus", "accent": "#3B82F6"}); r.Status != 200 {
		t.Fatalf("put: %d %s", r.Status, r.Raw)
	}
	if b := brandOf(); b["name"] != "Olympus" || b["accent"] != "#3b82f6" {
		t.Fatalf("after put: %v", b)
	}
	// Saving the settings form keeps the branding.
	st := admin.do("GET", "/api/settings", nil).Body
	delete(st, "setup_complete")
	st["acme_email"], st["ui_host"] = "ops@example.com", ""
	if r := admin.do("PUT", "/api/settings", st); r.Status != 200 {
		t.Fatalf("settings: %d %s", r.Status, r.Raw)
	}
	if b := brandOf(); b["name"] != "Olympus" {
		t.Fatalf("settings form reset the branding: %v", b)
	}

	// Images: validated, public, sandboxed; unknown assets refused.
	if r := admin.do("PUT", "/api/branding/logo", map[string]string{"data": base64.StdEncoding.EncodeToString([]byte("<html>"))}); r.Status != http.StatusBadRequest {
		t.Fatalf("non-image accepted: %d", r.Status)
	}
	if r := admin.do("PUT", "/api/branding/script", map[string]string{"data": pngPixel}); r.Status != http.StatusNotFound {
		t.Fatalf("unknown asset: %d", r.Status)
	}
	if r := admin.do("PUT", "/api/branding/logo", map[string]string{"data": pngPixel}); r.Status != 200 {
		t.Fatalf("upload: %d %s", r.Status, r.Raw)
	}
	logo, _ := brandOf()["logo"].(string)
	if !strings.HasPrefix(logo, "/api/branding/logo?v=") {
		t.Fatalf("logo url %q", logo)
	}
	anon := e.edgeClient()
	r := anon.do("GET", logo, nil)
	if r.Status != 200 || r.Header.Get("Content-Type") != "image/png" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("public logo: %d %v", r.Status, r.Header)
	}
	if r := anon.do("DELETE", "/api/branding/logo", nil); r.Status == 200 {
		t.Fatal("anonymous delete accepted")
	}
	if r := admin.do("DELETE", "/api/branding/logo", nil); r.Status != 200 {
		t.Fatalf("delete: %d", r.Status)
	}
	if b := brandOf(); b["logo"] != "" {
		t.Fatalf("logo not reset: %v", b)
	}
}
