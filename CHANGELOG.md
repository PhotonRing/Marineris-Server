# Changelog

All notable changes are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

**Versions before 1.0 follow the roadmap phases:** the minor number is the
phase (0.1.x = Phase 1, 0.2.0 starts Phase 2) and patch releases bring
features and fixes within the phase. 1.0.0 will mark a stable, complete
platform; from then on the usual rules apply (minor = features, patch =
fixes, major = breaking changes).

**Releasing is automatic:** move the entries of *Unreleased* to a new
`## [X.Y.Z] - date` section and merge to `main`. The release workflow sees the
new version, runs the full test suite, publishes the signed images, creates the
tag `vX.Y.Z` and the GitHub release — whose body is that section, followed by
the list of commits and the image digests — then deletes merged branches.

## [Unreleased]

## [0.1.1] - 2026-10-28

Phase 1, continued: a stack editor, branding, administrators-only routes and a
clearer view of the proxy and single sign-on.

**Upgrading from 0.1.0:** in `.env`, set `MARINERIS_IMAGE` and
`MARINERIS_CADDY_IMAGE` to the `0.1.1` tags, then
`docker compose pull && docker compose up -d` (see the
[upgrade runbook](https://github.com/PhotonRing/Marineris-Server/blob/main/docs/runbooks/upgrade.md)). The database is
migrated automatically. Note that the public Marineris UI host is now limited
to Authelia's `admins` group, which every existing account belongs to.

### Added

- **Online demo** on GitHub Pages, published with every release: the real web
  UI with sample data and an in-browser backend.
- **Stack page** (Stacks → a stack): every service with its icon, status and
  containers; stack actions (deploy, update images, restart, stop, delete) and
  service actions (start/stop, restart, recreate, update image); tabs for a
  service's logs (with follow), environment, mounts, networks and ports — saved
  into `compose.yaml` with comments and other services kept, checked against
  the policy, optionally recreating the service — and its icon; the
  `compose.yaml`/`.env` editor. The stacks list shows each stack's status and
  the icon of its main service.
- **Service icons**: downloaded once after a deployment from the image name or
  an `io.marineris.icon` label, or set from the UI (name, URL or upload);
  `io.marineris.main: "true"` picks the stack's main service.
- **Administrators-only routes**: a third access policy next to public and
  2FA, enforced by Authelia (group `admins`).
- **Branding from the UI** (Settings → Branding): name, accent colour,
  favicon, header icon and background image.

- **Proxy & SSO** overview: what Caddy serves (address, target, access, rate
  limit, certificate) and Authelia's access rules, portals, sessions and
  accounts, each with a link to the page that changes it.
- Stack page: a **Labels** tab, suggesting the labels Marineris reads
  (`io.marineris.main`, `io.marineris.icon`) with their meaning; a **Mounts**
  tab with typed mounts: a volume (of the stack, another existing Docker
  volume or a new one, declared in the file automatically), an anonymous
  volume or a host folder, and read-only.

### Changed

- The *Configuration* page is now **Proxy & SSO** (Overview, History,
  Generated file): it was easy to confuse with *Settings*.
- Icons: one open-source set ([Lucide](https://lucide.dev), ISC licence),
  single-colour, in the menu, the top bar, the hub and the stack page; the
  account is shown with a user icon and Settings with a cog.
- The public Marineris UI host is restricted to administrators in Authelia
  (it was open to any Authelia account; there are no others yet).
- The hub background covers the whole window (it stopped at the content
  column on wide screens, leaving dark bands).

### Security

- The stack policy refuses the `io.marineris.system` label, which would have
  let a user stack pass for a protected Marineris container.

### Fixed

- The demo banner no longer makes every page scroll.

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

[Unreleased]: https://github.com/PhotonRing/Marineris-Server/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/PhotonRing/Marineris-Server/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/PhotonRing/Marineris-Server/releases/tag/v0.1.0
