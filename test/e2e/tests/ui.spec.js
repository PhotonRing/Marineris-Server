import { test, expect } from '@playwright/test';
import { PASSWORD, setupToken, totp, watchConsole } from './helpers.js';

test.describe.configure({ mode: 'serial' });

let secret = '';

test('public listener refuses everything before setup', async ({ page }) => {
  await page.goto('http://127.0.0.1:18180/');
  await expect(page.getByRole('heading', { name: 'Setup pending' })).toBeVisible();
  const res = await page.request.get('http://127.0.0.1:18180/api/overview');
  expect(res.status()).toBe(503);
});

test('setup wizard', async ({ page }) => {
  const problems = watchConsole(page);
  await page.goto('/');
  await page.getByLabel('Setup token').fill('wrong-token');
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('alert')).toContainText('invalid setup token');

  await page.getByLabel('Setup token').fill(setupToken());
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByAltText('TOTP QR code')).toBeVisible();
  await page.locator('details summary').click();
  secret = (await page.locator('details code').textContent()).trim();

  await page.getByLabel('Password', { exact: true }).fill('aaaaaaaaaaaaaa');
  await page.getByLabel('Repeat password').fill('aaaaaaaaaaaaaa');
  await page.getByLabel("E-mail for Let's Encrypt").fill('ops@example.com');
  await page.getByLabel('Code from the authenticator').fill(totp(secret));
  await page.getByRole('button', { name: 'Create administrator' }).click();
  await expect(page.getByRole('alert')).toContainText(/password/i);

  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByLabel('Repeat password').fill(PASSWORD);
  await page.getByRole('button', { name: 'Create administrator' }).click();
  await expect(page.getByText('The administrator account is ready')).toBeVisible();
  const codes = (await page.locator('pre.codes').textContent()).trim().split('\n');
  expect(codes).toHaveLength(10);
  await expect(page.getByRole('button', { name: 'Open Marineris' })).toBeDisabled();
  await page.getByLabel('I have stored my recovery codes').check();
  await page.getByRole('button', { name: 'Open Marineris' }).click();
  // The hub is the default home page.
  await expect(page.getByRole('heading', { level: 2, name: 'Marineris' })).toBeVisible();
  await expect(page.getByText('No apps yet')).toBeVisible();
  await page.getByRole('link', { name: 'Dashboard', exact: true }).click();
  await expect(page.getByText(/staging CA/)).toBeVisible();
  expect(problems).toEqual([]);
});

test('every page renders without errors or CSP violations', async ({ page }) => {
  const problems = watchConsole(page);
  await login(page, 1);
  for (const name of ['Dashboard', 'Containers', 'Stacks', 'Volumes & networks', 'Routes', 'Domains & certificates', 'DNS profiles', 'Proxy & SSO', 'Audit log', 'Settings', 'Account']) {
    await page.getByRole('link', { name, exact: true }).click();
    await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
  }
  // Hub with its widgets (they poll the metrics endpoint).
  await page.getByRole('link', { name: 'Hub', exact: true }).click();
  await expect(page.getByText('Memory', { exact: true })).toBeVisible();
  expect(problems).toEqual([]);
});

test('wrong password and wrong code are refused', async ({ page }) => {
  await page.goto('/');
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill('not-the-password');
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByRole('alert')).toContainText('invalid username or password');
  await page.getByLabel('Password').fill(PASSWORD);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByLabel('Authenticator code').fill('000000');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('invalid code');
});

test('passkey registration and passwordless login', async ({ page, context }) => {
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  await login(page, 1);
  await page.getByRole('link', { name: 'Account', exact: true }).click();
  await page.getByPlaceholder(/Name, e.g./).fill('Virtual key');
  await page.getByRole('button', { name: 'Add passkey' }).click();
  await expect(page.getByRole('cell', { name: 'Virtual key' })).toBeVisible();

  await page.getByRole('button', { name: 'Log out', exact: true }).click();
  await page.getByRole('button', { name: /Sign in with a passkey/ }).click();
  await expect(page.getByRole('link', { name: 'Hub', exact: true })).toBeVisible();
});

test('branding: name, accent colour and header icon from the settings', async ({ page }) => {
  const problems = watchConsole(page);
  await login(page, 1);
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.getByLabel('Name', { exact: true }).fill('Olympus');
  await page.getByLabel('Accent colour (hex)').fill('#3b82f6');
  await page.getByRole('button', { name: 'Save branding' }).click();
  await expect(page.locator('.brand-name')).toHaveText('Olympus');
  await expect(page).toHaveTitle('Olympus');
  expect(await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--accent').trim())).toBe('#3b82f6');
  // A 1×1 PNG as the header icon: served by Marineris, shown in the sidebar.
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');
  await page.locator('.brand-image').filter({ hasText: 'Header icon' }).locator('input[type=file]').setInputFiles({ name: 'icon.png', mimeType: 'image/png', buffer: png });
  await expect(page.locator('.sidebar .brand img')).toHaveAttribute('src', /^\/api\/branding\/logo\?v=/);
  // The login page uses it too.
  await page.getByRole('button', { name: 'Log out', exact: true }).click();
  await expect(page.locator('.auth-brand')).toContainText('Olympus');
  // Back to the defaults for the other tests.
  await login(page, 2);
  await page.getByRole('link', { name: 'Settings', exact: true }).click();
  await page.locator('.brand-image').filter({ hasText: 'Header icon' }).getByRole('button', { name: 'Use default' }).click();
  await page.getByLabel('Name', { exact: true }).fill('');
  await page.getByRole('button', { name: 'Default', exact: true }).click();
  await page.getByRole('button', { name: 'Save branding' }).click();
  await expect(page.locator('.sidebar .wordmark')).toBeVisible();
  expect(problems).toEqual([]);
});

test('session cookie is hardened', async ({ page, context }) => {
  await login(page, 1);
  const cookie = (await context.cookies()).find((c) => c.name === '__Host-marineris_session');
  expect(cookie).toBeTruthy();
  expect(cookie.httpOnly).toBe(true);
  expect(cookie.secure).toBe(true);
  expect(cookie.sameSite).toBe('Strict');
});

async function login(page, offset) {
  await page.goto('/');
  if (await page.getByRole('link', { name: 'Hub', exact: true }).isVisible().catch(() => false)) return;
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill(PASSWORD);
  await page.getByRole('button', { name: 'Continue' }).click();
  // Each code works once: use the next window, then wait for a new one.
  await page.getByLabel('Authenticator code').fill(totp(secret, offset));
  await page.getByRole('button', { name: 'Sign in' }).click();
  const ok = await page.getByRole('link', { name: 'Hub', exact: true }).waitFor({ timeout: 3000 }).then(() => true, () => false);
  if (!ok) {
    // The code of this window was already used: wait for the next one.
    await page.waitForTimeout(31_000 - (Date.now() % 30_000));
    await page.getByLabel('Password').isVisible().then(async (v) => {
      if (v) {
        await page.getByLabel('Password').fill(PASSWORD);
        await page.getByRole('button', { name: 'Continue' }).click();
      }
    });
    await page.getByLabel('Authenticator code').fill(totp(secret, 1));
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('link', { name: 'Hub', exact: true })).toBeVisible();
  }
}
