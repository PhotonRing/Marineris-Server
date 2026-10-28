<script>
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import { link } from '../lib/router.js';
  import Modal from '../components/Modal.svelte';

  let routes = $state([]);
  let domains = $state([]);
  let containers = $state([]);
  let error = $state('');
  let open = $state(false);
  let busy = $state(false);
  let formError = $state('');
  let form = $state(blank());

  function blank() {
    return {
      id: 0,
      name: '',
      sub: '',
      domain: '',
      path_prefix: '',
      kind: 'container',
      container: '',
      port: '',
      upstream_tls: false,
      url: '',
      tls_insecure_skip_verify: false,
      folder: '',
      auth: 'authelia',
      enabled: true,
      connect_network: true,
      custom_rate: false,
      rate_events: 100,
      rate_window: '1m',
      hub_visible: true,
      hub_title: '',
      hub_subtitle: '',
      has_icon: false,
      icon: '', // resolved icon URL (own, linked or automatic)
      icon_link: '', // "stack" or "stack/service" the icon follows
      icon_mode: 'keep', // keep | auto | stack | name | url | upload
      icon_stack: '', // "stack" or "stack/service" (icon_mode stack)
      icon_name: '',
      icon_url: '',
      icon_data: '',
    };
  }

  async function load() {
    // Containers and stack icons are optional (Docker may be unreachable).
    const optional = Promise.all([get('/api/containers').catch(() => []), get('/api/stack-icons').catch(() => [])]);
    try {
      [routes, domains] = await Promise.all([get('/api/routes'), get('/api/domains')]);
    } catch (err) {
      error = describe(err);
    }
    [containers, stackIcons] = await optional;
    loaded = true;
  }
  onMount(load);

  let selected = $derived(containers.find((c) => c.name === form.container));
  // Icons of stacks and their services that a route can reuse.
  let stackIcons = $state([]);
  let loaded = $state(false);
  const iconKey = (i) => (i.service ? `${i.stack}/${i.service}` : i.stack);
  let stackIconPreview = $derived(stackIcons.find((i) => iconKey(i) === form.icon_stack)?.icon || '');
  // The service of the chosen container, when it belongs to a stack.
  let service = $derived(form.kind === 'container' && selected?.stack ? `${selected.stack} › ${selected.service}` : '');
  let serviceIcon = $derived(selected?.stack ? stackIcons.find((i) => i.stack === selected.stack && i.service === selected.service)?.icon || '' : '');
  const linkText = (l) => (l.includes('/') ? l.replace('/', ' › ') : `${l} (stack)`);
  let iconSource = $derived(
    form.has_icon ? 'its own icon' : form.icon_link ? `the icon of ${linkText(form.icon_link)}` : service ? `the icon of its service, ${service}` : '',
  );

  function pickContainer(e) {
    form.container = e.currentTarget.value;
    const c = containers.find((x) => x.name === form.container);
    if (c && c.ports.length) {
      // Prefer common HTTP ports when a container exposes several.
      const preferred = [80, 8080, 3000, 8000, 5000, 8096, 9000];
      form.port = c.ports.find((p) => preferred.includes(p)) || c.ports[0];
    }
    if (!form.name) form.name = form.container;
    // A stack's container shows its service's icon automatically; other
    // containers get a dashboard-icons suggestion.
    if (c && !c.stack && !form.has_icon && form.icon_mode === 'keep') {
      const slug = suggestSlug(c.image);
      if (slug) {
        form.icon_mode = 'name';
        form.icon_name = slug;
      }
    }
    if (!form.sub) form.sub = form.container.replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '');
  }

  function suggestSlug(image) {
    let s = image.split('/').pop().split('@')[0].split(':')[0].toLowerCase();
    return /^[a-z0-9][a-z0-9-]{0,63}$/.test(s) ? s : '';
  }

  function pickFile(e) {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    if (f.size > 512 * 1024) {
      formError = 'Icons are limited to 512 KB.';
      return;
    }
    const reader = new FileReader();
    reader.onload = () => (form.icon_data = String(reader.result).split(',')[1] || '');
    reader.readAsDataURL(f);
  }

  async function saveIcon(id) {
    if (form.icon_mode === 'stack' && form.icon_stack) {
      const [stack, service = ''] = form.icon_stack.split('/');
      return put(`/api/routes/${id}/icon`, { stack, service });
    }
    if (form.icon_mode === 'name' && form.icon_name.trim()) return put(`/api/routes/${id}/icon`, { name: form.icon_name.trim() });
    if (form.icon_mode === 'url' && form.icon_url.trim()) return put(`/api/routes/${id}/icon`, { url: form.icon_url.trim() });
    if (form.icon_mode === 'upload' && form.icon_data) return put(`/api/routes/${id}/icon`, { data: form.icon_data });
    if (form.icon_mode === 'auto' && (form.has_icon || form.icon_link)) return del(`/api/routes/${id}/icon`);
  }

  function create() {
    form = blank();
    form.domain = domains[0]?.name || '';
    formError = '';
    open = true;
  }

  function edit(r) {
    const d = domains.find((x) => r.host === x.name || r.host.endsWith('.' + x.name));
    form = {
      ...blank(),
      id: r.id,
      name: r.name,
      domain: d?.name || '',
      sub: d ? (r.host === d.name ? '' : r.host.slice(0, -(d.name.length + 1))) : r.host,
      path_prefix: r.path_prefix,
      kind: r.kind,
      container: r.target.container || '',
      port: r.target.port || '',
      upstream_tls: !!r.target.upstream_tls,
      url: r.target.url || '',
      tls_insecure_skip_verify: !!r.target.tls_insecure_skip_verify,
      folder: r.target.folder || '',
      auth: r.auth,
      enabled: r.enabled,
      connect_network: false,
      custom_rate: !!r.rate_limit,
      rate_events: r.rate_limit?.events ?? 100,
      rate_window: r.rate_limit?.window ?? '1m',
      hub_visible: r.hub.visible,
      hub_title: r.hub.title,
      hub_subtitle: r.hub.subtitle,
      has_icon: r.hub.has_icon,
      icon_link: r.hub.icon_link,
      icon: r.icon,
    };
    formError = '';
    open = true;
  }

  let host = $derived(form.sub ? `${form.sub}.${form.domain}` : form.domain);

  function body() {
    const target = {};
    if (form.kind === 'container') {
      target.container = form.container;
      target.port = Number(form.port);
      if (form.upstream_tls) target.upstream_tls = true;
    } else if (form.kind === 'external') {
      target.url = form.url.trim();
      if (form.tls_insecure_skip_verify) target.tls_insecure_skip_verify = true;
    } else {
      target.folder = form.folder.trim();
    }
    return {
      name: form.name.trim(),
      host,
      path_prefix: form.path_prefix.trim(),
      kind: form.kind,
      target,
      auth: form.auth,
      enabled: form.enabled,
      connect_network: form.kind === 'container' && form.connect_network,
      rate_limit: form.custom_rate ? { events: Number(form.rate_events), window: form.rate_window } : null,
      hub: { visible: form.hub_visible, title: form.hub_title.trim(), subtitle: form.hub_subtitle.trim() },
    };
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    formError = '';
    try {
      const saved = form.id ? await put(`/api/routes/${form.id}`, body()) : await post('/api/routes', body());
      try {
        await saveIcon(saved.id);
      } catch (err) {
        toast(`Route saved, but the icon could not be set: ${describe(err)}`, 'error');
      }
      // Reload before closing, so reopening a route shows what was saved.
      await load();
      toast(`Route ${form.name} saved and applied`);
      open = false;
    } catch (err) {
      formError = describe(err);
    } finally {
      busy = false;
    }
  }

  async function remove(r) {
    if (!confirm(`Delete route ${r.name} (${r.host}${r.path_prefix})?`)) return;
    try {
      await del(`/api/routes/${r.id}`);
      toast(`Route ${r.name} deleted`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  function targetText(r) {
    if (r.kind === 'container') return `${r.target.container}:${r.target.port}`;
    if (r.kind === 'external') return r.target.url;
    return `${r.kind === 'spa' ? 'SPA' : 'static'} · ${r.target.folder}`;
  }
</script>

<div class="stack">
  <div class="spread">
    <p class="muted small">Every change is validated, applied to Caddy, health-checked and rolled back automatically on failure.</p>
    <button class="primary" onclick={create} disabled={!loaded || !domains.length} title="Publish a container, a host of your network or a static folder">New route</button>
  </div>
  {#if !domains.length}
    <div class="alert">Add a domain first on the <a href="/domains" use:link>Domains</a> page.</div>
  {/if}
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>Name</th><th>Address</th><th>Target</th><th>Access</th><th></th></tr></thead>
      <tbody>
        {#each routes as r (r.id)}
          <tr>
            <td><strong>{r.name}</strong> {#if !r.enabled}<span class="badge">disabled</span>{/if}</td>
            <td class="mono small"><a href={'https://' + r.host + r.path_prefix} target="_blank" rel="noopener noreferrer">{r.host}{r.path_prefix}</a></td>
            <td class="mono small">{targetText(r)}</td>
            <td>
              {#if r.auth === 'admins'}<span class="badge ok">admins only</span>{:else if r.auth === 'authelia'}<span class="badge ok">Authelia 2FA</span>{:else}<span class="badge warn">public</span>{/if}
              {#if r.rate_limit}<span class="badge">{r.rate_limit.events}/{r.rate_limit.window}</span>{/if}
            </td>
            <td><div class="row end"><button class="small" onclick={() => edit(r)}>Edit</button><button class="small danger" onclick={() => remove(r)}>Delete</button></div></td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">No routes yet.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal bind:open title={form.id ? 'Edit route' : 'New route'} wide>
  <form id="route-form" onsubmit={save}>
    {#if formError}<div class="alert error">{formError}</div>{/if}
    <div class="field">
      <label for="rk">Target type</label>
      <select id="rk" bind:value={form.kind}>
        <option value="container">Container</option>
        <option value="external">External host (LAN device, other server)</option>
        <option value="static">Static folder</option>
        <option value="spa">Single-page app (falls back to index.html)</option>
      </select>
    </div>

    {#if form.kind === 'container'}
      <div class="grid">
        <div class="field">
          <label for="rc">Container</label>
          <select id="rc" value={form.container} onchange={pickContainer}>
            <option value="">— choose —</option>
            {#each containers.filter((c) => !c.system) as c (c.id)}
              <option value={c.name}>{c.name} ({c.image})</option>
            {/each}
          </select>
        </div>
        <div class="field">
          <label for="rp">Port</label>
          {#if selected && selected.ports.length > 1}
            <select id="rp" bind:value={form.port}>
              {#each selected.ports as p}<option value={p}>{p}</option>{/each}
            </select>
          {:else}
            <input id="rp" type="number" min="1" max="65535" bind:value={form.port} required />
          {/if}
        </div>
      </div>
      {#if selected && !selected.routable}
        <label class="check"><input type="checkbox" bind:checked={form.connect_network} /> Attach the container to the apps network so Caddy can reach it</label>
      {/if}
      <label class="check"><input type="checkbox" bind:checked={form.upstream_tls} /> The container itself speaks HTTPS</label>
    {:else if form.kind === 'external'}
      <div class="field">
        <label for="ru">Upstream URL</label>
        <input id="ru" bind:value={form.url} placeholder="https://192.168.1.20:5001" required />
        <div class="help">Loopback and link-local addresses are refused.</div>
      </div>
      {#if form.url.startsWith('https://')}
        <label class="check"><input type="checkbox" bind:checked={form.tls_insecure_skip_verify} /> Accept the upstream's self-signed certificate</label>
      {/if}
    {:else}
      <div class="field">
        <label for="rf">Folder</label>
        <input id="rf" bind:value={form.folder} placeholder="my-site" required />
        <div class="help">A folder inside <code>data/static/</code> on the server.</div>
      </div>
    {/if}

    <div class="grid mt">
      <div class="field">
        <label for="rs">Subdomain</label>
        <input id="rs" bind:value={form.sub} placeholder="app (empty = the domain itself)" />
      </div>
      <div class="field">
        <label for="rd">Domain</label>
        <select id="rd" bind:value={form.domain}>
          {#each domains as d (d.id)}<option value={d.name}>{d.name}</option>{/each}
        </select>
      </div>
      <div class="field">
        <label for="rpp">Path prefix (optional)</label>
        <input id="rpp" bind:value={form.path_prefix} placeholder="/api" />
      </div>
    </div>
    <p class="small muted">Address: <span class="mono">https://{host}{form.path_prefix}</span></p>

    <div class="grid">
      <div class="field">
        <label for="rn">Name</label>
        <input id="rn" bind:value={form.name} required />
      </div>
      <div class="field">
        <label for="ra">Access</label>
        <select id="ra" bind:value={form.auth}>
          <option value="authelia">Require Authelia login (2FA)</option>
          <option value="admins">Administrators only (Authelia 2FA)</option>
          <option value="none">Public (the app handles its own login)</option>
        </select>
      </div>
    </div>
    <label class="check"><input type="checkbox" bind:checked={form.custom_rate} /> Custom rate limit</label>
    {#if form.custom_rate}
      <div class="grid">
        <div class="field"><label for="re">Requests</label><input id="re" type="number" min="1" bind:value={form.rate_events} /></div>
        <div class="field"><label for="rw">Per window</label><input id="rw" bind:value={form.rate_window} placeholder="1m" /></div>
      </div>
    {/if}
    <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Enabled</label>

    <h3 class="mt">Hub</h3>
    <label class="check"><input type="checkbox" bind:checked={form.hub_visible} /> Show this app on the hub</label>
    {#if form.hub_visible}
      <div class="grid mt">
        <div class="field">
          <label for="hbt">Title</label>
          <input id="hbt" bind:value={form.hub_title} placeholder={form.name || 'App name'} maxlength="64" />
        </div>
        <div class="field">
          <label for="hbs">Subtitle</label>
          <input id="hbs" bind:value={form.hub_subtitle} placeholder="Films & séries" maxlength="120" />
        </div>
      </div>
      <div class="field">
        <label for="hbi">Icon</label>
        <div class="row">
          {#if form.icon_mode === 'keep' && (form.icon || (!form.id && serviceIcon))}<img class="tile-icon" src={form.icon || serviceIcon} alt="Current icon" />{/if}
          {#if form.icon_mode === 'auto' && serviceIcon}<img class="tile-icon" src={serviceIcon} alt="" />{/if}
          <select id="hbi" bind:value={form.icon_mode}>
            <option value="keep">{form.id ? (iconSource ? `Keep: ${iconSource}` : 'Keep: initials (no icon)') : service ? `The icon of its service, ${service}` : 'Initials (no icon)'}</option>
            {#if form.has_icon || form.icon_link}<option value="auto">{service ? `The icon of its service, ${service}` : 'Initials (no icon of its own)'}</option>{/if}
            {#if stackIcons.length}<option value="stack">The icon of another stack or service</option>{/if}
            <option value="name">By name (dashboard-icons)</option>
            <option value="url">From an https:// URL</option>
            <option value="upload">Upload a file</option>
          </select>
        </div>
        {#if form.icon_mode === 'stack'}
          <div class="row mt nowrap">
            <select bind:value={form.icon_stack} aria-label="Stack or service whose icon to use">
              {#each stackIcons as i (iconKey(i))}
                <option value={iconKey(i)}>{i.service ? `${i.stack} › ${i.service}` : `${i.stack} (stack)`}</option>
              {/each}
            </select>
            {#if stackIconPreview}<img class="tile-icon" src={stackIconPreview} alt="" />{/if}
          </div>
          <div class="help">The route follows that icon: changing it on the stack page changes it here too.</div>
        {:else if form.icon_mode === 'name'}
          <input class="mt" bind:value={form.icon_name} placeholder="jellyfin" />
          <div class="help">Name from the dashboard-icons collection (e.g. jellyfin, nextcloud, home-assistant). Marineris downloads it once and serves it itself.</div>
        {:else if form.icon_mode === 'url'}
          <input class="mt" bind:value={form.icon_url} placeholder="https://example.com/icon.png" />
        {:else if form.icon_mode === 'upload'}
          <input class="mt" type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/svg+xml,image/x-icon" onchange={pickFile} />
          <div class="help">PNG, JPEG, WebP, GIF, ICO or SVG, up to 512 KB.</div>
        {/if}
        {#if ['name', 'url', 'upload'].includes(form.icon_mode) && service}
          <div class="help">This icon is the route's only. To change the app's icon everywhere, set it on the service (stack page → Icon).</div>
        {/if}
      </div>
    {/if}
  </form>
  {#snippet actions()}
    <button onclick={() => (open = false)}>Cancel</button>
    <button class="primary" type="submit" form="route-form" disabled={busy}>{busy ? 'Applying…' : 'Save & apply'}</button>
  {/snippet}
</Modal>
