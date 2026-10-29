<script>
  // Every User: display name, Username, e-mail, Groups, state and Owner.
  // Administrators only (the API refuses everyone else).
  import { onMount } from 'svelte';
  import { get, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';

  let users = $state([]);
  let error = $state('');
  let loaded = $state(false);

  onMount(async () => {
    try {
      users = await get('/api/users');
    } catch (err) {
      error = describe(err);
    } finally {
      loaded = true;
    }
  });

  const stateLook = { active: 'ok', disabled: 'error' };
</script>

<div class="card">
  <h2>Users</h2>
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
