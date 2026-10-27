# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

**Releasing is automatic:** move the entries of *Unreleased* to a new
`## [X.Y.Z] - date` section and merge to `main`. The release workflow sees the
new version, runs the full test suite, publishes the signed images, creates the
tag `vX.Y.Z` and the GitHub release — whose body is that section, followed by
the list of commits and the image digests — then deletes merged branches.

## [Unreleased]

## [0.1.0] - 2026-10-27

First public version: Phase 1.

### Added

- **First-run setup** on a loopback-only listener (SSH tunnel), protected by a
  one-time setup token; the public listener serves nothing until setup is done.
- **Admin authentication**: argon2id passwords (Authelia-compatible), TOTP with
  replay protection, one-time recovery codes, **passkeys** (passwordless
  WebAuthn login), step-up confirmation for sensitive actions, login
  throttling, hardened `__Host-` session cookies, audit log.
- **Break-glass CLI**: `marineris admin reset-password | reset-mfa |
  remove-passkeys | revoke-sessions | disable-ui-authelia`.
- **DNS profiles** for OVHcloud, Cloudflare, Gandi, Infomaniak and Route 53,
  encrypted at rest, with least-privilege guides and a real TXT-record test.
- **Domains & certificates**: wildcard certificates through DNS-01 per domain
  profile, HTTP-01 fallback, Let's Encrypt staging by default, live
  certificate status.
- **Routes** to containers (automatic port selection, apps network
  attachment), external hosts and static folders/SPAs, with per-route
  Authelia 2FA and rate limits.
- **Containers, volumes, networks** management; system containers protected.
- **Compose stacks** through the Docker Compose SDK with a security policy and
  encrypted `.env`.
- **Safe configuration engine** for Caddy and Authelia: validation with the real
  binaries, encrypted history, health checks and automatic rollback.
- **Authelia** generated configuration with passkey login and 2FA rules.
- **CrowdSec** bouncer in Caddy, per-IP rate limiting.
- **Hardened compose stack**: allow-listing Docker socket proxy, internal
  networks, non-root read-only containers without capabilities.
- **Hub** home page (or the dashboard, see Settings → Home page): app tiles
  with customisable title, subtitle and icon (upload, https URL or a
  dashboard-icons name, downloaded once with SSRF protection and served by
  Marineris), and widgets for server time, CPU, memory, disk, network and
  uptime of the host.
- **Web UI** (Svelte) with a neutral theme, Mars-orange accents and swappable
  branding. Branding assets carry additional terms under AGPL-3.0
  section 7(c) and 7(e): distributed forks use their own name and logo
  (`TRADEMARKS.md`).
- **Local demo** (`deploy/demo/demo.sh`): the whole stack on one computer
  with Docker only, under `*.marineris.localhost`, built from the checkout.
- **CI**: lint (golangci-lint with gosec), unit tests with race detector and
  coverage gate, fuzzing, configuration validation against real Caddy and
  Authelia, Playwright end-to-end tests (incl. passkeys), integration tests on
  the real stack with a test ACME CA, gitleaks, govulncheck, Trivy, CodeQL,
  OWASP ZAP; multi-arch signed images with SBOM and provenance.

[Unreleased]: https://github.com/PhotonRing/Marineris-Server/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/PhotonRing/Marineris-Server/releases/tag/v0.1.0
