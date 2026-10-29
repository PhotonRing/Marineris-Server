# Architecture

```
                      internet
                         │ 80/443
                ┌────────▼─────────┐   access log    ┌──────────────┐
                │      Caddy       │────────────────▶│   CrowdSec   │
                │ TLS, routing,    │◀── decisions ───│   (LAPI)     │
                │ bouncer, limits  │                 └──────────────┘
                └─┬──────┬───────┬─┘
     forward-auth │      │ ui    │ apps network (internal)
          ┌───────▼──┐ ┌─▼──────────────┐   ┌──────────────────┐
          │ Authelia │ │   Marineris    │   │ your containers  │
          └──────────┘ │ API + web UI   │   └──────────────────┘
                       └─┬──────┬───────┘
          admin socket   │      │ docker-api network (internal)
          (unix, volume) │   ┌──▼───────────┐     ┌────────────────┐
           Caddy admin ◀─┘   │ socket proxy │────▶│ docker.sock(ro)│
                             └──────────────┘     └────────────────┘
          127.0.0.1:8081 ◀── SSH tunnel (setup / break-glass)
```

## Components

| Component | Role | Image |
|---|---|---|
| `marineris` | API, web UI, configuration engine. Single static Go binary. | distroless, UID 65532 |
| `marineris-init` | One-shot: data directory layout and generated secrets | same image |
| `caddy` | Edge proxy, ACME (DNS-01/HTTP-01), CrowdSec bouncer, rate limits | custom xcaddy build, UID 65532 |
| `authelia` | Forward-auth portal (2FA, passkeys) | upstream, UID 65532 |
| `valkey` | Authelia's session store: signing in survives Authelia's restarts (configuration changes, upgrades) | upstream, UID 65532 |
| `crowdsec` | Log analysis and ban decisions | upstream |
| `socket-proxy` | Allow-list in front of the Docker API | wollomatic/socket-proxy |

## Data and source of truth

Everything an administrator edits lives in SQLite (`data/marineris.db`, mode
0600). Credentials (DNS API keys, TOTP secrets, stack `.env`, configuration
history) are sealed with AES-256-GCM using the master key
`data/secrets/master.key` (0600). Each sealed value is bound to its row (the
GCM additional data), so ciphertexts cannot be swapped between rows.

Generated files:

| File | Consumer |
|---|---|
| Caddy JSON | pushed through the admin API (unix socket); Caddy persists it in its own volume |
| `data/authelia/configuration.yml`, `users_database.yml` | Authelia (read-only mount) |
| `data/secrets/authelia/*` | Authelia `*_FILE` variables; `redis` is also Valkey's password |
| `data/secrets/crowdsec-bouncer.key` (0640) | CrowdSec registers it; Caddy uses it |
| `data/stacks/<name>/compose.yaml`, `.env` (0600) | Docker Compose SDK |

The data directory is mounted at the **same absolute path** in the Marineris
container as on the host, so relative bind mounts in stacks resolve the same
way for the Docker daemon.

## Applying configuration

```
change ─▶ validate candidate (allow-list validators + full config build)
       ─▶ save in DB
       ─▶ render ─▶ validate with the real binary (Caddy /load is atomic;
                    authelia validate-config in a throw-away container)
       ─▶ snapshot (sealed history) ─▶ apply ─▶ health check
       ─▶ on failure: previous config restored, DB change reverted,
          attempt recorded as failed / rolled back
```

Caddy is checked through its admin API and a plain-HTTP probe listener that
only answers when the new HTTP app is running; Authelia through `/api/health`
after a restart. Authelia's users file is hot-reloaded, so a password change
does not restart it.

## Listeners

| Listener | Reachable from | Serves |
|---|---|---|
| edge `:8080` | Caddy only (`ui` network) | nothing but `/api/health` and `/api/meta` before setup; the UI after setup (behind Authelia by default) |
| local `:8081` | host loopback `127.0.0.1:8081` (SSH tunnel) | first-run setup, break-glass login (not affected by edge lockouts) |

## Hub metrics

CPU, memory, uptime and load come from `/proc` (not namespaced: they are the
host's values), disk usage from the filesystem of the data directory. Network
counters are per namespace, so compose.yml bind-mounts the host's
`/proc/1/net/dev` read-only at `/host/net-dev` (counters only, no other host
information). Hub icons, service icons and branding images are stored in the database and
served by Marineris (the browser never loads third-party images); remote
icons are downloaded once over HTTPS from public addresses only. After a stack
is deployed, Marineris looks up an icon for each service that has none on the
dashboard-icons CDN (cdn.jsdelivr.net), by its `io.marineris.icon` label or
its image name: the only request it makes without being asked to.

## Networks

All networks except `edge` (Caddy's internet side), `crowdsec` (hub updates)
and `outbound` (Marineris → DNS provider APIs) are `internal: true`. Your
applications join `marineris-apps`, shared only with Caddy.
