<script>
  import { onMount } from 'svelte';
  import { get, post, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  let component = $state('caddy');
  let history = $state([]);
  let current = $state('');
  let error = $state('');
  let viewOpen = $state(false);
  let view = $state(null);
  let applying = $state(false);

  async function load() {
    error = '';
    try {
      history = await get(`/api/config/components/${component}/history`);
      current = (await get(`/api/config/components/${component}/current`)).content;
    } catch (err) {
      error = describe(err);
      current = '';
    }
  }
  onMount(load);

  function pick(c) {
    component = c;
    load();
  }

  async function show(v) {
    view = null;
    viewOpen = true;
    try {
      view = await get(`/api/config/versions/${v.id}`);
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
</script>

<div class="stack">
  <div class="spread">
    <div class="tabs">
      <button class:active={component === 'caddy'} onclick={() => pick('caddy')}>Caddy</button>
      <button class:active={component === 'authelia'} onclick={() => pick('authelia')}>Authelia</button>
    </div>
    <button onclick={applyNow} disabled={applying}>{applying ? 'Applying…' : 'Re-apply now'}</button>
  </div>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card">
    <h2>History</h2>
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
  <div class="card">
    <h2>Current generated configuration <span class="muted small">(secrets redacted)</span></h2>
    <pre>{current}</pre>
  </div>
</div>

<Modal bind:open={viewOpen} title={view ? `${view.version.component} #${view.version.id}` : 'Configuration'} wide>
  {#if view}<pre>{view.content}</pre>{:else}<p class="muted">Loading…</p>{/if}
</Modal>
