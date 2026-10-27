<script>
  import { onMount } from 'svelte';
  import { get, put, describe } from '../lib/api.js';
  import { app, toast, loadMeta } from '../lib/state.svelte.js';

  const widgetList = [
    { id: 'clock', label: 'Server time' },
    { id: 'cpu', label: 'CPU' },
    { id: 'memory', label: 'Memory' },
    { id: 'disk', label: 'Disk' },
    { id: 'network', label: 'Network' },
    { id: 'uptime', label: 'Uptime' },
  ];

  function toggleWidget(id, on) {
    s.hub_widgets = on ? [...s.hub_widgets, id] : s.hub_widgets.filter((w) => w !== id);
  }

  let s = $state(null);
  let resolvers = $state('');
  let error = $state('');
  let busy = $state(false);
  let wasStaging = $state(true);

  onMount(async () => {
    try {
      s = await get('/api/settings');
      resolvers = (s.dns_resolvers || []).join(', ');
      wasStaging = s.acme_staging;
    } catch (err) {
      error = describe(err);
    }
  });

  async function save(e) {
    e.preventDefault();
    if (wasStaging && !s.acme_staging && !confirm('Switch to Let’s Encrypt production? Make sure your routes and DNS profiles work with staging first: production has strict rate limits.')) return;
    busy = true;
    error = '';
    try {
      const body = {
        acme_email: s.acme_email,
        acme_staging: s.acme_staging,
        acme_directory: s.acme_directory,
        acme_trusted_root_file: s.acme_trusted_root_file,
        dns_resolvers: resolvers.split(',').map((x) => x.trim()).filter(Boolean),
        dns_propagation_timeout: s.dns_propagation_timeout,
        ui_host: s.ui_host,
        ui_protect_with_authelia: s.ui_protect_with_authelia,
        auth_subdomain: s.auth_subdomain,
        crowdsec_enabled: s.crowdsec_enabled,
        default_rate_limit: { events: Number(s.default_rate_limit.events) || 0, window: s.default_rate_limit.window || '' },
        login_rate_limit: { events: Number(s.login_rate_limit.events) || 0, window: s.login_rate_limit.window || '' },
        home_page: s.home_page,
        hub_title: s.hub_title,
        hub_widgets: widgetList.filter((w) => s.hub_widgets.includes(w.id)).map((w) => w.id),
        hub_background: s.hub_background,
      };
      s = await put('/api/settings', body);
      wasStaging = s.acme_staging;
      app.home = s.home_page;
      await loadMeta();
      toast('Settings saved and applied');
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }
</script>

{#if !s}
  {#if error}<div class="alert error">{error}</div>{:else}<p class="muted">Loading…</p>{/if}
{:else}
  <form class="stack" onsubmit={save}>
    {#if error}<div class="alert error">{error}</div>{/if}
    <div class="card">
      <h2>Certificates</h2>
      <div class="field">
        <label for="em">ACME e-mail</label>
        <input id="em" type="email" bind:value={s.acme_email} required />
      </div>
      <label class="check"><input type="checkbox" bind:checked={s.acme_staging} /> Use the Let’s Encrypt <strong>&nbsp;staging&nbsp;</strong> CA (untrusted certificates, generous limits — for testing)</label>
      <details class="mt">
        <summary>Advanced</summary>
        <div class="field mt">
          <label for="dir">Custom ACME directory URL</label>
          <input id="dir" bind:value={s.acme_directory} placeholder="https://… (empty = Let's Encrypt)" />
        </div>
        <div class="field">
          <label for="root">Extra trusted root for the ACME server (path inside the Caddy container)</label>
          <input id="root" bind:value={s.acme_trusted_root_file} />
        </div>
        <div class="field">
          <label for="res">DNS resolvers for propagation checks</label>
          <input id="res" bind:value={resolvers} placeholder="1.1.1.1:53, 9.9.9.9:53" />
        </div>
        <div class="field">
          <label for="pt">DNS propagation timeout</label>
          <input id="pt" bind:value={s.dns_propagation_timeout} placeholder="2m (or -1 to skip)" />
        </div>
      </details>
    </div>

    <div class="card">
      <h2>Home page</h2>
      <div class="grid">
        <div class="field">
          <label for="hp">Page shown after login</label>
          <select id="hp" bind:value={s.home_page}>
            <option value="hub">Hub — your apps and server widgets</option>
            <option value="dashboard">Dashboard — status and alerts</option>
          </select>
        </div>
        <div class="field">
          <label for="ht">Hub title</label>
          <input id="ht" bind:value={s.hub_title} maxlength="64" />
        </div>
      </div>
      <div class="field">
        <span class="label">Hub widgets</span>
        <div class="row">
          {#each widgetList as w (w.id)}
            <label class="check"><input type="checkbox" checked={s.hub_widgets.includes(w.id)} onchange={(e) => toggleWidget(w.id, e.currentTarget.checked)} /> {w.label}</label>
          {/each}
        </div>
      </div>
      <label class="check"><input type="checkbox" bind:checked={s.hub_background} /> Show the branding background behind the hub (dimmed for readability)</label>
    </div>

    <div class="card">
      <h2>Marineris UI</h2>
      <div class="field">
        <label for="ui">Public hostname</label>
        <input id="ui" bind:value={s.ui_host} placeholder="marineris.example.com (empty = SSH tunnel only)" />
      </div>
      <label class="check"><input type="checkbox" bind:checked={s.ui_protect_with_authelia} /> Require an Authelia login (2FA) in front of the public UI</label>
      <div class="field mt">
        <label for="as">Authelia portal subdomain</label>
        <input id="as" bind:value={s.auth_subdomain} />
        <div class="help">The login portal is served at https://{s.auth_subdomain}.&lt;each domain&gt;.</div>
      </div>
    </div>

    <div class="card">
      <h2>Shield</h2>
      <label class="check"><input type="checkbox" bind:checked={s.crowdsec_enabled} /> Block IPs banned by CrowdSec on every route</label>
      <div class="grid mt">
        <div class="field">
          <label for="dre">Default rate limit (requests per IP)</label>
          <input id="dre" type="number" min="0" bind:value={s.default_rate_limit.events} />
          <div class="help">0 disables the default limit.</div>
        </div>
        <div class="field">
          <label for="drw">Window</label>
          <input id="drw" bind:value={s.default_rate_limit.window} placeholder="1m" />
        </div>
        <div class="field">
          <label for="lre">Login endpoint limit</label>
          <input id="lre" type="number" min="0" bind:value={s.login_rate_limit.events} />
        </div>
        <div class="field">
          <label for="lrw">Window</label>
          <input id="lrw" bind:value={s.login_rate_limit.window} placeholder="1m" />
        </div>
      </div>
    </div>
    <div class="row end">
      <button class="primary" type="submit" disabled={busy}>{busy ? 'Applying…' : 'Save & apply'}</button>
    </div>
  </form>
{/if}
