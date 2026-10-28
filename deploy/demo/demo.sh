#!/bin/sh
# Runs the whole Marineris stack on this computer, to try the current code
# without a server: Marineris, Caddy, Authelia, CrowdSec and the socket proxy.
# Needs Linux (or WSL 2 on Windows) with Docker and its compose plugin;
# nothing else (no Go, no sudo).
#
#   deploy/demo/demo.sh up       build the images from this checkout and start
#   deploy/demo/demo.sh token    print the first-run setup token again
#   deploy/demo/demo.sh code     print Authelia's latest one-time code
#   deploy/demo/demo.sh down     stop and delete everything the demo created
#
# SKIP_BUILD=1 reuses existing images (marineris:demo, marineris-caddy:demo);
# BUILD_ARGS adds "docker build" options, e.g. for a proxy.
#
# Ports 80 and 443 of 127.0.0.1 and port 8081 must be free. The demo uses the
# same container names as a real installation: do not run both on one host.
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# The data folder is mounted at the same path in the containers, whose
# unprivileged user cannot enter /root: root uses /opt instead.
default_base="$HOME/.marineris-demo"
case "$HOME" in /root | /root/*) default_base=/opt/marineris-demo ;; esac
BASE="${MARINERIS_DEMO_DIR:-$default_base}"
ENVFILE="$BASE/demo.env"
# Docker Desktop (WSL) cannot bind-mount a folder deleted and created again at
# the same path: each demo gets a data folder of its own, kept until "down".
if [ -f "$ENVFILE" ]; then
  DATA="$(sed -n 's/^MARINERIS_DATA=//p' "$ENVFILE")"
else
  DATA="$BASE/data-$(date +%Y%m%d%H%M%S)"
fi
# Steps that need root (file ownership) run in the demo's Caddy image: the
# demo needs no other image.
HELPER="--user 0 --entrypoint sh marineris-caddy:demo"

compose() {
  docker compose -p marineris --env-file "$ENVFILE" \
    -f "$ROOT/deploy/compose.yml" -f "$ROOT/deploy/demo/compose.demo.yml" "$@"
}

token() {
  docker logs marineris 2>&1 | sed -n 's/.*"setup_token":"\([A-Za-z0-9_-]*\)".*/\1/p' | tail -n 1
}

# Without an e-mail server, Authelia writes its messages to a file.
code() {
  docker exec marineris-authelia cat /notifications/notification.txt 2>/dev/null |
    sed -n 's/^[[:space:]]*\([A-Z0-9]\{8\}\)[[:space:]]*$/\1/p' | tail -n 1
}

