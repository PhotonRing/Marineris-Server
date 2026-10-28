package configmgr

import (
	"bytes"
	"context"
	"image"
	_ "image/gif" // decoders for uploaded brand images
	_ "image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/photonring/marineris-server/branding"
	"github.com/photonring/marineris-server/internal/store"
)

// autheliaAssetsDir is where Authelia's portal reads its logo and favicon
// (server.asset_path), inside the folder mounted at /config.
const autheliaAssetsDir = "assets"

// writeAutheliaAssets gives the Authelia portal the brand's logo and favicon,
// so that signing in looks like the rest of Marineris. Authelia only reads
// logo.png and favicon.ico: an image it cannot read (SVG, WebP) falls back
// to the default Marineris one.
func (m *Manager) writeAutheliaAssets(ctx context.Context) error {
	dir := filepath.Join(m.cfg.AutheliaDir(), autheliaAssetsDir)
	files := map[string][]byte{
		"logo.png":    m.brandImage(ctx, "logo", "icon-192.png", false),
		"favicon.ico": m.brandImage(ctx, "favicon", "favicon-32.png", true),
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) { //nolint:gosec // fixed path below the data directory
			continue
		}
		if err := writePublic(path, data); err != nil {
			return err
		}
	}
	return nil
}

// brandImage returns an uploaded brand image as PNG (or ICO, when allowed),
// else the default branding file def.
func (m *Manager) brandImage(ctx context.Context, asset, def string, ico bool) []byte {
	if im, err := m.store.GetImage(ctx, store.ImageBrand, asset); err == nil {
		switch im.ContentType {
		case "image/png":
			return im.Data
		case "image/x-icon", "image/vnd.microsoft.icon":
			if ico {
				return im.Data
			}
		case "image/jpeg", "image/gif":
			if img, _, err := image.Decode(bytes.NewReader(im.Data)); err == nil {
				var b bytes.Buffer
				if png.Encode(&b, img) == nil {
					return b.Bytes()
				}
			}
		}
	}
	if m.cfg.BrandingDir != "" {
		if b, err := os.ReadFile(filepath.Join(m.cfg.BrandingDir, def)); err == nil { //nolint:gosec // fixed file name in the configured branding folder
			return b
		}
	}
	b, _ := fs.ReadFile(branding.Defaults, def)
	return b
}
