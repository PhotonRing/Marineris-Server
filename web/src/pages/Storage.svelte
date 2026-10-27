<script>
  import { onMount } from 'svelte';
  import { get, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  let tab = $state('volumes');
  let volumes = $state([]);
  let networks = $state([]);
  let error = $state('');
  let confirmOpen = $state(false);
  let target = $state('');
  let typed = $state('');

  async function load() {
    try {
      [volumes, networks] = await Promise.all([get('/api/volumes'), get('/api/networks')]);
    } catch (err) {
      error = describe(err);
    }
  }
  onMount(load);

  function askDelete(v) {
    target = v.name;
    typed = '';
    confirmOpen = true;
  }

  async function doDelete() {
    try {
      await del(`/api/volumes/${encodeURIComponent(target)}`);
      toast(`Volume ${target} deleted`);
      confirmOpen = false;
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }
</script>

<div class="tabs">
  <button class:active={tab === 'volumes'} onclick={() => (tab = 'volumes')}>Volumes ({volumes.length})</button>
  <button class:active={tab === 'networks'} onclick={() => (tab = 'networks')}>Networks ({networks.length})</button>
</div>
{#if error}<div class="alert error">{error}</div>{/if}

{#if tab === 'volumes'}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>Name</th><th>Stack</th><th>Driver</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {#each volumes as v (v.name)}
          <tr>
            <td class="mono small">{v.name} {#if v.system}<span class="badge">system</span>{/if}</td>
            <td>{v.stack || ''}</td>
            <td>{v.driver}</td>
            <td class="small muted">{v.created_at ? new Date(v.created_at).toLocaleString() : ''}</td>
            <td class="right">{#if !v.system}<button class="small danger" onclick={() => askDelete(v)}>Delete</button>{/if}</td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">No volumes.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>Name</th><th>Driver</th><th>Scope</th><th>Stack</th></tr></thead>
      <tbody>
        {#each networks as n (n.id)}
          <tr>
            <td class="mono small">{n.name} {#if n.internal}<span class="badge">internal</span>{/if}</td>
            <td>{n.driver}</td>
            <td>{n.scope}</td>
            <td>{n.stack || ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<Modal bind:open={confirmOpen} title="Delete volume">
  <p>Deleting <strong class="mono">{target}</strong> permanently destroys its data.</p>
  <div class="field">
    <label for="vc">Type the volume name to confirm</label>
    <input id="vc" bind:value={typed} autocomplete="off" />
  </div>
  {#snippet actions()}
    <button onclick={() => (confirmOpen = false)}>Cancel</button>
    <button class="danger" disabled={typed !== target} onclick={doDelete}>Delete</button>
  {/snippet}
</Modal>
