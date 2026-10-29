// Global application state.
import { get, post, api, setCsrf } from './api.js';
import { createPasskey, getPasskey } from './webauthn.js';

export const app = $state({
  meta: null, // { name, version, listener, setup_complete, ui_host }
  session: null, // { user, csrf_token, method, ... }
  loading: true,
  toasts: [],
  stepUp: null, // pending step-up promise resolver
  home: '', // "hub" or "dashboard" (Settings → Home page)
});

let toastId = 0;
export function toast(message, kind = 'ok') {
  const id = ++toastId;
  app.toasts.push({ id, message, kind });
  setTimeout(() => {
    app.toasts = app.toasts.filter((t) => t.id !== id);
  }, kind === 'error' ? 9000 : 4500);
}

export async function loadMeta() {
  app.meta = await get('/api/meta');
  applyBrand(app.meta.brand);
}

// Branding chosen in Settings: page title, favicon, accent colour and hub
// background. The logo and name are read from app.meta.brand by the views.
export function applyBrand(brand) {
  if (!brand) return;
  app.meta = { ...app.meta, brand };
  document.title = brand.name;
  for (const [sel, def] of [['link[rel="icon"]', '/branding/favicon-32.png'], ['link[rel="apple-touch-icon"]', '/branding/icon-192.png']]) {
    const el = document.querySelector(sel);
    if (el) {
      el.href = (sel.includes('apple') ? brand.logo : brand.favicon) || def;
      el.removeAttribute('type');
    }
  }
  const root = document.documentElement.style;
  if (brand.background) root.setProperty('--hub-bg-image', `url("${brand.background}")`);
  else root.removeProperty('--hub-bg-image');
  applyTheme(document.documentElement.dataset.theme || 'dark', { remember: false });
}

export async function loadSession() {
  try {
    const s = await get('/api/auth/session');
    setCsrf(s.csrf_token);
    app.session = s;
  } catch {
    app.session = null;
  }
}

// setSession takes the answer of a sign-in and loads the full session, which
// carries the User's rights; the sign-in answer does not.
export async function setSession(s) {
  setCsrf(s.csrf_token);
  await loadSession();
}

export async function logout() {
  try {
    await post('/api/auth/logout');
  } finally {
    app.session = null;
    app.home = '';
    setCsrf('');
  }
  // Behind Authelia, its sign-in opens the UI: sign out there too, or the
  // next page would sign in again.
  const portal = app.meta?.portal_url;
  if (portal) {
    const back = `https://${app.meta.ui_host}/`;
    window.location.assign(`${portal}/logout?rd=${encodeURIComponent(back)}`);
  }
}

// Passwordless login (or step-up when already logged in).
export async function passkeyLogin() {
  const begin = await post('/api/auth/passkey/begin');
  const cred = await getPasskey(begin.options);
  const res = await api('POST', '/api/auth/passkey/finish?ceremony=' + encodeURIComponent(begin.ceremony), JSON.stringify(cred), { raw: true });
  await setSession(res);
  return res;
}

export async function registerPasskey(name) {
  const begin = await post('/api/account/passkeys/begin');
  const cred = await createPasskey(begin.options);
  await api(
    'POST',
    `/api/account/passkeys/finish?ceremony=${encodeURIComponent(begin.ceremony)}&name=${encodeURIComponent(name)}`,
    JSON.stringify(cred),
    { raw: true },
  );
}

// Theme: colours come from /branding/theme.json so they can be swapped
// without rebuilding. Applied through the CSSOM (allowed by the CSP).
export async function loadTheme() {
  let mode = 'dark';
  try {
    mode = localStorage.getItem('marineris-theme') || '';
  } catch {
    mode = '';
  }
  try {
    const res = await fetch('/branding/theme.json');
    const theme = await res.json();
    app.theme = theme;
    applyTheme(mode || theme.default_mode || 'dark');
  } catch {
    applyTheme(mode || 'dark');
  }
}

export function applyTheme(mode, { remember = true } = {}) {
  const root = document.documentElement;
  root.dataset.theme = mode;
  const vars = { ...app.theme?.[mode] };
  // A custom accent replaces the theme's; the secondary accent (links) is a
  // lighter or darker shade of it so it stays readable on the background.
  const accent = app.meta?.brand?.accent;
  if (accent) {
    vars.accent = accent;
    vars['accent-2'] = `color-mix(in srgb, ${accent} ${mode === 'light' ? '80%, black' : '70%, white'})`;
  }
  for (const k of ['accent', 'accent-2']) if (!vars[k]) root.style.removeProperty('--' + k);
  for (const [k, v] of Object.entries(vars)) root.style.setProperty('--' + k, v);
  if (!remember) return;
  try {
    localStorage.setItem('marineris-theme', mode);
  } catch {
    /* private mode */
  }
}
