// Package branding holds the default (placeholder) branding assets compiled
// into the binary. Operators replace them at runtime by mounting a directory
// with files of the same names and setting MARINERIS_BRANDING_DIR.
package branding

import "embed"

// Defaults are the built-in branding files.
//
//go:embed *.svg *.png *.webp theme.json
var Defaults embed.FS
