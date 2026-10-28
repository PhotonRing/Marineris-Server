# Break-glass access

Use when the public UI is unreachable (Caddy or Authelia broken, DNS or
certificate problem, public UI host misconfigured).

1. From your computer:
   ```sh
   ssh -L 8081:127.0.0.1:8081 you@your-server
   ```
2. Open **http://localhost:8081** and log in with password + TOTP (or a
   passkey registered on `localhost`). A *break-glass* badge is shown.
   Lockouts caused by failed attempts on the public side do not apply here.
3. Fix the problem: **Proxy & SSO** → **History** (restore a previous version), **Settings**,
   **Routes**, **Domains**.

If Authelia itself is broken and you need the public UI without it:

```sh
sudo docker exec marineris marineris admin disable-ui-authelia
sudo docker restart marineris
```

Re-enable *Require an Authelia login* in **Settings** afterwards.

**Verify:** `docker compose ps` shows every service healthy; the public UI host
redirects to the Authelia portal again.
