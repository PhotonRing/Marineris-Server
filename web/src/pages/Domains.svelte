<script>
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import { link } from '../lib/router.js';
  import Modal from '../components/Modal.svelte';

  let domains = $state([]);
  let profiles = $state([]);
  let certs = $state(null);
  let error = $state('');
  let open = $state(false);
  let form = $state({ id: 0, name: '', profile: '', wildcard: true });
  let formError = $state('');
  let busy = $state(false);

  async function load() {
    try {
      [domains, profiles] = await Promise.all([get('/api/domains'), get('/api/dns/profiles')]);
    } catch (err) {
      error = describe(err);
    }
  }

  async function loadCerts() {
    certs = null;
    try {
      certs = await get('/api/certificates');
    } catch (err) {
      certs = [];
      toast(describe(err), 'error');
    }
  }

  onMount(() => {
    load();
    loadCerts();
  });

  function profileName(id) {
    return profiles.find((p) => p.id === id)?.name || '—';
  }

  function create() {
    form = { id: 0, name: '', profile: profiles[0]?.id ?? '', wildcard: true };
    formError = '';
    open = true;
  }

  function edit(d) {
    form = { id: d.id, name: d.name, profile: d.dns_profile_id ?? '', wildcard: d.wildcard };
    formError = '';
    open = true;
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    formError = '';
    const body = { name: form.name.trim().toLowerCase(), dns_profile_id: form.profile === '' ? null : Number(form.profile), wildcard: form.wildcard };
    try {
      if (form.id) await put(`/api/domains/${form.id}`, body);
      else await post('/api/domains', body);
      toast(`Domain ${body.name} saved`);
      open = false;
      await load();
      loadCerts();
    } catch (err) {
      formError = describe(err);
    } finally {
      busy = false;
    }
  }

  async function remove(d) {
    if (!confirm(`Remove ${d.name}? Routes using it must be deleted first.`)) return;
    try {
      await del(`/api/domains/${d.id}`);
      toast(`Domain ${d.name} removed`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  function certBadge(c) {
    if (!c.ok) return 'error';
    if (c.staging || c.days_left < 14) return 'warn';
    return 'ok';
  }
</script>

<div class="stack">
  <div class="card">
    <div class="spread">
      <h2>Domains</h2>
      <button class="primary" onclick={create}>Add domain</button>
    </div>
    {#if error}<div class="alert error">{error}</div>{/if}
    <p class="small muted">
      With a DNS profile, certificates are issued through DNS-01 and cover the domain and <code>*.domain</code> (one wildcard
      certificate for every subdomain). Without one, each host gets its own certificate through HTTP-01.
    </p>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Domain</th><th>Challenge</th><th>DNS profile</th><th></th></tr></thead>
        <tbody>
          {#each domains as d (d.id)}
            <tr>
              <td class="mono">{d.name}</td>
              <td>{#if d.dns_profile_id}<span class="badge ok">DNS-01{d.wildcard ? ' · wildcard' : ''}</span>{:else}<span class="badge">HTTP-01</span>{/if}</td>
              <td>{profileName(d.dns_profile_id)}</td>
              <td><div class="row end"><button class="small" onclick={() => edit(d)}>Edit</button><button class="small danger" onclick={() => remove(d)}>Remove</button></div></td>
            </tr>
          {:else}
            <tr><td colspan="4" class="muted">No domains yet.</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>

  <div class="card">
    <div class="spread">
      <h2>Certificates</h2>
      <button class="small" onclick={loadCerts}>Re-check</button>
    </div>
    <p class="small muted">What Caddy actually serves for each host, checked with a real TLS handshake.</p>
    {#if certs === null}
      <p class="muted">Checking…</p>
    {:else}
      <div class="table-wrap">
        <table>
          <thead><tr><th>Host</th><th>Status</th><th>Issuer</th><th>Expires</th></tr></thead>
          <tbody>
            {#each certs as c (c.host)}
              <tr>
                <td class="mono small">{c.host}</td>
                <td>
                  <span class="badge {certBadge(c)}">{c.ok ? (c.staging ? 'staging' : 'valid') : 'problem'}</span>
                  {#if c.wildcard}<span class="badge">wildcard</span>{/if}
                  {#if c.error}<div class="small muted">{c.error}</div>{/if}
                </td>
                <td class="small">{c.issuer || '—'}</td>
                <td class="small">{c.not_after && c.ok ? `${new Date(c.not_after).toLocaleDateString()} (${c.days_left} days)` : '—'}</td>
              </tr>
            {:else}
              <tr><td colspan="4" class="muted">No hosts served yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</div>

<Modal bind:open title={form.id ? `Edit ${form.name}` : 'Add domain'}>
  <form id="domain-form" onsubmit={save}>
    {#if formError}<div class="alert error">{formError}</div>{/if}
    <div class="field">
      <label for="dn">Domain</label>
      <input id="dn" bind:value={form.name} placeholder="example.com" required disabled={!!form.id} />
    </div>
    <div class="field">
      <label for="dp">DNS profile</label>
      <select id="dp" bind:value={form.profile}>
        <option value="">None — use HTTP-01 (no wildcard)</option>
        {#each profiles as p (p.id)}<option value={p.id}>{p.name} ({p.provider})</option>{/each}
      </select>
      {#if !profiles.length}<div class="help">Create a profile on the <a href="/dns" use:link>DNS profiles</a> page to enable wildcard certificates.</div>{/if}
    </div>
    {#if form.profile !== ''}
      <label class="check"><input type="checkbox" bind:checked={form.wildcard} /> Issue a wildcard certificate (*.{form.name || 'domain'})</label>
    {/if}
  </form>
  {#snippet actions()}
    <button onclick={() => (open = false)}>Cancel</button>
    <button class="primary" type="submit" form="domain-form" disabled={busy}>{busy ? 'Applying…' : 'Save & apply'}</button>
  {/snippet}
</Modal>
