<p align="center">
  <img src="branding/logo-full.svg" alt="Marineris" width="180">
</p>

<h1 align="center">Marineris</h1>

<p align="center">
  A self-hosted server platform for an internet-facing machine: containers and
  compose stacks, reverse proxy with automatic multi-domain TLS, single sign-on
  with 2FA and passkeys, and a shield — all managed from one UI, secure by default.
</p>

> **Status: Phase 1 (pre-release).** Containers, stacks, routes, multi-domain
> certificates with DNS profiles, first-run setup and admin authentication are
> implemented. Users/OIDC UI, CrowdSec UI, dashboard metrics, backups, web
> terminal, marketplace and disk manager come in later phases (see
> [Roadmap](#roadmap)).

<p align="center">
  <img src="docs/images/hub.webp" alt="The Marineris hub: server widgets and app tiles over the Mars background" width="900">
</p>

## Features (Phase 1)

- **Hub.** A home page listing your apps as tiles (icon, name, subtitle — all
  customisable) with live widgets: server time, CPU, memory, disk, network and
  uptime. The status dashboard can be the home page instead.
- **Your branding.** Name, accent colour, favicon, header icon and background,
  changed from Settings → Branding (the login page uses them too).
- **One source of truth.** Marineris generates and applies the configuration of
  Caddy and Authelia from its own database. Every change is validated (by the
  real `caddy` and `authelia` binaries), snapshotted, applied, health-checked and
  **rolled back automatically** on failure. The **Proxy & SSO** page explains
  in plain terms what they do (sites, access, certificates, Authelia's access
  rules and sessions), with the full, encrypted history (one-click restore) and
  the generated files.
- **Containers & stacks.** List, start, stop, restart, logs and details;
  docker-compose stacks (Docker Compose SDK) with a **security policy**: no
  `privileged`, host namespaces, dangerous capabilities, devices, Docker socket
  or host-system mounts unless you explicitly allow the overridable ones.
  Each stack has its own page: the status and icon of every service; start,
  stop, restart, recreate or update one service or the whole stack; tabs to
  follow a service's logs and edit its image, environment, mounts (volume —
  of the stack, another existing one or a new one — or host folder,
  read-only), networks, ports and labels (with suggestions for the Marineris
  labels), all written back to `compose.yaml` and checked against the policy;
  or edit the whole file. Icons are picked automatically from the image name
  ([dashboard-icons](https://github.com/homarr-labs/dashboard-icons)), or set
  with an `io.marineris.icon` label or from the UI; the stack takes the icon
  of its main service (`io.marineris.main: "true"`, else the one published by
  a route, else the first one).
- **Routes.** Pick a container (its port is selected automatically), an external
  host (LAN appliance, other server) or a static folder / single-page app.
  Per-route Authelia 2FA protection — for everyone with an account, or
  **administrators only** — and rate limits.
- **Multi-domain TLS.** Let's Encrypt with **wildcard certificates through
  DNS-01** by default, or HTTP-01. **DNS credential profiles** for OVHcloud,
  Cloudflare, Gandi, Infomaniak and Route 53 — e.g. *OVH personal*, *OVH
  friend* — each with a **Test** button that creates, reads back and deletes a
  real TXT record. Live certificate status. Staging CA by default.
- **Authentication.** Admin login with password + TOTP or **passkeys**
  (WebAuthn), one-time recovery codes, step-up confirmation for sensitive
  actions. Authelia in front of the public UI and of any route (2FA, passkeys).
- **Shield.** CrowdSec bouncer in Caddy, per-IP rate limiting.
- **Secure by default.** No default passwords; the admin is locked until
  first-run setup, which is only reachable through an SSH tunnel and needs a
  one-time token from the server. Docker only through an allow-listing socket
  proxy. Every container non-root, read-only, all capabilities dropped.
  Break-glass access over SSH.

## Online demo

**[Try the web UI in your browser](https://photonring.github.io/Marineris-Server/)**:
the real interface with sample data, answered by your browser itself (nothing
is sent anywhere, changes are lost when you reload). It is published with
every release.

## Try it on your computer

The whole stack (Marineris, Caddy, Authelia, CrowdSec, socket proxy) runs on
a Linux computer or Windows with WSL 2, with Docker as the only requirement.
It builds the images from your checkout, so it also tests unreleased changes:

```sh
git clone https://github.com/PhotonRing/Marineris-Server.git && cd Marineris-Server
deploy/demo/demo.sh up      # prints the setup token and what to do next
deploy/demo/demo.sh down    # stops it and deletes everything it created
```

Apps are published under `*.marineris.localhost`, which every browser
resolves to your own computer; certificates come from Caddy's local CA (the
browser warns once per site). Nothing listens on your network.

## Quick start

Requirements: a Linux server with Docker Engine 24+ and the compose plugin,
ports 80 and 443 reachable from the internet, DNS records of your domains
pointing to the server.

```sh
git clone https://github.com/PhotonRing/Marineris-Server.git /opt/marineris/src
cd /opt/marineris/src/deploy
sudo ./install.sh          # creates /opt/marineris/data and .env
# edit .env: pin MARINERIS_IMAGE / MARINERIS_CADDY_IMAGE to a release tag
sudo docker compose up -d
sudo docker logs marineris 2>&1 | grep setup_token
```

From **your computer**, open an SSH tunnel and finish the setup in the browser:

```sh
ssh -L 8081:127.0.0.1:8081 you@your-server
# then open http://localhost:8081   (use "localhost", not 127.0.0.1, for passkeys)
```

Then, in the UI:

1. **DNS profiles** → create one per provider account and press **Test**
   ([OVH token guide](docs/runbooks/ovh-token.md)).
2. **Domains** → add your domains and attach a profile (wildcard certificate).
3. **Settings** → set the public UI host (e.g. `marineris.example.com`); once
   everything works, switch from the **staging** CA to production.
4. **Routes** → publish your apps.

Full guide: [docs/install.md](docs/install.md).

## Documentation

- [Installation](docs/install.md)
- [Architecture](docs/architecture.md) and [threat model](docs/threat-model.md)
- [Testing strategy](docs/testing.md)
- Runbooks: [break-glass access](docs/runbooks/break-glass.md) ·
  [locked out](docs/runbooks/lockout.md) ·
  [configuration rollback](docs/runbooks/rollback.md) ·
  [certificate problems](docs/runbooks/certificates.md) ·
  [OVH token](docs/runbooks/ovh-token.md) ·
  [backup and restore](docs/runbooks/backup-restore.md) ·
  [upgrade](docs/runbooks/upgrade.md)
- [Branding](branding/README.md) · [Name and logo policy](TRADEMARKS.md) ·
  [Security policy](SECURITY.md) ·
  [Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md)

## Roadmap

| Phase | Scope |
|---|---|
| 1 ✅ | Repository, CI, compose stack, first-run setup, containers/stacks (stack page with per-service status, actions, editing of mounts, labels and more, icons), routes (public, 2FA or admins only), multi-domain certificates with DNS profiles, passkeys, branding from the UI, Proxy & SSO overview |
| 2 | Users, groups and OIDC clients UI; per-route access by user and group (beyond "administrators only"); CrowdSec UI (decisions, allow-lists, alerts); dashboard; monitoring and alerts, with per-service CPU, memory and network graphs on the stack page; live log streaming; **Proxy & SSO editing**: Authelia settings from the UI (session lifetimes, login ban, password policy, e-mail notifier) and raw Caddy/Authelia edits kept across regenerations (validated overrides); mount options beyond read-only (tmpfs, bind propagation, volume sub-paths, NFS/CIFS volumes) |
| 3 | Backups (restic); **web terminal** inside containers (the stack page's Terminal tab: needs Docker `exec` through the socket proxy, with audit and step-up); stacks built from a Git repository or Dockerfile ("rebuild"); marketplace (Cosmos-compatible catalogs, compose templates, app icons) |
| 4 | Disk manager: SMART, mdadm RAID (incl. RAID10), privileged helper with a fixed command set |

## License

The code is licensed under the
[GNU Affero General Public License v3.0](LICENSE). If you run a modified
version as a network service, you must offer its source to its users.

The branding assets in [`branding/`](branding/) are under the same licence
with [additional terms](branding/LICENSE) (AGPL-3.0 section 7(c) and 7(e)):
forks that are distributed or offered over a network use their own name and
logo. See the [name and logo policy](TRADEMARKS.md).

The interface icons are [Lucide](https://lucide.dev) (ISC licence,
© Lucide Contributors); app icons are downloaded from
[dashboard-icons](https://github.com/homarr-labs/dashboard-icons) (Apache-2.0).
