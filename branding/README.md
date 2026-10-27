# Branding

Marineris branding assets, compiled into the binary as defaults.

| File              | Used for                                                                 |
|-------------------|--------------------------------------------------------------------------|
| `logo-full.svg`   | Planet + wordmark on the login and setup screens                         |
| `wordmark.svg`    | Sidebar wordmark. Used as a CSS mask, so it takes the theme's text colour — keep it a single-colour shape |
| `icon-192.png`    | Sidebar icon and `apple-touch-icon`                                      |
| `favicon-32.png`  | Browser tab icon                                                         |
| `background.webp` | Background of the login/setup screens only (behind a dark overlay); app pages stay plain for readability |
| `theme.json`      | Colours of the dark and light themes                                     |

## Replacing them without rebuilding

Put files with the **same names** in a directory inside the data folder, for
example `/opt/marineris/data/branding`, and set in `deploy/.env`:

```
MARINERIS_BRANDING_DIR=/opt/marineris/data/branding
```

Missing files fall back to the built-in ones. Allowed extensions: `.svg`,
`.png`, `.ico`, `.webp`, `.jpg`, `.json`. SVG files are served with a
sandboxing Content-Security-Policy, so scripts inside them never run.

## Sources

Built from the original artwork (`marineris.svg`, `marineris_no_background.svg`,
`marineris_favicon.png`, `background.png`):

- `favicon-32.png` / `icon-192.png`: resized from the 667×667 favicon
- `background.webp`: the 3 MB PNG re-encoded as WebP (quality 70, ~200 KB)
- `wordmark.svg`: viewBox cropped to the lettering

## Licence

These files are licensed under the AGPL-3.0 with additional terms under its
section 7(c) and 7(e), written in [LICENSE](LICENSE): forks that are
distributed or offered over a network replace them and use their own name —
see the [name and logo policy](../TRADEMARKS.md).
