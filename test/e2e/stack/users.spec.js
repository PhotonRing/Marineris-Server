// Users and Groups against the real stack (playwright.stack.config.js): the
// Owner invites someone, who accepts on the portal's host and signs in
// through Authelia; Disable, Reset links and a passkey Invite.
import { test, expect } from '@playwright/test';
import { watchConsole } from '../tests/helpers.js';
import { DOMAIN, HELLO, OPS, PASSWORD, PORTAL, UI, autheliaFirstFactor, autheliaSignIn, freshCode, ownerAPI, setupToken, waitServed } from './helpers.js';

test.describe.configure({ mode: 'serial' });

// A password must not contain the Username.
const ALICE_PASSWORD = 'Olympus-Mons-E2E-2026!';
const ALICE_NEW_PASSWORD = 'Tharsis-Ridge-E2E-2026!';
const BOB_PASSWORD = 'Hellas-Basin-E2E-2026!';
const leftLinkPage = { timeout: 60_000 };
const greets = (name) => `Signed in as: ${name}`;

let ownerCookies = [];
let ownerSecret = '';
let aliceSecret = '';
let alice; // alice's browser context, signed in through Authelia

// Arrange through the API (not under test): first-run setup, the demo's
// domain, the UI behind Authelia, a route for everyone signed in and one for
// Administrators only.
test.beforeAll(async ({ browser }) => {
  test.setTimeout(10 * 60_000);
  const { ctx, call } = await ownerAPI();
  const token = setupToken();
  const begun = await call('POST', '/api/setup/begin', { setup_token: token, username: 'admin' });
  ownerSecret = begun.totp_secret;
  await call('POST', '/api/setup/finish', {
    setup_token: token, username: 'admin', password: PASSWORD,
    totp_code: await freshCode(ownerSecret), acme_email: 'e2e@example.com',
  });
  await call('POST', '/api/domains', { name: DOMAIN });
  const settings = await call('GET', '/api/settings');
  delete settings.setup_complete;
  await call('PUT', '/api/settings', { ...settings, ui_host: DOMAIN, ui_protect_with_authelia: true });
  for (const [name, auth] of [['hello', 'authelia'], ['ops', 'admins']]) {
    await call('POST', '/api/routes', {
      name, host: `${name}.${DOMAIN}`, kind: 'container',
      target: { container: 'demo-hello', port: 80 }, auth, enabled: true,
    });
  }
  ownerCookies = (await ctx.storageState()).cookies;
  await ctx.dispose();

  // Caddy serves the routes and Authelia answers before the tests start.
  const page = await (await browser.newContext()).newPage();
  await waitServed(page, HELLO, async (p) =>
    p.url().startsWith(PORTAL) && p.getByRole('textbox', { name: 'Username' }).waitFor({ timeout: 10_000 }).then(() => true, () => false));
  await page.context().close();
});

// The Owner's browser on the local listener.
async function ownerPage(browser) {
  const context = await browser.newContext();
  await context.addCookies(ownerCookies);
  const page = await context.newPage();
  // Disable and other confirmations: yes.
  page.on('dialog', (d) => d.accept());
  return page;
}

// Answers the step-up dialog when the UI asks for it.
async function confirmIfAsked(page) {
  const dialog = page.getByRole('dialog', { name: "Confirm it's you" });
  if (await dialog.isVisible().catch(() => false)) {
    await dialog.getByLabel('Password').fill(PASSWORD);
    await dialog.getByLabel('Authenticator code').fill(await freshCode(ownerSecret));
    await dialog.getByRole('button', { name: 'Confirm' }).click();
  }
}

// The Owner creates an Invite from the Users page; returns its link.
async function invite(browser, username) {
  const page = await ownerPage(browser);
  await page.goto('/users');
  await page.getByRole('button', { name: 'Invite someone' }).click();
  await page.getByLabel('Username').fill(username);
  await page.getByRole('button', { name: 'Create the Invite' }).click();
  await page.waitForTimeout(500);
  await confirmIfAsked(page);
  const link = page.getByRole('textbox', { name: 'Link' });
  await expect(link).toHaveValue(new RegExp(`^${PORTAL}/marineris-link/#`));
  await expect(page.getByRole('img', { name: 'QR code of the link' })).toBeVisible();
  const url = await link.inputValue();
  await page.context().close();
  return url;
}

