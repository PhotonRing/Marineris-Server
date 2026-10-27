#!/bin/sh
# Starts a throw-away Marineris for the e2e tests. Docker and Caddy point to
# closed ports on purpose: the UI must cope with them being unreachable.
set -eu
BIN="${MARINERIS_BIN:-../../bin/marineris}"
DATA="$(pwd)/.data"
rm -rf "$DATA"
mkdir -p "$DATA"
export MARINERIS_DATA_DIR="$DATA"
export MARINERIS_EDGE_ADDR=127.0.0.1:18180
export MARINERIS_LOCAL_ADDR=127.0.0.1:18181
export MARINERIS_DOCKER_HOST=tcp://127.0.0.1:1
export MARINERIS_CADDY_ADMIN=http://127.0.0.1:2
export MARINERIS_CADDY_PROBE_ADDR=127.0.0.1:3
export MARINERIS_CADDY_TLS_ADDR=127.0.0.1:4
exec "$BIN" serve
