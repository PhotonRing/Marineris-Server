# Upgrade

1. Read the release notes (they list the changes and the signed digests).
2. Verify the images (command in the release notes), e.g.:
   ```sh
   cosign verify ghcr.io/photonring/marineris:0.2.0 \
     --certificate-identity 'https://github.com/PhotonRing/Marineris-Server/.github/workflows/release.yml@refs/heads/main' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```
3. [Back up](backup-restore.md) the data directory.
4. Update the source and the image tags:
   ```sh
   cd /opt/marineris/src && sudo git fetch --tags && sudo git checkout v0.2.0
   cd deploy && sudo nano .env      # MARINERIS_IMAGE / MARINERIS_CADDY_IMAGE
   sudo docker compose pull && sudo docker compose up -d
   ```
5. Database migrations run automatically; a binary refuses to start on a
   database newer than itself (downgrades need the backup).

**Verify:** `docker compose ps` all healthy, the dashboard shows the new
version, **Proxy & SSO** → **History** shows a successful *startup* apply.
