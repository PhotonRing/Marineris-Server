# Installation

## 1. Prerequisites

- Linux server (amd64 or arm64), Docker Engine 24+ with the compose plugin.
- Ports **80/tcp, 443/tcp and 443/udp** open to the internet; SSH for you.
- DNS: an `A`/`AAAA` record for every host you will serve (or a wildcard
  `*.example.com`) pointing to the server.
- Recommended: a host firewall allowing only 22, 80 and 443.

> **Client IPs and IPv6.** Rate limiting and CrowdSec need the real client IP.
> Docker preserves it for IPv4. If clients reach the server over IPv6 while
> containers only have IPv4, Docker's userland proxy replaces the address with
> its own: enable IPv6 in Docker (`"ipv6": true`, `"ip6tables": true` in
> `/etc/docker/daemon.json`) or disable the userland proxy.

## 2. Install

```sh
sudo git clone https://github.com/PhotonRing/Marineris-Server.git /opt/marineris/src
cd /opt/marineris/src/deploy
sudo ./install.sh
```

`install.sh` creates `.env` from `.env.example`, creates the data directory
(`/opt/marineris/data`, owned by UID 65532, mode 0700) and records the group
of the Docker socket. Edit `.env`:

- `MARINERIS_IMAGE` / `MARINERIS_CADDY_IMAGE`: pin a release, e.g.
  `ghcr.io/photonring/marineris:0.1.6` (and verify it, see the release notes).
- `MARINERIS_ALLOWED_BIND_ROOTS`: host folders your stacks may mount
  (default `/srv,/mnt,/media`).

Start:

```sh
sudo docker compose up -d
sudo docker compose ps          # marineris and authelia report "healthy"
```

## 3. First-run setup (over SSH)

Until setup is complete, Marineris has **no account** and its public side
answers only "setup pending". The setup page is served **only** on the
loopback listener and needs the setup token:

```sh
sudo docker logs marineris 2>&1 | grep setup_token
```

On your computer:

```sh
ssh -L 8081:127.0.0.1:8081 you@your-server
```

Open **http://localhost:8081** (write `localhost`: passkeys do not work on an
IP address) and follow the wizard: token → administrator name → authenticator
app (scan the QR code) → password, Let's Encrypt e-mail, optional public UI
host → **store the recovery codes**.

## 4. First configuration

1. **DNS profiles** → *New profile* for each provider account; press **Test**
   with one of its zones. See [OVH token](runbooks/ovh-token.md).
2. **Domains & certificates** → add each domain and choose its profile
   (wildcard on). Domains without a profile use HTTP-01.
3. **Settings** → *Public hostname* of the UI, e.g. `marineris.example.com`
   (must be inside one of your domains). The UI is then behind Authelia,
   whose sign-in opens it.
4. Open the public UI (`https://<public hostname>`): Authelia signs you in
   with the same password and authenticator app (Marineris registers them in
   Authelia), and that one sign-in opens the UI, the portal and every
   protected route. To sign in with a **passkey** everywhere, add it in
   **Account → Passkeys → Authelia portal**.
5. **Routes** → publish applications.
6. When certificates show as *staging* everywhere and everything works,
   **Settings → untick "staging"**. Production certificates replace them.

## 5. Day-to-day

- Updates: [upgrade runbook](runbooks/upgrade.md).
- Backups (until Phase 3 automates them): [backup and restore](runbooks/backup-restore.md).
- If anything goes wrong: [rollback](runbooks/rollback.md),
  [break-glass](runbooks/break-glass.md), [locked out](runbooks/lockout.md).
