// Package webui serves the embedded web UI (built from web/ by Vite) and
// the branding assets.
//
// Branding (logo, favicon, theme.json) is looked up first in the configured
// branding directory, then in the defaults compiled into the binary, so an
// operator can swap the look without rebuilding.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/photonring/marineris-server/branding"
)

//go:embed all:dist
var dist embed.FS

// Handler returns the UI handler. brandingDir may be empty.
func Handler(brandingDir string) http.Handler {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	brand := branding.Defaults
	files := http.FileServer(http.FS(ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := path.Clean("/" + r.URL.Path)
		if name, ok := strings.CutPrefix(p, "/branding/"); ok {
			serveBranding(w, r, brandingDir, brand, name)
			return
		}
		if p != "/" {
			if f, err := ui.Open(strings.TrimPrefix(p, "/")); err == nil {
				st, _ := f.Stat()
				f.Close()
				if st != nil && !st.IsDir() {
					if strings.HasPrefix(p, "/assets/") {
						// Vite fingerprints assets: cache them for long.
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		// SPA fallback: every other path renders index.html.
		w.Header().Set("Cache-Control", "no-cache")
		index, err := fs.ReadFile(ui, "index.html")
		if err != nil {
			http.Error(w, "UI not built", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

var brandingTypes = map[string]string{
	".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon",
	".webp": "image/webp", ".jpg": "image/jpeg", ".json": "application/json",
}

func serveBranding(w http.ResponseWriter, r *http.Request, dir string, def fs.FS, name string) {
	if name == "" || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	ctype, ok := brandingTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "public, max-age=300")
	if ctype == "image/svg+xml" {
		// SVGs can carry scripts; never let them run.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil { //nolint:gosec // name has no "/" and no leading "." (checked above)
			_, _ = w.Write(b) //nolint:gosec // static branding file served with a fixed content type
			return
		}
	}
	b, err := fs.ReadFile(def, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b) //nolint:gosec // static branding file served with a fixed content type
}
