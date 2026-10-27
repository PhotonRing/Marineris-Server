#!/bin/sh
# Tells the CI which parts of the project a pull request changes, so that
# jobs whose inputs did not change are skipped. Writes "area=true|false"
# lines to $GITHUB_OUTPUT (stdout when unset).
#
# Outside pull requests (schedule, release, manual run), and
# when the CI itself or the build scripts change, every area is "true".
#
# Usage: scripts/changed-areas.sh [base-sha head-sha]
set -eu

areas="go web e2e caddy deploy"
out="${GITHUB_OUTPUT:-/dev/stdout}"

emit_all() {
  for a in $areas; do echo "$a=true"; done >> "$out"
  exit 0
}

[ "$#" -eq 2 ] || emit_all
files="$(git diff --name-only "$1...$2")" || emit_all

go=false web=false e2e=false caddy=false deploy=false
for f in $files; do
  case "$f" in
    *.md|docs/*|LICENSE|branding/LICENSE) ;;
    .github/*|scripts/*|Makefile|.golangci.yml|.gitleaks.toml|.zap/*) emit_all ;;
    *.go|go.mod|go.sum|branding/*) go=true ;;
    web/*) web=true ;;
    test/e2e/*) e2e=true ;;
    deploy/caddy/*) caddy=true ;;
    Dockerfile|.dockerignore|deploy/*|test/integration/*) deploy=true ;;
  esac
done
{
  echo "go=$go"
  echo "web=$web"
  echo "e2e=$e2e"
  echo "caddy=$caddy"
  echo "deploy=$deploy"
} >> "$out"
