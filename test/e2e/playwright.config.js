// End-to-end tests of the web UI against a real Marineris binary (without
// Docker/Caddy: those are covered by test/integration).
import { defineConfig } from '@playwright/test';

// CI picks free ports (scripts/ci-free-ports.mjs): runners share a machine.
const local = process.env.E2E_LOCAL_PORT || '18181';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    // "localhost" (not 127.0.0.1): WebAuthn needs a domain as RP ID.
    baseURL: `http://localhost:${local}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'sh ./serve.sh',
    url: `http://127.0.0.1:${local}/api/health`,
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
