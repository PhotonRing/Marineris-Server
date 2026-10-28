# Threat model

## Assets

1. **Host control.** Docker access equals root on the host.
2. **DNS credentials.** They can redirect your domains or obtain certificates
   for them.
3. **Admin identity**: password hash, TOTP secret, passkeys, sessions.
4. **TLS private keys** (Caddy's volume) and the master key.
5. **Availability** of the published applications.

## Adversaries

- Anonymous internet clients (scanners, credential stuffing, exploit kits).
- A compromised application container on the apps network.
- A malicious or careless compose file (copied from the internet, later from a
  marketplace).
- A stolen browser session or a malicious website visited by the admin (CSRF,
  clickjacking).
- Not in scope: root on the host, a malicious administrator, supply-chain
  compromise of Docker/Caddy/Authelia themselves (mitigated by pinning and
  scanning only).

## Trust boundaries and mitigations

| Threat | Mitigation | Tested by |
|---|---|---|
| Instance hijacked before setup | Edge serves only health/meta until setup; setup is local-listener-only and needs a one-time token from the logs/data dir; token rotates after 10 bad guesses; setup cannot run twice | `TestEdgeServesNothingBeforeSetup`, `TestSetupFlow`, setup unit tests, e2e |
| Password guessing | argon2id (64 MiB), per-IP and per-account exponential lockout on the edge, IP-only on the loopback listener (no lock-out of the owner), Caddy rate limit on `/api/auth/*`, CrowdSec | `TestLoginThrottlingAndEnumeration`, throttle tests |
| Account enumeration | Same error and same work (dummy hash) for unknown users | `TestLoginThrottlingAndEnumeration` |
| Second factor bypass / replay | TOTP mandatory; each time step usable once (atomic DB update); MFA token single-use with 5 attempts; recovery codes single-use; passkeys require user verification | `TestLoginFlowAndReplay`, `TestMFAAttemptLimit`, `TestRecoveryCodeLoginOnce`, `TestTOTPStepConcurrentSingleUse` |
| Session theft / fixation | 256-bit tokens stored hashed; `__Host-` cookie, Secure, HttpOnly, SameSite=Strict; idle (30 min) and absolute (12 h) expiry; regenerated at login; password change revokes other sessions | `TestSessions*`, `TestSessionFixation`, `TestPasswordChangeRevokesOtherSessions`, e2e cookie test |
| CSRF / cross-origin | Origin must be the UI origin, `Sec-Fetch-Site` must be same-origin, per-session CSRF header token, SameSite=Strict | `TestAccessMatrix` (every route) |
| Clickjacking, XSS | `frame-ancestors 'none'`, strict CSP (no inline script/style), SVG branding sandboxed, Svelte escaping | `TestSecurityHeaders`, e2e CSP check, ZAP |
| Sensitive actions with a stolen session | Step-up (password+TOTP or passkey within 10 min) for settings, credentials, deletions, rollbacks, unsafe stacks | `TestAccessMatrix` (step-up routes), `TestStepUpFlow` |
| Attack surface creep | Single route table with access levels; golden file must be updated and reviewed; only seven public endpoints allowed (the seventh serves the branding images) | `TestRouteTableGolden`, `TestOnlyExpectedRoutesArePublic` |
| Config injection (Caddy JSON, Authelia YAML, compose) | Allow-list validators for every value (hostnames, paths, names, URLs); configs built as data structures, never strings | validator tests + fuzzing, `FuzzRouteFieldsCannotInjectJSON`, `TestInvalidRouteNeverReachesDBOrCaddy` |
| Identity header spoofing behind Authelia | Remote-* headers deleted before Authelia's are copied, and on the UI route even when Authelia does not protect it. The UI trusts `Remote-User` as the sign-in only on the edge listener and only while Authelia protects the UI; a session opened that way never counts as a recent confirmation (sensitive actions still need a step-up), and ends when Authelia vouches for nobody or for someone else | `TestForwardAuthStripsSpoofedHeaders`, `TestUnprotectedUIStripsIdentityHeaders`, `TestAutheliaSignIn*`, `TestRemoteUserIgnoredWithoutAuthelia`, integration test |
| Adding a passkey to someone's Authelia account | The page that adds Authelia passkeys (on the portal's host) needs a ticket from a step-up route: 256-bit, single user, single portal host, single passkey, 5 min, spent by the first finish attempt, sent in a header (never in a logged URL). Its requests must come from a portal host; the passkey is verified by Marineris (relying party = the portal's host, user verification required) before Authelia's own import command stores it. The page and its endpoints exist on the edge listener only | `TestAddAutheliaPasskeyFromTheUI`, `TestAutheliaPasskeyRefusals`, `TestAddAutheliaPasskey`, `TestPortalPasskeyPageRoute` |
| SSRF through "external host" routes | Loopback, link-local (cloud metadata), multicast, unspecified addresses refused | `TestUpstreamURL`, fuzz |
| Path traversal in static routes | Folder names allow-listed, no `/` or `..`; dotfiles hidden | validator tests, fuzz |
| Docker API abuse | Raw socket never mounted in Marineris; socket proxy allow-list (no exec, build, commit, archive, attach, swarm, plugins); only Marineris may connect; bind mounts limited to allowed roots | `TestSocketProxyAllowList` (integration) |
| Malicious compose file | Policy: no privileged, host namespaces, dangerous caps, devices, unconfined profiles, docker.sock or system paths, files outside the stack dir, include/extends of other files, builds, the reserved `io.marineris.system` label; process environment never interpolated; edits from the stack page re-checked before they are saved | stack policy tests, `TestInterpolationDoesNotSeeProcessEnv`, `TestStackServices` |
| A user who is not an administrator reaches an admin-only app (or the Marineris UI) | Authelia rule for group `admins`, followed by a deny for the same resource, most specific resources first | `TestAdminOnlyRules`, integration test |
| Admin locks themselves out | System containers cannot be stopped/removed from the UI; break-glass listener; CLI recovery commands | `TestSystemContainersProtected`, [runbooks](runbooks/lockout.md) |
| Broken config takes the site down | Validation with real binaries, health checks, automatic rollback, DB revert | configmgr tests, `TestFailedApplyRevertsDatabase`, integration rollback test |
| Credential leakage | Sealed at rest, never returned by the API, redacted from config views and provider errors, `.env`/data git-ignored, gitleaks in pre-commit and CI | `TestSecretsNeverReturned`, `TestRedact*`, `TestTestFailuresCleanUpAndRedact`, gitleaks |
| Container escape impact | All containers non-root (except CrowdSec, upstream image), read-only, `cap_drop: ALL`, `no-new-privileges`, memory/PID limits, internal networks | `TestHardening` (integration) |
| Vulnerable dependencies | Pinned digests and versions, Dependabot, govulncheck, npm audit, Trivy, CodeQL | CI workflow (pull requests and weekly) |

## Known limitations

- Bind-mount checks are lexical: a symlink inside an allowed root that points
  elsewhere is followed by the Docker daemon. Only create symlinks there that
  you trust.
- The socket proxy cannot inspect `privileged` or capability flags in container
  creation requests; that check is done by Marineris' stack policy.
- CrowdSec runs as root (upstream image) with all capabilities dropped.
- Caddy persists its configuration, which contains the DNS credentials, in its
  own volume (needed to keep serving if Marineris is down).
