import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// The production build is embedded into the Go binary (internal/webui).
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0, // no data: URIs for scripts/styles (strict CSP)
    sourcemap: false,
  },
  server: {
    // `npm run dev` proxies the API to a local Marineris (local listener).
    proxy: { '/api': 'http://localhost:8081', '/branding': 'http://localhost:8081' },
  },
});
