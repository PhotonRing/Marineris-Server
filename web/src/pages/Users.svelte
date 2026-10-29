<script>
  // Every User: display name, Username, e-mail, Groups, state and Owner; the
  // pending Invites, which become Users once accepted. Administrators only
  // (the API refuses everyone else).
  import UserPlus from '@lucide/svelte/icons/user-plus';
  import { onMount } from 'svelte';
  import { get, post, del, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';
  import LinkDialog from '../components/LinkDialog.svelte';

  let users = $state([]);
  let links = $state([]);
  let error = $state('');
  let loaded = $state(false);

  let inviteOpen = $state(false);
  let form = $state({ username: '', display_name: '', email: '' });
  let chosen = $state([]);
  let groupNames = $state([]); // every custom Group
  let formError = $state('');
  let busy = $state(false);
  let created = $state(null); // the Invite just created, with its link
  let linkOpen = $state(false);

  let invites = $derived(links.filter((l) => l.kind === 'invite'));

  async function load() {
    try {
      [users, links] = await Promise.all([get('/api/users'), get('/api/links')]);
    } catch (err) {
      error = describe(err);
    } finally {
      loaded = true;
    }
  }

  onMount(load);

  async function openInvite() {
    form = { username: '', display_name: '', email: '' };
    chosen = [];
    formError = '';
    inviteOpen = true;
    try {
      groupNames = (await get('/api/groups')).filter((g) => !g.builtin).map((g) => g.name);
    } catch (err) {
      formError = describe(err);
    }
  }

  function toggle(g) {
    chosen = chosen.includes(g) ? chosen.filter((x) => x !== g) : [...chosen, g];
  }

  async function createInvite(e) {
    e.preventDefault();
    busy = true;
    formError = '';
    try {
      created = await post('/api/invites', { ...form, groups: chosen });
      inviteOpen = false;
      linkOpen = true;
      await load();
    } catch (err) {
      formError = describe(err);
    } finally {
      busy = false;
    }
  }

  async function revoke(l) {
    if (!confirm(`Revoke the Invite for ${l.username}? Its link stops working.`)) return;
    try {
      await del(`/api/links/${l.id}`);
      toast(`Invite for ${l.username} revoked`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  const stateLook = { active: 'ok', disabled: 'error' };
  const when = (t) => new Date(t).toLocaleString();
</script>

<div class="stack">
  <div class="card">
    <div class="spread">
      <h2>Users</h2>
      <button class="primary" type="button" onclick={openInvite}><UserPlus size={14} aria-hidden="true" />Invite someone</button>
    </div>
    {#if error}<div class="alert error">{error}</div>{/if}
    <p class="small muted">
      What a User may do comes from their Groups: members of <code>admins</code> are Administrators. Only the Owner manages
      Administrators.
    </p>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Name</th><th>Username</th><th>E-mail</th><th>Groups</th><th>State</th></tr></thead>
        <tbody>
          {#each users as u (u.id)}
            <tr>
              <td>
                <a href="/users/{u.id}" use:link>{u.display_name}</a>
                {#if u.owner}<span class="badge ok">Owner</span>{/if}
              </td>
              <td class="mono">{u.username}</td>
              <td class="small">{u.email || '—'}</td>
              <td>
                {#each u.groups as g (g)}<span class="badge">{g}</span> {:else}<span class="muted">—</span>{/each}
              </td>
              <td><span class="badge {stateLook[u.state] || ''}">{u.state}</span></td>
            </tr>
          {:else}
            <tr><td colspan="5" class="muted">{loaded ? 'No Users.' : 'Loading…'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>

  {#if invites.length}
    <div class="card">
      <h2>Pending Invites</h2>
      <p class="small muted">
        Each Username is reserved until its Invite is accepted, expires or is revoked. A link is shown only once: to send it
        again, revoke the Invite and create a new one.
      </p>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Name</th><th>Username</th><th>Groups</th><th>Created by</th><th>Expires</th><th></th></tr></thead>
          <tbody>
            {#each invites as l (l.id)}
              <tr>
                <td>{l.display_name}</td>
                <td class="mono">{l.username}</td>
                <td>{#each l.groups as g (g)}<span class="badge">{g}</span> {:else}<span class="muted">—</span>{/each}</td>
                <td class="small">{l.created_by}</td>
                <td class="small">{when(l.expires_at)}</td>
                <td><button class="ghost small" type="button" onclick={() => revoke(l)}>Revoke</button></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</div>

<Modal bind:open={inviteOpen} title="Invite someone">
  <form id="invite-form" onsubmit={createInvite} class="stack">
    {#if formError}<div class="alert error">{formError}</div>{/if}
    <div class="field">
      <label for="iv-un">Username</label>
      <input
        id="iv-un"
        bind:value={form.username}
        required
        minlength="2"
        maxlength="32"
        pattern="[a-z0-9][a-z0-9._\-]+"
        autocomplete="off"
      />
      <div class="help">2 to 32 lowercase letters, digits, '.', '_' or '-'. It never changes.</div>
    </div>
    <div class="field">
      <label for="iv-dn">Display name</label>
      <input id="iv-dn" bind:value={form.display_name} maxlength="64" placeholder="the Username" />
    </div>
    <div class="field">
      <label for="iv-em">E-mail</label>
      <input id="iv-em" type="email" bind:value={form.email} placeholder="optional" />
    </div>
    <div class="field">
      <span class="label">Groups</span>
      {#each groupNames as g (g)}
        <label class="check"><input type="checkbox" checked={chosen.includes(g)} onchange={() => toggle(g)} /> {g}</label>
      {:else}
        <p class="small muted">No custom Groups yet.</p>
      {/each}
      <div class="help">Only the Owner makes someone an Administrator, once their User exists.</div>
    </div>
  </form>
  {#snippet actions()}
    <button type="button" onclick={() => (inviteOpen = false)}>Cancel</button>
    <button class="primary" type="submit" form="invite-form" disabled={busy}>Create the Invite</button>
  {/snippet}
</Modal>

<LinkDialog bind:open={linkOpen} title="Invite created" url={created?.url || ''}>
  {#if created}
    <p>
      Send this link to <strong>{created.display_name}</strong>. It opens on the sign-in portal, where they choose a password
      and set up an authenticator app. It works once, until {when(created.expires_at)}.
    </p>
  {/if}
</LinkDialog>
