#!/bin/sh
# After a release, deletes every branch except the default one whose work is
# already in it: either fully merged, or the head of a merged pull request
# (squash/rebase merges). Branches with unmerged work are kept and listed,
# so nothing is ever lost. Needs GH_TOKEN with contents:write.
set -eu
repo="${GITHUB_REPOSITORY:?}"
default="$(gh api "repos/$repo" --jq .default_branch)"
deleted=""
kept=""
for b in $(gh api --paginate "repos/$repo/branches" --jq '.[].name'); do
  [ "$b" = "$default" ] && continue
  sha="$(gh api "repos/$repo/branches/$b" --jq .commit.sha)"
  ahead="$(gh api "repos/$repo/compare/$default...$sha" --jq .ahead_by)"
  merged_pr="$(gh api "repos/$repo/pulls?state=closed&head=${repo%%/*}:$b&per_page=100" \
    --jq "[.[] | select(.merged_at != null and .head.sha == \"$sha\")] | length")"
  if [ "$ahead" = "0" ] || [ "$merged_pr" != "0" ]; then
    if gh api -X DELETE "repos/$repo/git/refs/heads/$b" >/dev/null 2>&1; then
      deleted="$deleted $b"
    else
      kept="$kept $b(protected)"
    fi
  else
    kept="$kept $b($ahead-unmerged-commits)"
  fi
done
{
  echo "### Branch cleanup"
  echo "- deleted:${deleted:- none}"
  echo "- kept (unmerged work):${kept:- none}"
} >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
if [ -n "$kept" ]; then
  echo "::warning::branches with unmerged work were kept:$kept"
fi
