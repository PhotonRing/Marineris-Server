#!/bin/sh
# Refuses files that must never be committed: runtime data, real .env files,
# keys, and assistant metadata. Used by pre-commit and CI.
set -eu
status=0
if [ "$#" -eq 0 ]; then
  # Re-run with every tracked file as an argument (NUL-separated: safe with spaces).
  git ls-files -z | xargs -0 "$0"
  exit $?
fi
for f in "$@"; do
  case "$f" in
    data/*|*/data/secrets/*|deploy/data/*|*.db|*.key|*.pem|*.p12|*.pfx|CLAUDE.md|*/CLAUDE.md|.claude/*|*/.claude/*)
      echo "forbidden file: $f" >&2; status=1 ;;
    .env|*/.env|.env.*|*/.env.*)
      case "$f" in *.example) ;; *) echo "forbidden file: $f" >&2; status=1 ;; esac ;;
  esac
done
exit $status
