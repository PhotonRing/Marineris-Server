package configmgr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/photonring/marineris-server/branding"
	"github.com/photonring/marineris-server/internal/store"
)

func portalAsset(t *testing.T, m *Manager, name string) []byte {
	t.Helper()
	if err := m.writeAutheliaAssets(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(m.cfg.AutheliaDir(), autheliaAssetsDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAutheliaAssetsFollowTheBranding(t *testing.T) {
	m, st, _, _ := setup(t)
	ctx := context.Background()
	def, _ := fs.ReadFile(branding.Defaults, "icon-192.png")

	// Default branding.
	if !bytes.Equal(portalAsset(t, m, "logo.png"), def) {
		t.Fatal("the portal does not show the default logo")
	}

	// An uploaded PNG is used as is.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var p bytes.Buffer
	_ = png.Encode(&p, img)
	if err := st.SetImage(ctx, store.ImageBrand, "logo", "image/png", p.Bytes()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(portalAsset(t, m, "logo.png"), p.Bytes()) {
		t.Fatal("uploaded PNG logo not used")
	}

	// A JPEG becomes a PNG (Authelia only reads logo.png).
	var j bytes.Buffer
	_ = jpeg.Encode(&j, img, nil)
	if err := st.SetImage(ctx, store.ImageBrand, "logo", "image/jpeg", j.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(portalAsset(t, m, "logo.png"))); err != nil {
		t.Fatalf("JPEG logo not converted to PNG: %v", err)
	}

	// An SVG cannot be converted: the default logo.
	if err := st.SetImage(ctx, store.ImageBrand, "logo", "image/svg+xml", []byte("<svg/>")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(portalAsset(t, m, "logo.png"), def) {
		t.Fatal("an SVG logo must fall back to the default")
	}

	// A favicon may stay an ICO.
	ico := []byte{0, 0, 1, 0, 1, 0}
	if err := st.SetImage(ctx, store.ImageBrand, "favicon", "image/x-icon", ico); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(portalAsset(t, m, "favicon.ico"), ico) {
		t.Fatal("ICO favicon not used")
	}
}
