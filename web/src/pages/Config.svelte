<script>
  // Proxy & SSO: what Caddy (reverse proxy, certificates, shield) and
  // Authelia (single sign-on) are configured to do. Both configurations are
  // generated from the Routes, Domains and Settings pages; this page shows
  // them in plain terms, their history (with restore) and the raw files.
  import { onMount } from 'svelte';
  import { get, post, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';
  import Pencil from '@lucide/svelte/icons/pencil';

  let component = $state('caddy');
  let view = $state('overview');
  let summary = $state(null);
  let history = $state([]);
  let current = $state('');
  let error = $state('');
  let versionOpen = $state(false);
  let version = $state(null);
  let applying = $state(false);

  async function load() {
    error = '';
    try {
      [summary, history, current] = await Promise.all([
        get('/api/config/summary'),
        get(`/api/config/components/${component}/history`),
        get(`/api/config/components/${component}/current`).then((r) => r.content),
      ]);
    } catch (err) {
      error = describe(err);
    }
  }
  onMount(load);

  function pick(c) {
    component = c;
    load();
  }

  async function show(v) {
    version = null;
    versionOpen = true;
    try {
      version = await get(`/api/config/versions/${v.id}`);
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function rollback(v) {
    if (!confirm(`Restore ${v.component} configuration #${v.id} from ${new Date(v.created_at).toLocaleString()}?`)) return;
    try {
      await post(`/api/config/versions/${v.id}/rollback`);
      toast('Previous configuration restored');
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function applyNow() {
    applying = true;
    try {
      await post('/api/config/apply');
      toast('Configuration re-applied');
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      applying = false;
    }
  }

  const badge = { applied: 'ok', superseded: '', failed: 'error', rolled_back: 'warn' };
  const access = {
    none: ['warn', 'public'],
    authelia: ['ok', 'Authelia 2FA'],
    admins: ['ok', 'admins only'],
  };
  const role = { ui: 'Marineris UI', portal: 'login portal', route: 'route' };
  const policy = { two_factor: ['ok', 'two-factor login'], one_factor: ['warn', 'password only'], deny: ['error', 'deny'], bypass: ['warn', 'no login'] };
</script>

<div class="stack">
  <p class="muted small">
    What the reverse proxy (Caddy) and the single sign-on (Authelia) do. Both are generated from the
    <a href="/routes" use:link>Routes</a>, <a href="/domains" use:link>Domains</a> and <a href="/settings" use:link>Settings</a> pages: change things there,
    and every change is validated, applied and rolled back automatically if it fails.
  </p>
  <div class="spread">
    <div class="tabs no-margin">
      <button class:active={component === 'caddy'} onclick={() => pick('caddy')}>Caddy — proxy &amp; certificates</button>
      <button class:active={component === 'authelia'} onclick={() => pick('authelia')}>Authelia — single sign-on</button>
    </div>
    <button onclick={applyNow} disabled={applying}>{applying ? 'Applying…' : 'Re-apply now'}</button>
  </div>
  <div class="tabs">
    <button class:active={view === 'overview'} onclick={() => (view = 'overview')}>Overview</button>
    <button class:active={view === 'history'} onclick={() => (view = 'history')}>History</button>
    <button class:active={view === 'raw'} onclick={() => (view = 'raw')}>Generated file</button>
  </div>
  {#if error}<div class="alert error">{error}</div>{/if}

  {#if view === 'overview' && summary}
    {#if component === 'caddy'}
      {@const c = summary.caddy}
      <div class="grid">
        <div class="card kv">
          <span class="label">Certificates</span><span>{c.ca}</span>
          <span class="muted small">account {c.acme_email || '—'}</span>
          <a class="small row tight inline" href="/settings" use:link><Pencil size={13} aria-hidden="true" />Settings</a>
        </div>
        <div class="card kv">
          <span class="label">Shield</span>
          <span>CrowdSec {c.crowdsec ? 'blocks banned IPs' : 'is off'}</span>
          <span class="muted small">default rate limit: {c.default_rate_limit || 'none'}</span>
          <a class="small row tight inline" href="/settings" use:link><Pencil size={13} aria-hidden="true" />Settings</a>
        </div>
      </div>
      <div class="card table-wrap">
        <h2>Sites</h2>
        <table>
          <thead><tr><th>Address</th><th>Serves</th><th>Access</th><th>Rate limit</th><th>Certificate</th><th></th></tr></thead>
          <tbody>
            {#each c.sites as s (s.role + s.address)}
              <tr class:muted={!s.enabled}>
                <td class="mono small">{s.address}{#if !s.enabled} <span class="badge">disabled</span>{/if}</td>
                <td class="small"><strong>{s.name}</strong> <span class="muted">· {role[s.role]}</span><div class="muted">{s.target}</div></td>
                <td><span class="badge {access[s.access]?.[0] ?? ''}">{access[s.access]?.[1] ?? s.access}</span></td>
                <td class="small">{s.rate_limit || '—'}</td>
                <td class="small">{s.tls}</td>
                <td><a class="small row tight inline" href={s.link} use:link title="Edit"><Pencil size={13} aria-hidden="true" />Edit</a></td>
              </tr>
            {:else}
              <tr><td colspan="6" class="muted">Nothing is published yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      {@const a = summary.authelia}
      <div class="grid">
        <div class="card kv">
          <span class="label">Default</span>
          <span>Everything not listed below is <strong>denied</strong></span>
          <span class="muted small">second factors: {a.second_factors.join(', ')}</span>
        </div>
        <div class="card kv">
          <span class="label">Sessions</span>
          <span>{a.session.expiration} at most, {a.session.inactivity} idle</span>
          <span class="muted small">{a.regulation.max_retries} failed logins in {a.regulation.find_time} → banned {a.regulation.ban_time}</span>
        </div>
        <div class="card kv">
          <span class="label">Login portals</span>
          {#each a.portals as p (p.domain)}<span class="mono small">{p.url}</span>{:else}<span class="muted">none (add a domain)</span>{/each}
          <a class="small row tight inline" href="/domains" use:link><Pencil size={13} aria-hidden="true" />Domains</a>
        </div>
      </div>
      <div class="card table-wrap">
        <h2>Access rules <span class="muted small">(the first matching rule applies)</span></h2>
        <table>
          <thead><tr><th>#</th><th>Sites</th><th>Paths</th><th>Who</th><th>Result</th></tr></thead>
          <tbody>
            {#each a.rules as r, i (i)}
              <tr>
                <td class="muted">{i + 1}</td>
                <td class="mono small">{r.domains.join(', ')}</td>
                <td class="mono small">{r.resources.length ? r.resources.join(', ') : 'all'}</td>
                <td class="small">{r.subjects.length ? r.subjects.map((x) => x.replace('group:', 'group ')).join(', ') : 'anyone'}</td>
                <td><span class="badge {policy[r.policy]?.[0] ?? ''}">{policy[r.policy]?.[1] ?? r.policy}</span></td>
              </tr>
            {:else}
              <tr><td colspan="5" class="muted">No protected site yet: choose “Authelia” or “Administrators only” on a route.</td></tr>
            {/each}
          </tbody>
        </table>
        <div class="row mt"><a class="small row tight inline" href="/routes" use:link><Pencil size={13} aria-hidden="true" />Change who can open a site on the Routes page</a></div>
      </div>
      <div class="card table-wrap">
        <h2>Accounts</h2>
        <table>
          <thead><tr><th>User</th><th>Groups</th><th>State</th></tr></thead>
          <tbody>
            {#each a.users as u (u.username)}
              <tr><td>{u.username}</td><td class="small">{u.groups.join(', ') || '—'}</td><td>{#if u.disabled}<span class="badge">disabled</span>{:else}<span class="badge ok">active</span>{/if}</td></tr>
            {/each}
          </tbody>
        </table>
        <p class="muted small mt">Users and groups beyond the administrator come in Phase 2.</p>
      </div>
    {/if}
  {:else if view === 'history'}
    <div class="card">
      <p class="small muted">Every attempt is recorded (encrypted). Failed changes never replace the running configuration.</p>
      <div class="table-wrap">
        <table>
          <thead><tr><th>#</th><th>When</th><th>By</th><th>Reason</th><th>Status</th><th></th></tr></thead>
          <tbody>
            {#each history as v (v.id)}
              <tr>
                <td>{v.id}</td>
                <td class="small">{new Date(v.created_at).toLocaleString()}</td>
                <td>{v.actor}</td>
                <td class="small">{v.reason}</td>
                <td>
                  <span class="badge {badge[v.status] ?? ''}">{v.status.replace('_', ' ')}</span>
                  {#if v.error}<div class="small muted">{v.error}</div>{/if}
                </td>
                <td>
                  <div class="row end">
                    <button class="small" onclick={() => show(v)}>View</button>
                    {#if v.status === 'superseded'}<button class="small" onclick={() => rollback(v)}>Restore</button>{/if}
                  </div>
                </td>
              </tr>
            {:else}
              <tr><td colspan="6" class="muted">Nothing applied yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {:else if view === 'raw'}
    <div class="card">
      <h2>Current generated file <span class="muted small">(secrets redacted)</span></h2>
      <p class="muted small">Read-only: Marineris writes this file again after each change. Raw edits that survive regeneration come in Phase 2.</p>
      <pre>{current}</pre>
    </div>
  {/if}
</div>

<Modal bind:open={versionOpen} title={version ? `${version.version.component} #${version.version.id}` : 'Configuration'} wide>
  {#if version}<pre>{version.content}</pre>{:else}<p class="muted">Loading…</p>{/if}
</Modal>
