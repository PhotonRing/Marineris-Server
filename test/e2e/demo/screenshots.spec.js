// Regenerates the README's pictures from the demo. Runs only on request,
// since it writes into docs/images: npm run screenshots (in test/e2e, after
// npm run build:demo in web/).
import fs from 'node:fs';
import { test, expect } from '@playwright/test';

test.skip(!process.env.UPDATE_SCREENSHOTS, 'set UPDATE_SCREENSHOTS=1 to regenerate the README pictures');

// Same size and framing as the picture the README shows at width 900.
test.use({ viewport: { width: 1440, height: 860 } });

// Playwright writes PNG or JPEG only; Chromium's canvas encodes WebP.
async function toWebP(page, png) {
  const blank = await page.context().newPage();
  const dataURL = await blank.evaluate(async (b64) => {
    const img = new Image();
    img.src = `data:image/png;base64,${b64}`;
    await img.decode();
    const canvas = document.createElement('canvas');
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    canvas.getContext('2d').drawImage(img, 0, 0);
    return canvas.toDataURL('image/webp', 0.8);
  }, png.toString('base64'));
  await blank.close();
  expect(dataURL.startsWith('data:image/webp')).toBe(true);
  return Buffer.from(dataURL.split(',')[1], 'base64');
}

test('screenshot: the hub', async ({ page }) => {
  await page.goto('./');
  await expect(page.getByRole('heading', { level: 2, name: 'Marineris' })).toBeVisible();
  // Tiles and widgets settle (icons and the Mars background load).
  await page.waitForLoadState('networkidle');
  // The README shows the product, not the demo's notice.
  await page.evaluate(() => {
    document.querySelector('.demo-banner')?.remove();
    document.documentElement.style.setProperty('--chrome-top', '0px');
  });
  const png = await page.screenshot();
  fs.writeFileSync(new URL('../../../docs/images/hub.webp', import.meta.url), await toWebP(page, png));
});
