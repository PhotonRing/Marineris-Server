<script>
  import { onMount } from 'svelte';
  import { get, post, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  let list = $state([]);
  let error = $state('');
  let filter = $state('');
  let busy = $state('');
  let logsOpen = $state(false);
  let logs = $state('');
  let logsTitle = $state('');
  let detailOpen = $state(false);
  let detail = $state(null);

  let shown = $derived(
    list.filter((c) => !filter || c.name.includes(filter) || c.image.includes(filter) || (c.stack || '').includes(filter)),
  );

  async function load() {
    try {
      list = await get('/api/containers');
      error = '';
    } catch (err) {
      error = describe(err);
    }
  }
  onMount(load);

  async function act(c, action) {
    busy = c.id + action;
    try {
      await post(`/api/containers/${encodeURIComponent(c.name)}/${action}`);
      toast(`${c.name}: ${action} done`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }

  async function remove(c) {
    if (!confirm(`Remove container ${c.name}? Its anonymous data is lost.`)) return;
    try {
      await del(`/api/containers/${encodeURIComponent(c.name)}`);
      toast(`${c.name} removed`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function showLogs(c) {
    logsTitle = c.name;
    logs = 'Loading…';
    logsOpen = true;
    try {
      logs = (await get(`/api/containers/${encodeURIComponent(c.name)}/logs?tail=500`)).logs || '(no output)';
    } catch (err) {
      logs = describe(err);
    }
  }

  async function showDetail(c) {
    detail = null;
    detailOpen = true;
    try {
      detail = await get(`/api/containers/${encodeURIComponent(c.name)}`);
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  function stateBadge(s) {
    return s === 'running' ? 'ok' : s === 'exited' || s === 'dead' ? 'error' : 'warn';
  }
</script>

<div class="stack">
  <div class="spread">
    <input class="w-full" placeholder="Filter by name, image or stack…" bind:value={filter} />
    <button onclick={load}>Refresh</button>
  </div>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card table-wrap">
    <table>
      <thead>
        <tr><th>Name</th><th>State</th><th>Image</th><th>Ports</th><th>Stack</th><th></th></tr>
      </thead>
      <tbody>
        {#each shown as c (c.id)}
          <tr>
            <td>
              <button class="ghost small" onclick={() => showDetail(c)}>{c.name}</button>
              {#if c.system}<span class="badge" title="Part of Marineris">system</span>{/if}
            </td>
            <td><span class="badge {stateBadge(c.state)}">{c.state}</span>{#if c.health}<span class="badge">{c.health}</span>{/if}</td>
            <td class="mono small">{c.image}</td>
            <td class="mono small">{c.ports.join(', ')}</td>
            <td>{c.stack || ''}</td>
            <td class="right">
              <div class="row end">
                {#if c.state === 'running'}
                  <button class="small" disabled={!!busy} onclick={() => act(c, 'restart')}>Restart</button>
                  {#if !c.system}<button class="small" disabled={!!busy} onclick={() => act(c, 'stop')}>Stop</button>{/if}
                {:else}
                  <button class="small" disabled={!!busy} onclick={() => act(c, 'start')}>Start</button>
                  {#if !c.system}<button class="small danger" onclick={() => remove(c)}>Remove</button>{/if}
                {/if}
                <button class="small" onclick={() => showLogs(c)}>Logs</button>
              </div>
            </td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted">No containers.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal bind:open={logsOpen} title={'Logs · ' + logsTitle} wide>
  <pre>{logs}</pre>
</Modal>

<Modal bind:open={detailOpen} title={detail?.name || 'Container'} wide>
  {#if detail}
    <div class="grid">
      <div><div class="muted small">Image</div><div class="mono small">{detail.image}</div></div>
      <div><div class="muted small">State</div>{detail.state} {detail.exit_code ? `(exit ${detail.exit_code})` : ''}</div>
      <div><div class="muted small">Restart policy</div>{detail.restart_policy || '—'}</div>
      <div><div class="muted small">Networks</div>{detail.networks.join(', ')}</div>
      <div><div class="muted small">Exposed ports</div>{detail.exposed_ports.join(', ') || '—'}</div>
      <div><div class="muted small">Reachable by Caddy</div>{detail.routable ? 'yes' : 'no'}</div>
    </div>
    <h3 class="mt">Command</h3>
    <pre>{detail.command.join(' ')}</pre>
    <h3>Mounts</h3>
    <pre>{detail.mounts.join('\n') || '—'}</pre>
    <h3>Environment <span class="muted small">(values hidden)</span></h3>
    <pre>{detail.env.join('\n') || '—'}</pre>
  {:else}
    <p class="muted">Loading…</p>
  {/if}
</Modal>