# Docker Desktop's WSL integration writes "credsStore": "desktop.exe" in the
# Docker configuration; the helper is a Windows program that fails ("exec
# format error") when the distribution cannot run Windows programs (interop
# disabled, often by systemd), and every image pull then fails. The demo only
# pulls public images: it then uses a copy of the configuration without the
# helper, and leaves the user's own untouched.
skip_broken_credential_helper() {
  conf="${DOCKER_CONFIG:-$HOME/.docker}"
  [ -f "$conf/config.json" ] || return 0
  store=$(sed -n 's/.*"credsStore"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$conf/config.json" | head -n 1)
  [ -n "$store" ] || return 0
  docker-credential-"$store" list </dev/null >/dev/null 2>&1 && return 0

  echo "==> docker-credential-$store cannot run here: pulling without it (public images only)"
  alt="$BASE/docker-config"
  rm -rf "$alt"
  mkdir -p "$alt"
  # Contexts, CLI plugins and buildx state stay shared with the real configuration.
  for f in "$conf"/*; do
    [ -e "$f" ] && [ "${f##*/}" != config.json ] && ln -s "$f" "$alt/"
  done
  context=$(sed -n 's/.*"currentContext"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$conf/config.json" | head -n 1)
  if [ -n "$context" ]; then
    printf '{"currentContext": "%s"}\n' "$context" > "$alt/config.json"
  else
    echo '{}' > "$alt/config.json"
  fi
  DOCKER_CONFIG="$alt"
  export DOCKER_CONFIG
}

up() {
  docker compose version >/dev/null 2>&1 || { echo "Docker with the compose plugin is required." >&2; exit 1; }
  sock=/var/run/docker.sock
  [ -S "$sock" ] || { echo "No Docker socket at $sock (Linux or WSL 2 is required)." >&2; exit 1; }
  mkdir -p "$BASE"
  skip_broken_credential_helper

  if [ "${SKIP_BUILD:-0}" = "1" ]; then
    echo "==> Using the existing images (SKIP_BUILD=1)"
  else
    echo "==> Building the images from $ROOT (the first build takes a few minutes)"
    # shellcheck disable=SC2086
    docker build -q ${BUILD_ARGS:-} -t marineris:demo "$ROOT" >/dev/null
    # shellcheck disable=SC2086
    docker build -q ${BUILD_ARGS:-} -t marineris-caddy:demo "$ROOT/deploy/caddy" >/dev/null
  fi

  echo "==> Preparing $BASE"
  mkdir -p "$BASE"
  # Directories owned by the unprivileged user of the containers, created
  # inside a container: no sudo needed.
  # shellcheck disable=SC2086
  docker run --rm -v "$BASE:/base" $HELPER -c '
    set -e
    for d in "" stacks static authelia authelia-notify authelia-data secrets; do
      install -d -o 65532 -g 65532 -m 0700 "/base/$1/$d"
    done' sh "${DATA##*/}"

  cat > "$ENVFILE" <<EOF
MARINERIS_DATA=$DATA
DOCKER_GID=$(stat -c %g "$sock")
MARINERIS_IMAGE=marineris:demo
MARINERIS_CADDY_IMAGE=marineris-caddy:demo
MARINERIS_ALLOWED_BIND_ROOTS=$DATA/stacks
EOF

  # Docker Desktop creates a missing bind-mount source as a directory when
  # compose creates the containers, before marineris-init runs: the secrets
  # mounted as files (CrowdSec's bouncer key) must exist first.
  docker run --rm --user 65532:65532 --read-only --network none --cap-drop ALL \
    --security-opt no-new-privileges:true -e MARINERIS_DATA_DIR="$DATA" \
    -v "$DATA:$DATA" marineris:demo init >/dev/null

  echo "==> Starting the stack"
  compose up -d --quiet-pull
  i=0
  until docker exec marineris /usr/local/bin/marineris healthcheck >/dev/null 2>&1; do
    i=$((i + 1))
    [ "$i" -lt 60 ] || { echo "Marineris did not start; see: docker logs marineris" >&2; exit 1; }
    sleep 2
  done

  cat <<EOF

Marineris is running on this computer.

1. First-run setup: open http://localhost:8081
   (use "localhost", not 127.0.0.1, so that passkeys work)
   Setup token: $(token)

2. In the UI:
   - Domains: add  marineris.localhost  (no DNS profile)
   - Settings: public UI host  marineris.localhost
   - Routes: add  hello.marineris.localhost  -> container demo-hello,
     port 80, protected by Authelia

3. Open https://hello.marineris.localhost (and https://marineris.localhost).
   Certificates come from Caddy's local CA: the browser warns once per
   site, accept it (Advanced -> Proceed).
   Authelia (https://auth.marineris.localhost) asks for your password, then
   to register a second factor: "Register device", then "Add". It confirms
   with a one-time code that the demo prints with:
     deploy/demo/demo.sh code

Stop and delete everything:  deploy/demo/demo.sh down
EOF
}

down() {
  if [ -f "$ENVFILE" ]; then
    compose down -v --remove-orphans
  fi
  if [ -d "$BASE" ]; then
    # Files belong to the containers' user: delete them from a container too.
    # shellcheck disable=SC2086
    docker run --rm -v "$BASE:/base" $HELPER -c 'rm -rf /base/data /base/data-* /base/demo.env'
    rm -rf "$BASE/docker-config"
    rmdir "$BASE" 2>/dev/null || true
  fi
  echo "The demo is stopped and its data deleted."
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  token) token ;;
  code) code ;;
  *) echo "usage: $0 up|token|code|down" >&2; exit 2 ;;
esac
