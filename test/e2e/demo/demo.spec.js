import { test, expect } from '@playwright/test';

// Fails on any page error, failed request to the site itself, or CSP report.
function watch(page) {
  const problems = [];
  page.on('pageerror', (e) => problems.push('pageerror: ' + e.message));
  // Failed loads are checked below for the site itself (app icons come from a CDN).
  page.on('console', (m) => m.type() === 'error' && !/Failed to load resource/.test(m.text()) && problems.push('console: ' + m.text()));
  page.on('requestfailed', (r) => r.url().includes('/Marineris-Server/') && problems.push('failed: ' + r.url()));
  page.on('response', (r) => r.url().includes('/Marineris-Server/') && r.status() >= 400 && problems.push(`${r.status()}: ${r.url()}`));
  return problems;
}

test('every page of the demo renders, signed in, with sample data', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./');
  await expect(page.getByRole('note')).toContainText('Demo');
  await expect(page.getByRole('heading', { level: 2, name: 'Marineris' })).toBeVisible();
  await expect(page.getByText('Jellyfin')).toBeVisible();
  for (const name of ['Dashboard', 'Containers', 'Stacks', 'Volumes & networks', 'Routes', 'Domains & certificates', 'DNS profiles', 'Configuration', 'Audit log', 'Settings', 'Account']) {
    await page.getByRole('link', { name, exact: true }).click();
    await expect(page.getByRole('heading', { level: 1, name })).toBeVisible();
    expect(page.url()).toContain('/Marineris-Server/#/');
  }
  // A deep link works after a reload (the path lives in the fragment).
  await page.reload();
  await expect(page.getByRole('heading', { level: 1, name: 'Account' })).toBeVisible();
  expect(problems).toEqual([]);
});

test('a route can be published and shows on the hub', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/routes');
  await page.getByRole('button', { name: 'New route' }).click();
  await page.getByLabel('Container', { exact: true }).selectOption('whoami');
  await page.getByLabel('Subdomain').fill('whoami');
  await page.getByLabel('Name', { exact: true }).fill('whoami');
  await page.getByRole('button', { name: 'Save & apply' }).click();
  await expect(page.getByRole('cell', { name: /whoami\.mars\.example/ })).toBeVisible();
  // A second route with the same name is refused, like on a real server.
  await page.getByRole('button', { name: 'New route' }).click();
  await page.getByLabel('Container', { exact: true }).selectOption('jellyfin');
  await page.getByLabel('Subdomain').fill('films');
  await page.getByLabel('Name', { exact: true }).fill('whoami');
  await page.getByRole('button', { name: 'Save & apply' }).click();
  await expect(page.getByText(/already exists/)).toBeVisible();
  await page.getByRole('button', { name: 'Cancel' }).click();
  // The new app is on the hub, and the change is in the configuration history.
  await page.getByRole('link', { name: 'Hub', exact: true }).click();
  await expect(page.getByRole('link', { name: /^whoami/ })).toHaveAttribute('href', 'https://whoami.mars.example/');
  await page.getByRole('link', { name: 'Configuration', exact: true }).click();
  await expect(page.getByText('route whoami added').first()).toBeVisible();
  expect(problems).toEqual([]);
});

test('sign out and sign in again (any password, any code)', async ({ page }) => {
  await page.goto('./');
  await page.getByRole('button', { name: 'Log out' }).click();
  // Passkeys need a real server: the demo says so.
  await page.getByRole('button', { name: /Sign in with a passkey/ }).click();
  await expect(page.getByText(/not available in the demo/)).toBeVisible();
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Password').fill('anything-goes-here');
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByLabel('Authenticator code').fill('123456');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { level: 2, name: 'Marineris' })).toBeVisible();
});
