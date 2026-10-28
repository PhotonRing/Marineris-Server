import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import fs from 'node:fs';
import path from 'node:path';

// The production build is embedded into the Go binary (internal/webui).
// VITE_DEMO=1 builds the static demo instead (dist-demo, for GitHub Pages):
// the API is answered in the browser (src/demo) and the site lives under a
// sub-path, VITE_DEMO_BASE (default /Marineris-Server/).
const demo = process.env.VITE_DEMO === '1';
const base = demo ? process.env.VITE_DEMO_BASE || '/Marineris-Server/' : '/';

// The branding is served by the Go server at /branding; the demo ships a copy
// of it under its sub-path.
function demoBranding() {
  const src = path.resolve(import.meta.dirname, '../branding');
  const rewrite = (code) => code.replaceAll('/branding/', `${base}branding/`);
  return {
    name: 'marineris-demo-branding',
    enforce: 'pre',
    transform(code, id) {
      if (/\.(svelte|js|css)$/.test(id) && code.includes('/branding/')) return { code: rewrite(code), map: null };
      return null;
    },
    transformIndexHtml: rewrite,
    writeBundle(options) {
      const out = path.join(options.dir, 'branding');
      fs.mkdirSync(out, { recursive: true });
      for (const f of fs.readdirSync(src)) if (/\.(svg|png|webp|json)$/.test(f)) fs.copyFileSync(path.join(src, f), path.join(out, f));
    },
  };
}

export default defineConfig({
  base,
  plugins: [svelte(), demo && demoBranding()],
  build: {
    outDir: demo ? 'dist-demo' : '../internal/webui/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0, // no data: URIs for scripts/styles (strict CSP)
    sourcemap: false,
  },
  server: {
    // `npm run dev` proxies the API to a local Marineris (local listener).
    proxy: { '/api': 'http://localhost:8081', '/branding': 'http://localhost:8081' },
  },
});
