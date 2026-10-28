<script>
  // Settings → Branding: name, accent colour, favicon, header icon and
  // background. Changes apply at once, for every visitor (login page too).
  import { put, del, describe } from '../lib/api.js';
  import { app, toast, applyBrand } from '../lib/state.svelte.js';

  const images = [
    { id: 'favicon', label: 'Favicon', help: 'Browser tab icon. A square PNG, SVG or ICO (32 × 32 or larger).', fallback: '/branding/favicon-32.png' },
    { id: 'logo', label: 'Header icon', help: 'Shown in the sidebar and on the login page. A square image, 192 × 192 or larger.', fallback: '/branding/icon-192.png' },
    { id: 'background', label: 'Background', help: 'Behind the login page and the hub (when enabled). A landscape WebP or JPEG; 512 KB at most.', fallback: '/branding/background.webp' },
  ];
  const DEFAULT_ACCENT = '#e8743b';

  let brand = $derived(app.meta?.brand || {});
  let name = $state(app.meta?.brand?.custom_name ? app.meta.brand.name : '');
  let accent = $state(app.meta?.brand?.accent || '');
  let error = $state('');
  let busy = $state('');

  async function save(e) {
    e.preventDefault();
    busy = 'save';
    error = '';
    try {
      applyBrand(await put('/api/branding', { name, accent }));
      toast('Branding saved');
    } catch (err) {
      error = describe(err);
    } finally {
      busy = '';
    }
  }

  function upload(id, e) {
    const f = e.currentTarget.files?.[0];
    e.currentTarget.value = '';
    if (!f) return;
    if (f.size > 512 * 1024) {
      error = 'Images are limited to 512 KB.';
      return;
    }
    const reader = new FileReader();
    reader.onload = async () => {
      busy = id;
      error = '';
      try {
        applyBrand(await put(`/api/branding/${id}`, { data: String(reader.result).split(',')[1] || '' }));
        toast('Image updated');
      } catch (err) {
        error = describe(err);
      } finally {
        busy = '';
      }
    };
    reader.readAsDataURL(f);
  }

  async function reset(id) {
    busy = id;
    error = '';
    try {
      applyBrand(await del(`/api/branding/${id}`));
    } catch (err) {
      error = describe(err);
    } finally {
      busy = '';
    }
  }
</script>

<form class="card" onsubmit={save}>
  <h2>Branding</h2>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="grid">
    <div class="field">
      <label for="bn">Name</label>
      <input id="bn" bind:value={name} maxlength="40" placeholder="Marineris" />
      <div class="help">Page title and header. Empty keeps the Marineris logo.</div>
    </div>
    <div class="field">
      <label for="bc">Accent colour</label>
      <div class="row nowrap">
        <input id="bc" class="color-swatch" type="color" value={accent || DEFAULT_ACCENT} oninput={(e) => (accent = e.currentTarget.value)} aria-label="Pick the accent colour" />
        <input bind:value={accent} placeholder={DEFAULT_ACCENT} aria-label="Accent colour (hex)" />
        <button type="button" class="ghost small" onclick={() => (accent = '')} disabled={!accent}>Default</button>
      </div>
    </div>
  </div>
  <div class="brand-images">
    {#each images as im (im.id)}
      <div class="brand-image">
        <img class:wide={im.id === 'background'} src={brand[im.id] || im.fallback} alt="" />
        <div>
          <div class="label">{im.label} {#if !brand[im.id]}<span class="muted small">(default)</span>{/if}</div>
          <div class="help">{im.help}</div>
          <div class="row mt">
            <label class="btn small">
              {busy === im.id ? 'Uploading…' : 'Upload…'}
              <input class="visually-hidden" type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/svg+xml,image/x-icon" onchange={(e) => upload(im.id, e)} disabled={!!busy} />
            </label>
            {#if brand[im.id]}<button type="button" class="ghost small" onclick={() => reset(im.id)} disabled={!!busy}>Use default</button>{/if}
          </div>
        </div>
      </div>
    {/each}
  </div>
  <div class="row end">
    <button class="primary" type="submit" disabled={!!busy}>{busy === 'save' ? 'Saving…' : 'Save branding'}</button>
  </div>
</form>
