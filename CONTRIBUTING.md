# Contributing

## Development setup

Requirements: Go (version in `go.mod`), Node.js 22, Docker with compose.

```sh
scripts/install-git-hooks.sh       # gitleaks + forbidden-files pre-commit hook
make ui                            # build the web UI into internal/webui/dist
make test                          # unit and security tests (race detector)
make lint                          # golangci-lint (incl. gosec)
make e2e                           # Playwright end-to-end tests
make integration                   # full stack with a test ACME CA (needs sudo)
```

Run the UI with live reload against a local server:

```sh
make dev-server    # Marineris on 127.0.0.1:8081 with a throw-away data dir
cd web && npm run dev
```

## Rules

- **No secrets in the repository.** Real configuration lives in the
  git-ignored `data/` folder; ship `.example` files. The pre-commit hook and CI
  run gitleaks on every change.
- **Every new API endpoint** goes into the route table (`internal/api/routes.go`)
  with the least access level that works. The golden file
  `internal/api/testdata/routes.golden` must be updated
  (`go test ./internal/api -run Golden -update-golden`) and the change reviewed:
  it is the attack surface.
- **Every value written into a generated configuration** must go through an
  allow-list validator in `internal/validate` (with a fuzz test), never through
  escaping.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `test:`, `docs:`, …) and user-visible changes get an entry
  under *Unreleased* in `CHANGELOG.md`.
- Branch names are descriptive (`phase-2-users-ui`, `fix-cert-renewal-alert`).

## Releases

Releases are automatic: see the top of [CHANGELOG.md](CHANGELOG.md). After a
release, the workflow deletes every branch whose work is already in `main`.

By contributing you agree that your contributions are licensed under the
AGPL-3.0.
