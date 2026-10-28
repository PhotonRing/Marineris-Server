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
  for (const name of ['Dashboard', 'Containers', 'Stacks', 'Volumes & networks', 'Routes', 'Domains & certificates', 'DNS profiles', 'Proxy & SSO', 'Audit log', 'Settings', 'Account']) {
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
  await page.getByRole('link', { name: 'Proxy & SSO', exact: true }).click();
  await expect(page.getByRole('cell', { name: 'whoami.mars.example' })).toBeVisible();
  await page.getByRole('button', { name: 'History', exact: true }).click();
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

test('the hub fills the window without scrolling under the demo banner', async ({ page }) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto('./');
  await expect(page.getByText('Jellyfin')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(1080);
  // The background layer covers the whole width, not only the main column.
  const bg = await page.evaluate(() => {
    const s = getComputedStyle(document.querySelector('.shell'), '::before');
    return { image: s.backgroundImage, left: s.left, right: s.right };
  });
  expect(bg.image).toContain('background.webp');
  expect([bg.left, bg.right]).toEqual(['0px', '0px']);
});

test('a stack: services with status and icons, editing a service, actions', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/stacks');
  await page.getByRole('link', { name: /media/ }).click();
  const services = page.getByRole('navigation', { name: 'Services' });
  await expect(services.getByRole('button', { name: /jellyfin/ })).toBeVisible();
  await expect(services.getByRole('button', { name: /jellyseerr/ })).toBeVisible();
  // Edit an environment variable and recreate the service.
  await page.getByRole('tab', { name: 'Environment' }).click();
  await page.getByLabel('Value 2', { exact: true }).fill('https://films.mars.example');
  await page.getByRole('button', { name: 'Save & recreate' }).click();
  await expect(page.getByText('jellyfin saved and recreated')).toBeVisible();
  await services.getByRole('button', { name: /compose\.yaml/ }).click();
  await expect(page.getByLabel('compose.yaml')).toHaveValue(/JELLYFIN_PublishedServerUrl: "https:\/\/films\.mars\.example"/);
  await expect(page.getByLabel('compose.yaml')).toHaveValue(/jellyseerr:\n {4}image: fallenbagel/);
  // Stopping one service leaves the stack partly running.
  await services.getByRole('button', { name: /jellyseerr/ }).click();
  await page.getByRole('button', { name: 'Stop', exact: true }).last().click();
  await expect(page.getByText('partly running')).toBeVisible();
  await page.getByRole('tab', { name: 'Terminal' }).click();
  await expect(page.getByText('Coming in Phase 3.')).toBeVisible();
  // The policy applies to edits too.
  await page.getByRole('tab', { name: 'Mounts' }).click();
  await page.getByRole('button', { name: '+ Add mount' }).click();
  await page.getByLabel('Mount 2 type').selectOption('bind');
  await page.getByLabel('Mount 2 host folder').fill('/var/run/docker.sock');
  await page.getByLabel('Mount 2 path in the container').fill('/var/run/docker.sock');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText(/security policy/)).toBeVisible();
  expect(problems).toEqual([]);
});

test('branding from the settings', async ({ page }) => {
  await page.goto('./#/settings');
  await page.getByLabel('Name', { exact: true }).fill('Olympus');
  await page.getByLabel('Accent colour (hex)').fill('#3b82f6');
  await page.getByRole('button', { name: 'Save branding' }).click();
  await expect(page.locator('.brand-name')).toHaveText('Olympus');
  await expect(page).toHaveTitle('Olympus');
  await page.getByLabel('Accent colour (hex)').fill('blue');
  await page.getByRole('button', { name: 'Save branding' }).click();
  await expect(page.getByText(/must be a colour/)).toBeVisible();
});

test('proxy & SSO overview: sites and Authelia rules in plain terms', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/proxy');
  await expect(page.getByRole('cell', { name: 'nas.mars.example' })).toBeVisible();
  await expect(page.getByText('wildcard *.mars.example (DNS-01)').first()).toBeVisible();
  await page.getByRole('button', { name: /Authelia/ }).click();
  // The admin-only NAS: admins get in with two factors, everyone else is denied.
  await expect(page.getByRole('row', { name: /nas\.mars\.example.*group admins.*two-factor/ })).toBeVisible();
  await expect(page.getByRole('row', { name: /nas\.mars\.example.*anyone.*deny/ })).toBeVisible();
  await page.getByRole('button', { name: 'Generated file' }).click();
  await expect(page.getByText(/Raw edits that survive regeneration come in Phase 2/)).toBeVisible();
  expect(problems).toEqual([]);
});

