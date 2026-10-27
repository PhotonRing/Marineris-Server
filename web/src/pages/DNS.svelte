<script>
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  let providers = $state([]);
  let profiles = $state([]);
  let error = $state('');
  let open = $state(false);
  let form = $state({ id: 0, name: '', provider: 'ovh', config: {} });
  let existing = $state(null);
  let formError = $state('');
  let busy = $state(false);
  let testOpen = $state(false);
  let testProfile = $state(null);
  let testZone = $state('');
  let testResult = $state(null);
  let testing = $state(false);

  let provider = $derived(providers.find((p) => p.id === form.provider));

  async function load() {
    try {
      [providers, profiles] = await Promise.all([get('/api/dns/providers'), get('/api/dns/profiles')]);
    } catch (err) {
      error = describe(err);
    }
  }
  onMount(load);

  function create() {
    existing = null;
    form = { id: 0, name: '', provider: 'ovh', config: {} };
    formError = '';
    open = true;
  }

  function edit(p) {
    existing = p;
    const cfg = {};
    for (const [k, v] of Object.entries(p.config || {})) cfg[k] = typeof v === 'string' ? v : '';
    form = { id: p.id, name: p.name, provider: p.provider, config: cfg };
    formError = '';
    open = true;
  }

  function secretSet(key) {
    const v = existing?.config?.[key];
    return v && typeof v === 'object' && v.set;
  }

  async function save(e) {
    e.preventDefault();
    busy = true;
    formError = '';
    const config = {};
    for (const f of provider.fields) {
      const v = (form.config[f.key] || '').trim();
      if (v !== '' || !f.secret) config[f.key] = v;
    }
    try {
      if (form.id) await put(`/api/dns/profiles/${form.id}`, { name: form.name, config });
      else await post('/api/dns/profiles', { name: form.name, provider: form.provider, config });
      toast(`Profile ${form.name} saved`);
      open = false;
      await load();
    } catch (err) {
      formError = describe(err);
    } finally {
      busy = false;
    }
  }

  async function remove(p) {
    if (!confirm(`Delete DNS profile ${p.name}?`)) return;
    try {
      await del(`/api/dns/profiles/${p.id}`);
      toast(`Profile ${p.name} deleted`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  function openTest(p) {
    testProfile = p;
    testZone = p.domains[0] || '';
    testResult = null;
    testOpen = true;
  }

  async function runTest(e) {
    e.preventDefault();
    testing = true;
    testResult = null;
    try {
      testResult = await post(`/api/dns/profiles/${testProfile.id}/test`, { zone: testZone.trim().toLowerCase() });
      await load();
    } catch (err) {
      testResult = { ok: false, steps: [], error: describe(err) };
    } finally {
      testing = false;
    }
  }
</script>

<div class="stack">
  <div class="spread">
    <p class="muted small">
      Credentials used to prove domain ownership through DNS (DNS-01) — required for wildcard certificates. Secrets are
      encrypted at rest and never shown again.
    </p>
    <button class="primary" onclick={create}>New profile</button>
  </div>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>Name</th><th>Provider</th><th>Domains</th><th>Last test</th><th></th></tr></thead>
      <tbody>
        {#each profiles as p (p.id)}
          <tr>
            <td><strong>{p.name}</strong></td>
            <td>{providers.find((x) => x.id === p.provider)?.name || p.provider}</td>
            <td class="mono small">{p.domains.join(', ') || '—'}</td>
            <td>
              {#if !p.last_test_at}<span class="badge warn">never tested</span>
              {:else if p.last_test_ok}<span class="badge ok">passed</span>
              {:else}<span class="badge error">failed</span>{/if}
              {#if p.last_test_at}<div class="small muted">{new Date(p.last_test_at).toLocaleString()}</div>{/if}
            </td>
            <td>
              <div class="row end">
                <button class="small" onclick={() => openTest(p)}>Test</button>
                <button class="small" onclick={() => edit(p)}>Edit</button>
                <button class="small danger" onclick={() => remove(p)}>Delete</button>
              </div>
            </td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">No DNS profiles. Create one per provider account (e.g. “OVH personal”, “OVH friend”, “Cloudflare”).</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal bind:open title={form.id ? `Edit ${form.name}` : 'New DNS profile'} wide>
  <form id="dns-form" onsubmit={save}>
    {#if formError}<div class="alert error">{formError}</div>{/if}
    <div class="grid">
      <div class="field">
        <label for="pn">Name</label>
        <input id="pn" bind:value={form.name} placeholder="OVH personal" required />
      </div>
      <div class="field">
        <label for="pp">Provider</label>
        <select id="pp" bind:value={form.provider} disabled={!!form.id}>
          {#each providers as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
        </select>
      </div>
    </div>
    {#if provider}
      <div class="alert small">
        <strong>Least privilege:</strong>
        {provider.guide}
      </div>
      <div class="mt">
        {#each provider.fields as f (f.key)}
          <div class="field">
            <label for={'f-' + f.key}>{f.label}{f.required ? '' : ' (optional)'}</label>
            {#if f.options}
              <select id={'f-' + f.key} bind:value={form.config[f.key]}>
                {#each f.options as o}<option value={o}>{o}</option>{/each}
              </select>
            {:else}
              <input
                id={'f-' + f.key}
                type={f.secret ? 'password' : 'text'}
                autocomplete="off"
                spellcheck="false"
                bind:value={form.config[f.key]}
                placeholder={f.secret && secretSet(f.key) ? '•••••• (unchanged — type to replace)' : f.default || ''}
              />
            {/if}
            {#if f.help}<div class="help">{f.help}</div>{/if}
          </div>
        {/each}
      </div>
    {/if}
  </form>
  {#snippet actions()}
    <button onclick={() => (open = false)}>Cancel</button>
    <button class="primary" type="submit" form="dns-form" disabled={busy}>Save</button>
  {/snippet}
</Modal>

<Modal bind:open={testOpen} title={'Test ' + (testProfile?.name || '')}>
  <p class="small muted">
    Creates a temporary TXT record <code>_marineris-check</code> in the zone, reads it back and deletes it — exactly what a
    DNS-01 challenge needs.
  </p>
  <form id="test-form" onsubmit={runTest}>
    <div class="field">
      <label for="tz">Zone</label>
      <input id="tz" bind:value={testZone} placeholder="example.com" required />
    </div>
  </form>
  {#if testResult}
    <div class="alert {testResult.ok ? 'ok' : 'error'}">
      {#each testResult.steps as s}<div>{s}</div>{/each}
      {#if testResult.error}<div>{testResult.error}</div>{/if}
    </div>
  {/if}
  {#snippet actions()}
    <button class="primary" type="submit" form="test-form" disabled={testing}>{testing ? 'Testing…' : 'Run test'}</button>
  {/snippet}
</Modal>
