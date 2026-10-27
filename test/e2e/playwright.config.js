// End-to-end tests of the web UI against a real Marineris binary (without
// Docker/Caddy: those are covered by test/integration).
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    // "localhost" (not 127.0.0.1): WebAuthn needs a domain as RP ID.
    baseURL: 'http://localhost:18181',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'sh ./serve.sh',
    url: 'http://127.0.0.1:18181/api/health',
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