test('a service: labels with suggestions, typed mounts', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/stacks/media');
  const services = page.getByRole('navigation', { name: 'Services' });
  await services.getByRole('button', { name: /jellyseerr/ }).click();
  // Make jellyseerr the main service through its labels.
  await page.getByRole('tab', { name: 'Labels' }).click();
  await page.getByRole('button', { name: '+ Add label' }).click();
  await page.getByLabel('Name 1').fill('io.marineris.main');
  await expect(page.locator('.key-help')).toHaveText('Main service: its icon represents the stack.');
  await page.getByLabel('Value 1').selectOption('true');
  // Mounts: a new read-only volume and a host folder.
  await page.getByRole('tab', { name: 'Mounts' }).click();
  await page.getByRole('button', { name: '+ Add mount' }).click();
  await page.getByLabel('Mount 2 volume').selectOption('new');
  await page.getByLabel('Mount 2 new volume name').fill('posters');
  await page.getByLabel('Mount 2 path in the container').fill('/posters');
  await page.getByLabel('Mount 2 read-only').check();
  await page.getByRole('button', { name: '+ Add mount' }).click();
  await page.getByLabel('Mount 3 type').selectOption('bind');
  await page.getByLabel('Mount 3 host folder').fill('/srv/media/requests');
  await page.getByLabel('Mount 3 path in the container').fill('/imports');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText(/jellyseerr saved/)).toBeVisible();
  await services.getByRole('button', { name: /compose\.yaml/ }).click();
  const compose = page.getByLabel('compose.yaml');
  await expect(compose).toHaveValue(/- posters:\/posters:ro/);
  await expect(compose).toHaveValue(/- \/srv\/media\/requests:\/imports/);
  await expect(compose).toHaveValue(/\n {2}posters: \{\}/);
  await expect(compose).toHaveValue(/jellyseerr:[\s\S]*io\.marineris\.main: "true"/);
  // A container path must be absolute.
  await services.getByRole('button', { name: /jellyfin/ }).click();
  await page.getByRole('tab', { name: 'Mounts' }).click();
  await page.getByLabel('Mount 1 path in the container').fill('config');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText(/must be absolute/)).toBeVisible();
  expect(problems).toEqual([]);
});

test('stack buttons fit the state and explain themselves; rename', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/stacks/media');
  const header = page.locator('.card').first();
  // A running stack offers "Apply changes", not "Deploy", and every button has a tooltip.
  await expect(header.getByRole('button', { name: 'Apply changes' })).toBeVisible();
  await expect(header.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
  for (const b of await header.getByRole('button').all()) expect(await b.getAttribute('title')).toBeTruthy();
  for (const b of await page.locator('.service-panel .spread').first().getByRole('button').all()) expect(await b.getAttribute('title')).toBeTruthy();
  // Saving a service without recreating it leaves changes to apply.
  await page.getByRole('tab', { name: 'Environment' }).click();
  await page.getByLabel('Value 2', { exact: true }).fill('https://films.mars.example');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('changes not applied')).toBeVisible();
  await header.getByRole('button', { name: 'Apply changes' }).click();
  await expect(page.getByText('changes not applied')).toHaveCount(0);
  // Rename.
  await header.getByRole('button', { name: 'Rename' }).click();
  await page.getByLabel('New name').fill('films');
  await page.getByRole('button', { name: 'Rename', exact: true }).last().click();
  await expect(page.getByText('Stack renamed to films')).toBeVisible();
  expect(page.url()).toContain('#/stacks/films');
  await page.getByRole('link', { name: 'All stacks' }).click();
  await expect(page.getByRole('link', { name: /films/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /^media/ })).toHaveCount(0);
  expect(problems).toEqual([]);
});

test('a route reuses the icon of a stack service', async ({ page }) => {
  const problems = watch(page);
  await page.goto('./#/routes');
  await page.getByRole('button', { name: 'New route' }).click();
  await page.getByLabel('Container', { exact: true }).selectOption('jellyseerr');
  await expect(page.getByLabel('Icon', { exact: true })).toHaveValue('stack');
  await expect(page.getByLabel('Stack or service whose icon to use')).toHaveValue('media/jellyseerr');
  await page.getByLabel('Subdomain').fill('requests');
  await page.getByRole('button', { name: 'Save & apply' }).click();
  await page.getByRole('link', { name: 'Hub', exact: true }).click();
  await expect(page.getByRole('link', { name: /^jellyseerr/ }).locator('img')).toHaveAttribute('src', /^data:image\/svg/);
  expect(problems).toEqual([]);
});
