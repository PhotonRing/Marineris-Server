# Security policy

Marineris runs on internet-facing servers and holds credentials that control
them (DNS API keys, Docker access). Security reports are handled first.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's
[private vulnerability reporting](https://github.com/PhotonRing/Marineris-Server/security/advisories/new)
with:

- the affected version or commit,
- steps to reproduce or a proof of concept,
- the impact you expect.

You will get an acknowledgement within 72 hours. Fixes are released as a
patch version with a GitHub security advisory; reporters are credited unless
they prefer otherwise.

## Supported versions

Before 1.0, only the latest release receives security fixes.

## Scope

In scope: the Marineris server and UI, the generated Caddy/Authelia
configuration, the compose stack in `deploy/`, the release images.

Out of scope: vulnerabilities in Caddy, Authelia, CrowdSec, Docker or the
socket proxy themselves (report them upstream), and findings that need an
already-compromised administrator account or root access on the host.

## Design notes

See [docs/threat-model.md](docs/threat-model.md) for the assets, trust
boundaries and mitigations, and [docs/testing.md](docs/testing.md) for how
they are tested.
