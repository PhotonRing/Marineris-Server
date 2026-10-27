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

export function setSession(s) {
  setCsrf(s.csrf_token);
  app.session = s;
}

export async function logout() {
  try {
    await post('/api/auth/logout');
  } finally {
    app.session = null;
    app.home = '';
    setCsrf('');
  }
}

// Passwordless login (or step-up when already logged in).
export async function passkeyLogin() {
  const begin = await post('/api/auth/passkey/begin');
  const cred = await getPasskey(begin.options);
  const res = await api('POST', '/api/auth/passkey/finish?ceremony=' + encodeURIComponent(begin.ceremony), JSON.stringify(cred), { raw: true });
  setSession(res);
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

export function applyTheme(mode) {
  const root = document.documentElement;
  root.dataset.theme = mode;
  const vars = app.theme?.[mode];
  if (vars) {
    for (const [k, v] of Object.entries(vars)) root.style.setProperty('--' + k, v);
  }
  try {
    localStorage.setItem('marineris-theme', mode);
  } catch {
    /* private mode */
  }
}
