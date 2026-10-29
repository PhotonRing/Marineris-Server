# Locked out

All commands run on the server (root), inside the Marineris container. Every
command is written to the audit log.

| Situation | Command |
|---|---|
| Forgot the password | `docker exec marineris marineris admin reset-password <user>` — prints a new random password |
| Lost the authenticator **and** the recovery codes | `docker exec marineris marineris admin reset-mfa <user>` — prints a new TOTP secret and new recovery codes |
| Lost a device with a passkey | log in with password + TOTP and remove it in **Account**, or `docker exec marineris marineris admin remove-passkeys <user>` |
| Suspect a stolen session | `docker exec marineris marineris admin revoke-sessions <user>` |
| "Too many failed attempts" on the public UI | use the [break-glass](break-glass.md) listener (not affected); public lockouts expire after at most 1 hour |
| List users, their groups and the owner | `docker exec marineris marineris admin users` |

After `reset-password` or `reset-mfa`, restart Marineris
(`docker restart marineris`) so the new password or authenticator also
reaches Authelia (the portal uses the same ones), then change the password in
**Account**.

**Verify:** you can log in on `http://localhost:8081` through the SSH tunnel,
and the audit log shows the `cli.*` entry.
