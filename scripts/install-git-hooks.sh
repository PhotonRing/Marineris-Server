#!/bin/sh
# Installs a pre-commit hook that runs gitleaks on staged changes and the
# forbidden-files check. Alternative to the pre-commit framework.
set -eu
root="$(git rev-parse --show-toplevel)"
hook="$root/.git/hooks/pre-commit"
cat > "$hook" <<'HOOK'
#!/bin/sh
set -eu
root="$(git rev-parse --show-toplevel)"
files="$(git diff --cached --name-only --diff-filter=ACMR)"
[ -z "$files" ] || "$root/scripts/check-forbidden-files.sh" $files
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks git --pre-commit --staged --redact --no-banner --config "$root/.gitleaks.toml"
else
  echo "warning: gitleaks not installed; secret scan skipped (go install github.com/zricethezav/gitleaks/v8@v8.30.1)" >&2
fi
HOOK
chmod +x "$hook"
echo "pre-commit hook installed"
