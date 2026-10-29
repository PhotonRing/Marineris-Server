<script>
  // One User: display name and e-mail (the Username never changes), custom
  // Groups, disable or enable, delete. Only the Owner acts on Administrators
  // and on the Owner; the buttons follow that rule, the API enforces it.
  import ArrowLeft from '@lucide/svelte/icons/arrow-left';
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { path, navigate, link } from '../lib/router.js';
  import { app, toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  const BUILTIN = ['admins', 'proxy-admins'];
  const id = Number($path.split('/')[2] || 0);
  const base = `/api/users/${id}`;

  let user = $state(null);
  let error = $state('');
  let form = $state({ display_name: '', email: '' });
  let formError = $state('');
  let groupNames = $state([]); // every custom Group
  let chosen = $state([]); // the User's custom Groups being edited
  let groupError = $state('');
  let busy = $state('');
  let deleteOpen = $state(false);
  let deleteConfirm = $state('');

  let me = $derived(app.session?.user?.id === id);
  let viewerIsOwner = $derived(!!app.session?.rights?.owner);
  // Same rule as the API (mayActOn): an Administrator does not act on
  // another Administrator or on the Owner.
  let mayAct = $derived(!!user && (me || viewerIsOwner || (!user.owner && !user.groups.includes('admins'))));
  let builtins = $derived(user ? user.groups.filter((g) => BUILTIN.includes(g)) : []);

  function show(u) {
    user = u;
    form = { display_name: u.display_name, email: u.email };
    chosen = u.groups.filter((g) => !BUILTIN.includes(g));
  }

  onMount(async () => {
    try {
      show(await get(base));
    } catch (err) {
      error = describe(err);
      return;
    }
    // The Groups page's list when there is one; otherwise every custom Group
    // a User is in.
    try {
      const gs = await get('/api/groups');
      groupNames = gs.map((g) => (typeof g === 'string' ? g : g.name)).filter((g) => !BUILTIN.includes(g));
    } catch {
      const users = await get('/api/users').catch(() => []);
      groupNames = [...new Set(users.flatMap((u) => u.groups))].filter((g) => !BUILTIN.includes(g)).sort();
    }
  });

  async function saveProfile(e) {
    e.preventDefault();
    busy = 'profile';
    formError = '';
    try {
      show(await put(base, { display_name: form.display_name, email: form.email }));
      toast('Saved');
    } catch (err) {
      formError = describe(err);
    } finally {
      busy = '';
    }
  }

  function toggle(g) {
    chosen = chosen.includes(g) ? chosen.filter((x) => x !== g) : [...chosen, g];
  }

  async function saveGroups() {
    busy = 'groups';
    groupError = '';
    try {
      show(await put(`${base}/groups`, { groups: chosen }));
      toast('Groups saved');
    } catch (err) {
      groupError = describe(err);
    } finally {
      busy = '';
    }
  }

  async function setDisabled(disabled) {
    if (disabled && !confirm(`Disable ${user.username}? They are signed out everywhere and can no longer sign in.`)) return;
    busy = 'state';
    try {
      show(await post(`${base}/${disabled ? 'disable' : 'enable'}`));
      toast(disabled ? `${user.username} disabled` : `${user.username} enabled`);
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }

  async function remove() {
    busy = 'delete';
    try {
      const r = await del(base);
      toast(`${user.username} deleted`);
      if (r?.warning) toast(r.warning, 'error');
      deleteOpen = false;
      navigate('/users');
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }
</script>

<div class="stack">
  <div><a href="/users" use:link class="row small"><ArrowLeft size={14} aria-hidden="true" />Users</a></div>
  {#if error}<div class="alert error">{error}</div>{/if}

  {#if user}
    <div class="card">
      <div class="spread">
        <h2>
          {user.display_name}
          {#if user.owner}<span class="badge ok">Owner</span>{/if}
          <span class="badge {user.state === 'active' ? 'ok' : 'error'}">{user.state}</span>
        </h2>
      </div>
      {#if !mayAct}
        <div class="alert">Only the Owner manages Administrators and the Owner.</div>
      {/if}
      <form onsubmit={saveProfile} class="stack">
        {#if formError}<div class="alert error">{formError}</div>{/if}
        <div class="field">
          <label for="un">Username</label>
          <input id="un" value={user.username} disabled />
          <div class="help">A Username never changes: sessions, passkeys and app accounts are tied to it.</div>
        </div>
        <div class="field">
          <label for="dn">Display name</label>
          <input id="dn" bind:value={form.display_name} maxlength="64" required disabled={!mayAct} />
        </div>
        <div class="field">
          <label for="em">E-mail</label>
          <input id="em" type="email" bind:value={form.email} placeholder="optional" disabled={!mayAct} />
        </div>
        {#if mayAct}
          <div class="row end"><button class="primary" type="submit" disabled={busy === 'profile'}>Save</button></div>
        {/if}
      </form>
    </div>

    <div class="card">
      <h2>Groups</h2>
      {#if groupError}<div class="alert error">{groupError}</div>{/if}
      {#if builtins.length}
        <p class="small">
          Built-in: {#each builtins as g (g)}<span class="badge">{g}</span> {/each}
          <span class="muted">(managed by the Owner)</span>
        </p>
      {/if}
      {#each groupNames as g (g)}
        <label class="check"><input type="checkbox" checked={chosen.includes(g)} disabled={!mayAct} onchange={() => toggle(g)} /> {g}</label>
      {:else}
        <p class="small muted">No custom Groups yet.</p>
      {/each}
      {#if mayAct && groupNames.length}
        <div class="row end"><button onclick={saveGroups} disabled={busy === 'groups'}>Save Groups</button></div>
      {/if}
    </div>

    {#if mayAct && !me}
      <div class="card">
        <h2>Access</h2>
        <div class="row">
          {#if user.state === 'disabled'}
            <button onclick={() => setDisabled(false)} disabled={busy === 'state'}>Enable</button>
          {:else}
            <button onclick={() => setDisabled(true)} disabled={busy === 'state'}>Disable</button>
          {/if}
          <button class="danger" onclick={() => ((deleteConfirm = ''), (deleteOpen = true))}>Delete…</button>
        </div>
        <p class="small muted">
          Disabling keeps everything the User had (password, authenticator, passkeys, Groups) and signs them out
          everywhere. Deleting removes them for good.
        </p>
      </div>
    {/if}
  {/if}
</div>

<Modal bind:open={deleteOpen} title="Delete {user?.username}">
  <p>Their passkeys, authenticator, Group memberships and sessions are removed. This cannot be undone.</p>
  <div class="field">
    <label for="dc">Type the Username to confirm</label>
    <input id="dc" bind:value={deleteConfirm} autocomplete="off" />
  </div>
  {#snippet actions()}
    <button onclick={() => (deleteOpen = false)}>Cancel</button>
    <button class="danger" onclick={remove} disabled={deleteConfirm !== user?.username || busy === 'delete'}>Delete</button>
  {/snippet}
</Modal>
