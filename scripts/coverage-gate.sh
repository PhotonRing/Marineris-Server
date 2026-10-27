#!/bin/sh
# Fails when a security-critical package drops below its minimum coverage.
# Usage: go test -cover ./internal/... | scripts/coverage-gate.sh
set -eu
fail=0
while read -r line; do
  echo "$line"
  pkg=$(echo "$line" | awk '{print $2}')
  pct=$(echo "$line" | sed -n 's/.*coverage: \([0-9.]*\)% of statements.*/\1/p')
  [ -n "$pct" ] || continue
  min=0
  case "$pkg" in
    */internal/validate) min=90 ;;
    */internal/setup) min=80 ;;
    */internal/dnsprofiles|*/internal/certs) min=80 ;;
    */internal/caddy|*/internal/authelia) min=70 ;;
    */internal/api|*/internal/secrets|*/internal/configmgr) min=65 ;;
    */internal/auth|*/internal/stacks) min=60 ;;
  esac
  if [ "$(echo "$pct < $min" | bc)" -eq 1 ]; then
    echo "::error::$pkg coverage $pct% is below the required $min%"
    fail=1
  fi
done
exit $fail
