# Certificate problems

Open **Domains & certificates → Certificates**: Marineris performs a real TLS
handshake with Caddy for every served host.

| Status | Cause and fix |
|---|---|
| *staging* | Expected while **Settings → staging** is on. Switch to production once all hosts work. |
| "no certificate served" | Not issued yet (wait a minute) or issuance fails: check `docker logs marineris-caddy 2>&1 \| grep -i acme`. |
| DNS-01 errors (`401`, `403`, "not authorized") | Credentials or rights of the DNS profile: run **Test** on the profile. For OVH see [OVH token](ovh-token.md). |
| DNS-01 "timed out waiting for record to propagate" | Slow provider: raise *DNS propagation timeout* (Settings → Advanced), or set public resolvers (`1.1.1.1:53`). |
| HTTP-01 errors | Port 80 not reachable from the internet, or the DNS record does not point to this server. |
| "rateLimited" from Let's Encrypt | Production limits hit: switch back to staging, fix the cause, wait for the limit window (see letsencrypt.org/docs/rate-limits). |
| expires in < 14 days | Renewal is failing: see the Caddy logs as above. |

**Verify:** every host shows *valid* (or *staging* during tests) with the
expected issuer.
