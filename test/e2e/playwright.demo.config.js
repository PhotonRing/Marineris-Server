// Tests of the static demo (web/dist-demo, published on GitHub Pages), served
// under the same sub-path as on Pages. Build it first: npm run build:demo
// in web/.
import { defineConfig } from '@playwright/test';

// CI picks a free port (scripts/ci-free-ports.mjs): runners share a machine.
const port = process.env.E2E_DEMO_PORT || '18190';

export default defineConfig({
  testDir: './demo',
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${port}/Marineris-Server/`,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: `cd ../../web && VITE_DEMO=1 npx vite preview --outDir dist-demo --port ${port} --strictPort --host 127.0.0.1`,
    url: `http://127.0.0.1:${port}/Marineris-Server/`,
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
