#!/bin/sh
# Deletes finished GitHub Actions runs (with their logs and artifacts) that
# are older than a number of days, to keep the Actions page readable. 0 days
# deletes every finished run. The run executing this script is never deleted.
# Needs GH_TOKEN with actions:write.
# Usage: scripts/prune-runs.sh [days]   (default 30)
set -eu
repo="${GITHUB_REPOSITORY:?}"
days="${1:-30}"
case "$days" in '' | *[!0-9]*) echo "days must be a number" >&2; exit 1 ;; esac
cutoff="$(date -u -d "-$days days" +%Y-%m-%dT%H:%M:%SZ)"
self="${GITHUB_RUN_ID:-0}"

ids="$(gh api --paginate "repos/$repo/actions/runs?per_page=100" \
  --jq ".workflow_runs[] | select(.status == \"completed\" and .created_at < \"$cutoff\" and .id != $self) | .id")"
n=0
for id in $ids; do
  gh api -X DELETE "repos/$repo/actions/runs/$id" >/dev/null && n=$((n + 1))
done
echo "### Run history" >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
echo "- deleted $n finished runs older than $days days" >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
