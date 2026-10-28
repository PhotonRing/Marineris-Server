package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/photonring/marineris-server/internal/icons"
	"github.com/photonring/marineris-server/internal/store"
)

// Branding the administrator can change from the UI: a name, an accent
// colour and three images. They are public (the login page uses them).
var brandAssets = map[string]bool{"favicon": true, "logo": true, "background": true}

var accentRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// DefaultBrandName is shown when no name is set.
const DefaultBrandName = "Marineris"

type brand struct {
	Name   string `json:"name"`
	Custom bool   `json:"custom_name"`
	Accent string `json:"accent"`
	// Same-origin URLs of the uploaded images, empty for the defaults.
	Favicon    string `json:"favicon"`
	Logo       string `json:"logo"`
	Background string `json:"background"`
}

func (s *Server) brand(ctx context.Context, st store.Settings) (brand, error) {
	b := brand{Name: st.BrandName, Custom: st.BrandName != "", Accent: st.AccentColor}
	if b.Name == "" {
		b.Name = DefaultBrandName
	}
	versions, err := s.Store.ImageVersions(ctx, store.ImageBrand)
	if err != nil {
		return b, err
	}
	url := func(asset string) string {
		t, ok := versions[asset]
		if !ok {
			return ""
		}
		return "/api/branding/" + asset + "?v=" + strconv.FormatInt(t.UnixNano(), 36)
	}
	b.Favicon, b.Logo, b.Background = url("favicon"), url("logo"), url("background")
	return b, nil
}

func (s *Server) handleBrandingGet(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	b, err := s.brand(r.Context(), rc.settings)
	if err != nil {
		return err
	}
	return ok(w, b)
}

func (s *Server) handleBrandingPut(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	var req struct {
		Name   string `json:"name"`
		Accent string `json:"accent"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(name) > 40 || strings.ContainsAny(name, "<>\r\n\t") {
		return fieldError("name", "is limited to 40 characters, without < > or line breaks")
	}
	if name == DefaultBrandName {
		name = ""
	}
	accent := strings.ToLower(strings.TrimSpace(req.Accent))
	if accent != "" && !accentRE.MatchString(accent) {
		return fieldError("accent", "must be a colour like #e8743b")
	}
	ctx := r.Context()
	st, err := s.Store.UpdateSettings(ctx, func(st *store.Settings) error {
		st.BrandName, st.AccentColor = name, accent
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(r, rc, "branding.update", "", true, "name="+name+" accent="+accent)
	b, err := s.brand(ctx, st)
	if err != nil {
		return err
	}
	return ok(w, b)
}

func brandAsset(r *http.Request) (string, error) {
	a := r.PathValue("asset")
	if !brandAssets[a] {
		return "", &Error{Status: http.StatusNotFound, Code: "not_found", Message: "unknown branding asset"}
	}
	return a, nil
}

func (s *Server) handleBrandingAsset(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	a, err := brandAsset(r)
	if err != nil {
		return err
	}
	im, err := s.Store.GetImage(r.Context(), store.ImageBrand, a)
	if err != nil {
		return err
	}
	serveImage(w, im)
	return nil
}

// serveImage writes a stored image. Its type was sniffed on upload; SVGs
// can carry scripts, so they never run, even when opened directly.
func serveImage(w http.ResponseWriter, im *store.Image) {
	h := w.Header()
	h.Set("Content-Type", im.ContentType)
	// URLs carry a version: cache for a day, revalidated after a change.
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	_, _ = w.Write(im.Data) //nolint:gosec // sniffed image with a fixed type and a sandboxing CSP
}

// decodeImage validates an uploaded image (base64).
func decodeImage(data string) (string, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", nil, fieldError("data", "must be base64")
	}
	ct, err := icons.Detect(raw)
	if err != nil {
		return "", nil, fieldError("data", err.Error())
	}
	return ct, raw, nil
}

func (s *Server) handleBrandingAssetPut(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	a, err := brandAsset(r)
	if err != nil {
		return err
	}
	var req struct {
		Data string `json:"data"` // base64
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ct, data, err := decodeImage(req.Data)
	if err != nil {
		return err
	}
	if err := s.Store.SetImage(r.Context(), store.ImageBrand, a, ct, data); err != nil {
		return err
	}
	s.audit(r, rc, "branding."+a, "", true, ct)
	return s.handleBrandingGet(w, r, rc)
}

func (s *Server) handleBrandingAssetDelete(w http.ResponseWriter, r *http.Request, rc *reqCtx) error {
	a, err := brandAsset(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteImage(r.Context(), store.ImageBrand, a); err != nil {
		return err
	}
	s.audit(r, rc, "branding."+a, "", true, "reset to default")
	return s.handleBrandingGet(w, r, rc)
}
