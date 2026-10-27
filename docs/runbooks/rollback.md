# Configuration rollback

Marineris already rolls back automatically when a new configuration fails to
load or fails its health check (the failed attempt is listed as *failed* or
*rolled back* in **Configuration**). Use this runbook when a change was applied
successfully but does something you did not want.

1. **Configuration** → choose *Caddy* or *Authelia*.
2. Find the last good version (*superseded*), press **View** to check it, then
   **Restore** (asks you to confirm your identity).
3. The restored version is applied through the same validate → apply →
   health-check cycle.

Note: restoring a configuration does not change the database (routes,
domains…). Fix or revert those in their pages, otherwise the next change will
render them again.

From the command line (UI unreachable): use [break-glass](break-glass.md).

**Verify:** the restored version is now *applied* at the top of the history and
the affected sites answer as expected.
