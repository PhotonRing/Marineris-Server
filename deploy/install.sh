#!/bin/sh
# Prepares the host for the Marineris stack. Safe to run more than once.
set -eu

cd "$(dirname "$0")"

if [ ! -f .env ]; then
  cp .env.example .env
  echo "Created .env from .env.example"
fi

# shellcheck disable=SC1091
. ./.env

case "${MARINERIS_DATA:-}" in
  /*) ;;
  *) echo "MARINERIS_DATA must be an absolute path" >&2; exit 1 ;;
esac

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root (it creates $MARINERIS_DATA and reads the Docker socket group)." >&2
  exit 1
fi

# Data directory owned by the unprivileged UID used by Marineris and Authelia.
install -d -o 65532 -g 65532 -m 0700 "$MARINERIS_DATA"
install -d -o 65532 -g 65532 -m 0700 "$MARINERIS_DATA/stacks" "$MARINERIS_DATA/static" "$MARINERIS_DATA/authelia" "$MARINERIS_DATA/authelia-notify" "$MARINERIS_DATA/authelia-data" "$MARINERIS_DATA/authelia-sessions"
install -d -o 65532 -g 65532 -m 0700 "$MARINERIS_DATA/secrets"

gid="$(stat -c %g /var/run/docker.sock)"
if grep -q '^DOCKER_GID=' .env; then
  sed -i "s/^DOCKER_GID=.*/DOCKER_GID=$gid/" .env
else
  echo "DOCKER_GID=$gid" >> .env
fi
echo "DOCKER_GID set to $gid"

echo
echo "Next steps:"
echo "  docker compose up -d"
echo "  docker logs marineris 2>&1 | grep setup_token"
echo "  ssh -L 8081:127.0.0.1:${MARINERIS_LOCAL_PORT:-8081} you@this-server   # from your computer"
echo "  open http://localhost:8081"
