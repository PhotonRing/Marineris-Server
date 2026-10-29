#!/bin/sh
# Refuses files that must never be committed: runtime data, real .env files,
# keys, and assistant metadata. Used by pre-commit and CI.
set -eu
status=0
if [ "$#" -eq 0 ]; then
  # A re-run with no files has nothing to check. Without this guard an empty
  # file list would re-run the script with no arguments, forever.
  [ -z "${CHECK_FORBIDDEN_FILES_CHILD:-}" ] || exit 0
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "not inside a git repository: pass the files to check as arguments" >&2
    exit 2
  fi
  # Re-run with every tracked file as an argument (NUL-separated: safe with
  # spaces); -r runs nothing when there are no files.
  git ls-files -z | CHECK_FORBIDDEN_FILES_CHILD=1 xargs -0 -r sh "$0"
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
