<script>
  import { onMount } from 'svelte';
  import { get, describe } from '../lib/api.js';

  let entries = $state([]);
  let error = $state('');
  let failuresOnly = $state(false);

  let shown = $derived(failuresOnly ? entries.filter((e) => !e.success) : entries);

  onMount(async () => {
    try {
      entries = await get('/api/audit');
    } catch (err) {
      error = describe(err);
    }
  });
</script>

<div class="stack">
  <label class="check"><input type="checkbox" bind:checked={failuresOnly} /> Failures only</label>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>When</th><th>Who</th><th>IP</th><th>Action</th><th>Target</th><th>Result</th></tr></thead>
      <tbody>
        {#each shown as e (e.id)}
          <tr>
            <td class="small">{new Date(e.at).toLocaleString()}</td>
            <td>{e.actor}</td>
            <td class="mono small">{e.ip}</td>
            <td class="mono small">{e.action}</td>
            <td class="small">{e.target}{#if e.details}<div class="muted">{e.details}</div>{/if}</td>
            <td><span class="badge {e.success ? 'ok' : 'error'}">{e.success ? 'ok' : 'failed'}</span></td>
          </tr>
        {:else}
          <tr><td colspan="6" class="muted">No entries.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
