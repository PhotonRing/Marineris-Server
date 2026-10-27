# Backup and restore

Automated restic backups arrive in Phase 3. Until then, back up these items:

| Item | Location | Notes |
|---|---|---|
| Marineris data | `/opt/marineris/data` (`MARINERIS_DATA`) | database, **master key**, secrets, stacks, Authelia files |
| Caddy data | volume `marineris_caddy-data` | certificates and ACME account (re-issued automatically if lost) |
| CrowdSec | volumes `marineris_crowdsec-db`, `marineris_crowdsec-config` | optional |
| Your apps | their volumes / bind mounts | |

The master key and the database belong together: one without the other cannot
decrypt the stored credentials. Store backups encrypted.

## Backup

```sh
cd /opt/marineris/src/deploy
sudo docker compose stop marineris        # consistent SQLite snapshot
sudo tar -C / -czf /root/marineris-data-$(date +%F).tgz opt/marineris/data
sudo docker compose start marineris
```

## Restore

```sh
cd /opt/marineris/src/deploy
sudo docker compose down
sudo rm -rf /opt/marineris/data
sudo tar -C / -xzf /root/marineris-data-YYYY-MM-DD.tgz
sudo docker compose up -d
```

**Verify:** log in, check **Configuration** (a startup apply is recorded) and
the **Certificates** page.