// Opens a User's page as the Owner.
async function userPage(browser, username) {
  const page = await ownerPage(browser);
  await page.goto('/users');
  await page.getByRole('link', { name: username, exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Reset link' })).toBeVisible();
  return page;
}

// Opens url in alice's browser until check passes (Authelia re-reads its
// files after every change).
async function eventually(page, url, check) {
  await expect(async () => {
    await page.goto(url);
    await check();
  }).toPass({ timeout: 2 * 60_000, intervals: [2000, 5000] });
}

test('an invited person accepts on the portal, signs in through Authelia and sees only their apps', async ({ browser }) => {
  const url = await invite(browser, 'alice');

  alice = await browser.newContext();
  const page = await alice.newPage();
  const problems = watchConsole(page);
  await page.goto(url);
  await expect(page.getByRole('heading', { name: /^Welcome to .*, alice$/ })).toBeVisible();
  await expect(page.getByLabel('Username')).toHaveValue('alice');
  await page.getByLabel('Choose a password').fill(ALICE_PASSWORD);
  await page.getByLabel('Type it again').fill(ALICE_PASSWORD);
  await expect(page.getByAltText('Authenticator QR code')).toBeVisible();
  aliceSecret = (await page.locator('#secret').textContent()).trim();
  await page.getByLabel('Code').fill(await freshCode(aliceSecret));
  await page.getByRole('button', { name: /^Join / }).click();

  // On to Authelia's sign-in, which then opens the UI.
  await page.waitForURL((u) => u.href.startsWith(PORTAL) && !u.pathname.startsWith('/marineris-link'), leftLinkPage);
  await autheliaSignIn(page, 'alice', ALICE_PASSWORD, aliceSecret);
  await page.waitForURL((u) => u.hostname === DOMAIN, leftLinkPage);

  // Only the Hub and Account; only the apps she may open; no server widgets.
  await expect(page.getByRole('link', { name: 'Hub', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Account', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Dashboard', exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Users', exact: true })).toHaveCount(0);
  await expect(page.locator('a.tile', { hasText: /hello/i })).toBeVisible();
  await expect(page.locator('a.tile', { hasText: /ops/i })).toHaveCount(0);
  await expect(page.getByText('Memory', { exact: true })).toHaveCount(0);

  // An Administrators-only page is refused, and so is its API.
  await page.goto(`${UI}/routes`);
  await expect(page.getByRole('heading', { name: 'You are not allowed to open this page' })).toBeVisible();
  expect(await page.evaluate(async () => (await fetch('/api/routes')).status)).toBe(403);
  expect(problems).toEqual([]);

  // The same sign-in opens her app, not the Administrators' one.
  await page.goto(HELLO);
  await expect(page.locator('body')).toContainText(greets('alice'));
  await page.goto(OPS);
  await expect(page.locator('body')).not.toContainText(greets('alice'));
});

test("a disabled User's open session stops working", async ({ browser }) => {
  const page = alice.pages()[0];
  const owner = await userPage(browser, 'alice');
  await owner.getByRole('button', { name: 'Disable' }).click();
  await expect(owner.locator('.toast', { hasText: 'alice disabled' })).toBeVisible();

  // Authelia checks the User on every request (refresh_interval: always).
  await eventually(page, HELLO, async () => {
    await expect(page.locator('body')).not.toContainText(greets('alice'), { timeout: 2000 });
  });

  await owner.getByRole('button', { name: 'Enable' }).click();
  await expect(owner.locator('.toast', { hasText: 'alice enabled' })).toBeVisible();
  await owner.context().close();
});

test('a Reset link sets a new password and authenticator', async ({ browser }) => {
  const owner = await userPage(browser, 'alice');
  await owner.getByRole('button', { name: 'Create a Reset link…' }).click();
  await owner.getByLabel('Password and second factor').check();
  await owner.getByRole('button', { name: 'Create the Reset link' }).click();
  await owner.waitForTimeout(500);
  await confirmIfAsked(owner);
  const link = owner.getByRole('textbox', { name: 'Link' });
  await expect(link).toHaveValue(new RegExp(`^${PORTAL}/marineris-link/#`));
  const url = await link.inputValue();
  await owner.context().close();

  // While the link is pending, Authelia does not let her in with her old
  // credentials. It learns of the block when it reloads its users file, a
  // moment after the link is created: retry, each time in a fresh browser,
  // until it refuses.
  let page;
  await expect(async () => {
    await alice.close();
    alice = await browser.newContext();
    page = await alice.newPage();
    await page.goto(HELLO);
    await autheliaFirstFactor(page, 'alice', ALICE_PASSWORD);
    await expect(page.getByText(/incorrect username or password/i)).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await expect(page.locator('body')).not.toContainText(greets('alice'));

  await page.goto(url);
  await expect(page.getByRole('heading', { name: 'Hello alice' })).toBeVisible();
  await page.getByLabel('Choose a password').fill(ALICE_NEW_PASSWORD);
  await page.getByLabel('Type it again').fill(ALICE_NEW_PASSWORD);
  aliceSecret = (await page.locator('#secret').textContent()).trim();
  await page.getByLabel('Code').fill(await freshCode(aliceSecret));
  await page.getByRole('button', { name: 'Save' }).click();
  await page.waitForURL((u) => u.href.startsWith(PORTAL) && !u.pathname.startsWith('/marineris-link'), leftLinkPage);

  await page.goto(HELLO);
  await autheliaSignIn(page, 'alice', ALICE_NEW_PASSWORD, aliceSecret);
  await expect(page.locator('body')).toContainText(greets('alice'));
});

test('an Invite accepted with a passkey signs in with it alone', async ({ browser }) => {
  const url = await invite(browser, 'bob');
  const context = await browser.newContext();
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });

  await page.goto(url);
  await expect(page.getByRole('heading', { name: /^Welcome to .*, bob$/ })).toBeVisible();
  await page.getByLabel('Choose a password').fill(BOB_PASSWORD);
  await page.getByLabel('Type it again').fill(BOB_PASSWORD);
  await page.getByLabel('Passkey', { exact: true }).check();
  await page.getByLabel('Name (optional)').fill('Virtual key');
  await page.getByRole('button', { name: /^Join / }).click();
  await page.waitForURL((u) => u.href.startsWith(PORTAL) && !u.pathname.startsWith('/marineris-link'), leftLinkPage);

  // The passkey alone signs in, on the portal, to the UI and the apps.
  await page.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await page.waitForURL((u) => u.hostname === DOMAIN, leftLinkPage);
  await expect(page.locator('a.tile', { hasText: /hello/i })).toBeVisible();
  await page.goto(HELLO);
  await expect(page.locator('body')).toContainText(greets('bob'));
  await context.close();
});

test.afterAll(async () => {
  await alice?.close();
});
