// Tests of the static demo (web/dist-demo, published on GitHub Pages), served
// under the same sub-path as on Pages. Build it first: npm run build:demo
// in web/.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './demo',
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:18190/Marineris-Server/',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'cd ../../web && VITE_DEMO=1 npx vite preview --outDir dist-demo --port 18190 --strictPort --host 127.0.0.1',
    url: 'http://127.0.0.1:18190/Marineris-Server/',
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
