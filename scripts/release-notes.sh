#!/bin/sh
# Writes the GitHub release notes for a tag to stdout:
#   1. the CHANGELOG.md section of that version (required),
#   2. the list of commits since the previous tag,
#   3. the published images with their digests and how to verify them.
# Usage: scripts/release-notes.sh v0.1.0 [marineris-digest caddy-digest]
set -eu
tag="$1"
ver="${tag#v}"
repo="${GITHUB_REPOSITORY:-PhotonRing/Marineris-Server}"
owner="$(echo "$repo" | cut -d/ -f1 | tr '[:upper:]' '[:lower:]')"

section="$(awk -v v="$ver" '
  index($0, "## [" v "]") == 1 { on = 1; next }
  on && /^## \[/ { exit }
  on && /^\[[^]]+\]: / { next }
  on { print }
' CHANGELOG.md)"
if [ -z "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
  echo "CHANGELOG.md has no '## [$ver]' section: write the release notes first." >&2
  exit 1
fi

prev="$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)"
range="$tag"
[ -n "$prev" ] && range="$prev..$tag"

printf '%s\n\n' "$section"
echo "## Commits"
echo
git log --no-merges --format='- %s (%h)' "$range"
echo
if [ -n "$prev" ]; then
  echo "**Full changes:** https://github.com/$repo/compare/$prev...$tag"
  echo
fi

if [ "$#" -ge 3 ]; then
  cat <<EOT
## Container images

| Image | Digest |
|---|---|
| \`ghcr.io/$owner/marineris:$ver\` | \`$2\` |
| \`ghcr.io/$owner/marineris-caddy:$ver\` | \`$3\` |

Images are multi-arch (amd64, arm64), signed with Sigstore cosign (keyless) and ship an SBOM and build provenance. Verify before deploying:

\`\`\`sh
cosign verify ghcr.io/$owner/marineris@$2 \\
  --certificate-identity 'https://github.com/$repo/.github/workflows/release.yml@refs/heads/main' \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
\`\`\`
EOT
fi
