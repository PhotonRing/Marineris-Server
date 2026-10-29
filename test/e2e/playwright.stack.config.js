// End-to-end tests against the whole stack (Caddy, Authelia, Valkey,
// CrowdSec) started by deploy/demo/demo.sh up: the UI's local listener on
// localhost:8081, the rest under *.marineris.localhost (Chromium resolves
// *.localhost to this computer). Start the demo first; the suite does the
// first-run setup itself, so it needs a fresh demo (demo.sh down, then up).
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './stack',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  // Authelia restarts on every change to its configuration (routes, domains,
  // settings, Users): steps wait for it.
  timeout: 10 * 60_000,
  expect: { timeout: 30_000 },
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: 'http://localhost:8081',
    // Certificates come from Caddy's local CA.
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
});
