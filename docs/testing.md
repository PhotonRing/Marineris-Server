# Testing strategy

Every layer is tested against the real thing whenever possible. Everything
below runs in GitHub Actions (see [When the CI runs](#when-the-ci-runs)).

| Layer | What | Where | Command |
|---|---|---|---|
| Unit | Crypto, store, auth (RFC 6238 vectors), validators, config builders, policy, apply/rollback engine | `internal/**/*_test.go` | `make test` |
| API security | Golden route table; every route × anonymous / missing CSRF / foreign origin / no Origin / cross-site / stale step-up; login, MFA, replay, throttling, fixation, enumeration; secrets never returned; failed apply reverts the DB | `internal/api/*_test.go` | `make test` |
| Fuzzing | Hostnames, paths, folders, upstream URLs, sealed values, password hashes, compose pre-scan, Caddy JSON injection | `Fuzz*` | `make fuzz` (CI: seed corpus with the unit tests, 4 min each weekly) |
| Real binaries | Generated Caddy config through `caddy validate` (every module and field exists); generated Authelia config through `authelia validate-config` | `TestCaddyValidate`, `TestAutheliaValidateConfig` | CI job *real-tools* |
| Browser e2e | Setup wizard, login, wrong credentials, every page without CSP violations, **passkey registration and login** with a virtual authenticator, cookie flags | `test/e2e` | `make e2e` |
| Integration | The real compose stack with a test ACME CA (Pebble + DNS): setup, certificates issued, routing, Authelia forward-auth and header spoofing, stack deployed through the socket proxy, CrowdSec ban enforced at the edge, rejected config rolled back with traffic intact, socket-proxy deny list, runtime hardening of every container | `test/integration` | `make integration` |
| Static analysis | golangci-lint (gosec, errorlint, staticcheck…), svelte-check, shellcheck, hadolint, actionlint-clean workflows | `.golangci.yml` | `make lint` |
| Supply chain & secrets | gitleaks (full history), govulncheck, npm audit, Trivy (images and IaC), CodeQL (Go, JS) | `ci.yml` | CI |
| Dynamic scan | OWASP ZAP baseline on both listeners, failing on missing security headers, cookie flags, information leaks | `ci.yml`, `.zap/rules.tsv` | CI |

Coverage gates (`scripts/coverage-gate.sh`) keep security-critical packages
above their minimums (validators 90 %, setup 80 %, …).

## When the CI runs

There are three workflows, and only one of them tests code:

| Workflow | Runs | Does |
|---|---|---|
| `ci.yml` | each push to a pull request; weekly; from the release | every test and scan below |
| `release.yml` | when `CHANGELOG.md` changes on `main` | if there is a new version: `ci.yml`, then signed multi-arch images, tag and GitHub release |
| `housekeeping.yml` | when a pull request closes; monthly | deletes merged branches and runs older than 30 days |

To keep the answer on a pull request quick:

- A newer push cancels the run of the older one, and documentation-only
  changes (`*.md`, `docs/`) run nothing.
- Each job runs only when its inputs changed (`scripts/changed-areas.sh`): a
  web UI change does not start the integration suite, a Playwright update
  only runs the browser tests, and so on. Changes to workflows or scripts run
  everything.
- The images are built **once** per run, for amd64 and with layer caching (an
  unchanged Caddy is not compiled again), then shared with the integration
  suite, the Caddy configuration check and Trivy.
- Multi-arch images are only built by the release. Both images cross-compile
  instead of compiling under arm64 emulation, which took over 30 minutes.
- Merges to `main` start nothing but the housekeeping (and the release when
  the changelog changes): what was merged was already tested on the pull
  request. Weekly, every job runs on `main` with 4 minutes of fuzzing per
  target, which is also the code-scanning baseline.

Before pushing, `make lint test` catches most failures locally.

## Operational checks (runbooks)

The runbooks in `docs/runbooks/` describe how to verify the behaviour on your
own server, e.g. after an upgrade:

1. `docker compose ps`: every service healthy.
2. `curl -sI https://<ui-host>` → Authelia redirect.
3. Certificates page: every host valid, not staging.
4. Audit log: no unexpected failures.
