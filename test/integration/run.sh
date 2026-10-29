#!/bin/sh
# Runs the integration suite against a real Marineris stack with a test ACME
# CA. Needs Docker, Go and root (sudo) for the data directory.
#
#   test/integration/run.sh            # build images, start, test, stop
#   KEEP=1 test/integration/run.sh     # leave the stack running afterwards
#
# Images are expected as marineris:$IT_TAG, marineris-caddy:$IT_TAG and
# marineris-it-pebble:dev (built here unless SKIP_BUILD=1).
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

IT_TAG="${IT_TAG:-dev}"
IT_BASE="${IT_BASE:-/opt/marineris-it}"
export IT_TAG IT_CERTS="$IT_BASE/certs"
DATA="$IT_BASE/data"
ENVFILE="$IT_BASE/it.env"
SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO="sudo"

BUILD_ARGS="${BUILD_ARGS:-}"
if [ "${SKIP_BUILD:-0}" != "1" ]; then
  # shellcheck disable=SC2086
  docker build $BUILD_ARGS -t "marineris:$IT_TAG" .
  # shellcheck disable=SC2086
  docker build $BUILD_ARGS -t "marineris-caddy:$IT_TAG" deploy/caddy
  # shellcheck disable=SC2086
  docker build $BUILD_ARGS -t marineris-it-pebble:dev test/integration/pebble
fi

compose() {
  docker compose -p marineris --env-file "$ENVFILE" -f deploy/compose.yml -f test/integration/compose.it.yml "$@"
}

cleanup() {
  status=$?
  if [ $status -ne 0 ] || [ "${KEEP:-0}" != "1" ]; then
    mkdir -p "$ROOT/test/integration/logs"
    for c in marineris marineris-caddy marineris-authelia marineris-valkey marineris-crowdsec marineris-socket-proxy marineris-it-pebble; do
      docker logs "$c" > "$ROOT/test/integration/logs/$c.log" 2>&1 || true
    done
  fi
  if [ "${KEEP:-0}" != "1" ]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
    docker rm -f it-web >/dev/null 2>&1 || true
    $SUDO rm -rf "$IT_BASE"
  fi
  exit $status
}
trap cleanup EXIT

$SUDO rm -rf "$IT_BASE"
$SUDO mkdir -p "$IT_BASE"
$SUDO chown "$(id -u):$(id -g)" "$IT_BASE"
go run ./test/integration/gencerts "$IT_CERTS"
$SUDO install -d -o 65532 -g 65532 -m 0700 "$DATA" "$DATA/stacks" "$DATA/static" "$DATA/authelia" "$DATA/authelia-notify" "$DATA/authelia-data" "$DATA/authelia-sessions"
$SUDO install -d -o 65532 -g 65532 -m 0700 "$DATA/secrets"

cat > "$ENVFILE" <<EOF
MARINERIS_DATA=$DATA
DOCKER_GID=$(stat -c %g /var/run/docker.sock)
MARINERIS_IMAGE=marineris:$IT_TAG
MARINERIS_CADDY_IMAGE=marineris-caddy:$IT_TAG
MARINERIS_ALLOWED_BIND_ROOTS=/srv,/mnt,/media
IT_CERTS=$IT_CERTS
EOF

compose up -d
go test -tags integration -count=1 -v -timeout 20m ./test/integration/
