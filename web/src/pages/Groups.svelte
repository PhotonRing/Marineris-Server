<script>
  // Groups: create, rename and delete custom Groups, add and remove members.
  // admins and proxy-admins are built-in: listed here, their members changed
  // by the Owner only.
  // Only the Owner acts on Administrators and on the Owner; the controls
  // follow that rule, the API enforces it (mayActOn).
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';
  import { app, toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  let groups = $state([]);
  let users = $state([]);
  let error = $state('');
  let loaded = $state(false);
  let newName = $state('');
  let createError = $state('');
  let busy = $state('');
  let renaming = $state(null); // the Group being renamed
  let renameOpen = $state(false);
  let renameTo = $state('');
  let renameError = $state('');
  let adding = $state({}); // group name -> chosen User ID

  let viewerIsOwner = $derived(!!app.session?.rights?.owner);
  let myID = $derived(app.session?.user?.id);

  // Same rule as the API: an Administrator does not act on another
  // Administrator or on the Owner.
  function mayAct(u) {
    return viewerIsOwner || u.id === myID || (!u.owner && !u.groups.includes('admins'));
  }

  // The built-in Groups' members are the Owner's (with step-up); only
  // Administrators join proxy-admins, and the Owner stays in admins.
  function editable(g) {
    return !g.builtin || viewerIsOwner;
  }

  function candidates(g) {
    const inGroup = new Set(g.members.map((m) => m.id));
    return users.filter(
      (u) => !inGroup.has(u.id) && mayAct(u) && (g.name !== 'proxy-admins' || u.groups.includes('admins')),
    );
  }

  function memberMayBeRemoved(g, m) {
    const u = users.find((x) => x.id === m.id);
    if (!u || !editable(g)) return false;
    if (g.name === 'admins' && (u.owner || g.members.length === 1)) return false;
    return mayAct(u);
  }

  async function load() {
    try {
      [groups, users] = await Promise.all([get('/api/groups'), get('/api/users')]);
    } catch (err) {
      error = describe(err);
    } finally {
      loaded = true;
    }
  }
  onMount(load);

  async function create(e) {
    e.preventDefault();
    busy = 'create';
    createError = '';
    try {
      const g = await post('/api/groups', { name: newName.trim() });
      toast(`Group ${g.name} created`);
      newName = '';
      await load();
    } catch (err) {
      createError = describe(err);
    } finally {
      busy = '';
    }
  }

  function startRename(g) {
    renaming = g;
    renameTo = g.name;
    renameError = '';
    renameOpen = true;
  }

  async function rename(e) {
    e.preventDefault();
    busy = 'rename';
    renameError = '';
    try {
      await put(`/api/groups/${encodeURIComponent(renaming.name)}`, { name: renameTo.trim() });
      toast(`Group renamed to ${renameTo.trim()}`);
      renameOpen = false;
      await load();
    } catch (err) {
      renameError = describe(err);
    } finally {
      busy = '';
    }
  }

  async function remove(g) {
    const n = g.members.length;
    if (!confirm(`Delete the Group ${g.name}? Its ${n} member${n === 1 ? '' : 's'} stay, without this Group.`)) return;
    busy = 'delete:' + g.name;
    try {
      await del(`/api/groups/${encodeURIComponent(g.name)}`);
      toast(`Group ${g.name} deleted`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }

  async function member(g, id, add) {
    const u = users.find((x) => x.id === Number(id));
    if (!add && g.name === 'admins' && u?.groups.includes('proxy-admins') &&
      !confirm(`${u.username} also stops being a Proxy & SSO Administrator. Continue?`)) return;
    busy = 'member:' + g.name;
    try {
      const path = `/api/groups/${encodeURIComponent(g.name)}/members/${id}`;
      if (add) await put(path);
      else await del(path);
      adding[g.name] = '';
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }
</script>

<div class="stack">
  <div class="card">
    <h2>Groups</h2>
    {#if error}<div class="alert error">{error}</div>{/if}
    <p class="small muted">
      A User may be in several Groups. Changes reach Authelia at once. The built-in <code>admins</code> and
      <code>proxy-admins</code> are managed by the Owner.
    </p>
    <form onsubmit={create} class="row">
      <input bind:value={newName} placeholder="new-group" aria-label="New Group name" maxlength="32" required />
      <button class="primary" type="submit" disabled={busy === 'create'}>Create</button>
    </form>
    <p class="small muted">2 to 32 characters: lowercase letters, digits, <code>-</code> or <code>_</code>.</p>
    {#if createError}<div class="alert error">{createError}</div>{/if}
  </div>

  {#each groups as g (g.name)}
    <div class="card">
      <div class="spread">
        <h2>
          {g.name}
          {#if g.builtin}<span class="badge">built-in</span>{/if}
        </h2>
        {#if !g.builtin}
          <div class="row">
            <button onclick={() => startRename(g)}>Rename</button>
            <button class="danger" onclick={() => remove(g)} disabled={busy === 'delete:' + g.name}>Delete</button>
          </div>
        {/if}
      </div>
      <div class="row">
        {#each g.members as m (m.id)}
          <span class="badge">
            <a href="/users/{m.id}" use:link>{m.display_name || m.username}</a>
            {#if memberMayBeRemoved(g, m)}
              <button
                class="ghost small"
                aria-label="Remove {m.username} from {g.name}"
                onclick={() => member(g, m.id, false)}
                disabled={busy === 'member:' + g.name}>×</button
              >
            {/if}
          </span>
        {:else}
          <span class="small muted">No members.</span>
        {/each}
      </div>
      {#if !editable(g)}
        <p class="small muted">Managed by the Owner.</p>
      {:else if candidates(g).length}
        <div class="row">
          <select bind:value={adding[g.name]} aria-label="User to add to {g.name}">
            <option value="">Add a member…</option>
            {#each candidates(g) as u (u.id)}<option value={u.id}>{u.display_name} ({u.username})</option>{/each}
          </select>
          <button onclick={() => member(g, adding[g.name], true)} disabled={!adding[g.name] || busy === 'member:' + g.name}>Add</button>
        </div>
      {/if}
    </div>
  {:else}
    <div class="card"><p class="muted">{loaded ? 'No Groups.' : 'Loading…'}</p></div>
  {/each}
</div>

<Modal bind:open={renameOpen} title="Rename {renaming?.name}">
  <form id="rename" onsubmit={rename} class="stack">
    {#if renameError}<div class="alert error">{renameError}</div>{/if}
    <div class="field">
      <label for="rn">New name</label>
      <input id="rn" bind:value={renameTo} maxlength="32" required />
    </div>
  </form>
  {#snippet actions()}
    <button onclick={() => (renameOpen = false)}>Cancel</button>
    <button class="primary" type="submit" form="rename" disabled={busy === 'rename'}>Rename</button>
  {/snippet}
</Modal>
