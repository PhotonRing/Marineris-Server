#!/bin/sh
# Runs every check CI runs before a push, in one command: gofmt, goimports,
# go vet, tests with the race detector, the coverage gate, golangci-lint,
# the forbidden-files guard and, when npm is there, the web build and check.
# Without a Go toolchain (e.g. on Windows), the Go part runs in the same
# pinned image the Dockerfile builds with.
# Usage: scripts/check.sh
set -eu
cd "$(dirname "$0")/.."

GO_IMAGE=golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb
GOLANGCI_VERSION=v2.15.0
GOIMPORTS_VERSION=v0.38.0
LOCAL_PREFIX=github.com/photonring/marineris-server

go_checks() {
  out=$(gofmt -l ./cmd ./internal ./test)
  [ -z "$out" ] || { echo "gofmt: run gofmt -w on:"; echo "$out"; exit 1; }
  out=$(go run golang.org/x/tools/cmd/goimports@$GOIMPORTS_VERSION -local $LOCAL_PREFIX -l ./cmd ./internal ./test)
  [ -z "$out" ] || { echo "goimports: third-party imports go in their own group before $LOCAL_PREFIX:"; echo "$out"; exit 1; }
  go vet ./...
  go vet -tags integration ./...
  go test -race -count=1 ./...
  go test -count=1 -cover ./internal/... | scripts/coverage-gate.sh
  GOTOOLCHAIN=local go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION run --build-tags integration ./...
}

if [ "${1:-}" = "--go-only" ]; then
  go_checks
  exit 0
fi

if command -v go >/dev/null 2>&1; then
  go_checks
else
  echo "== no Go toolchain: running the Go checks in $GO_IMAGE"
  MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -w /src \
    -v marineris-gomod:/go/pkg/mod -v marineris-gocache:/root/.cache/go-build \
    -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 \
    "$GO_IMAGE" sh -c 'apk add -q gcc musl-dev && sh scripts/check.sh --go-only'
fi

scripts/check-forbidden-files.sh

if command -v npm >/dev/null 2>&1; then
  (cd web && npm ci --no-audit --no-fund --silent && npm run build && npm run check && npm run test:scripts)
else
  echo "== npm not found: web build and check skipped"
fi
echo "== all checks passed"
