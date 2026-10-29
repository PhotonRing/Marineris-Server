import { execFileSync } from 'node:child_process';
import { expect, request } from '@playwright/test';
import { PASSWORD, totp } from '../tests/helpers.js';

export { PASSWORD, totp };

// The demo's names (deploy/demo/demo.sh).
export const LOCAL = 'http://localhost:8081';
export const DOMAIN = 'marineris.localhost';
export const UI = `https://${DOMAIN}`;
export const PORTAL = `https://auth.${DOMAIN}`;
export const HELLO = `https://hello.${DOMAIN}/`;
export const OPS = `https://ops.${DOMAIN}/`;

// The first-run setup token, from Marineris' log (as demo.sh token does).
export function setupToken() {
  const logs = execFileSync('docker', ['logs', 'marineris'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
  const all = [...(logs + '').matchAll(/"setup_token":"([A-Za-z0-9_-]+)"/g)];
  if (!all.length) throw new Error('no setup token in the logs of the marineris container');
  return all[all.length - 1][1];
}

// A code of the authenticator that has not been used yet: Authelia and
// Marineris refuse a code seen before, so a step whose code was used waits
// for the next one.
const used = new Map();
export async function freshCode(secret) {
  for (;;) {
    const step = Math.floor(Date.now() / 30000);
    if (used.get(secret) !== step) {
      used.set(secret, step);
      return totp(secret);
    }
    await new Promise((r) => setTimeout(r, 30_500 - (Date.now() % 30_000)));
  }
}

// The Owner's API client on the local listener, with the CSRF token.
export async function ownerAPI() {
  const ctx = await request.newContext({ baseURL: LOCAL, extraHTTPHeaders: { Origin: LOCAL } });
  let csrf = '';
  const call = async (method, path, data) => {
    const res = await ctx.fetch(path, { method, data, headers: csrf ? { 'X-CSRF-Token': csrf } : {} });
    const body = await res.json().catch(() => ({}));
    if (!res.ok()) throw new Error(`${method} ${path}: HTTP ${res.status()}: ${JSON.stringify(body)}`);
    if (body.csrf_token) csrf = body.csrf_token;
    return body;
  };
  return { ctx, call };
}

// Signs in on the Authelia portal page the browser is on: password, then a
// code of the authenticator.
export async function autheliaFirstFactor(page, username, password) {
  await page.getByRole('textbox', { name: 'Username' }).fill(username);
  await page.getByRole('textbox', { name: 'Password' }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
}

export async function autheliaSignIn(page, username, password, secret) {
  await autheliaFirstFactor(page, username, password);
  const otp = page.locator('#one-time-password-method input').first();
  await expect(otp).toBeVisible();
  await otp.click();
  await page.keyboard.type(await freshCode(secret));
}

// Opens url until Caddy serves it as expected: routes and certificates take
// a moment after each change, and Authelia restarts.
export async function waitServed(page, url, check, timeout = 3 * 60_000) {
  const deadline = Date.now() + timeout;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const res = await page.goto(url, { timeout: 15_000 });
      if (await check(page, res)) return;
      last = `HTTP ${res?.status()} at ${page.url()}`;
    } catch (err) {
      last = err.message;
    }
    await page.waitForTimeout(3000);
  }
  throw new Error(`${url} not served as expected: ${last}`);
}
