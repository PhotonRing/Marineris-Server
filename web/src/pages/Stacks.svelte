<script>
  import { onMount } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';

  const template = `services:
  app:
    image: nginx:1.27-alpine
    restart: unless-stopped
    # Bind mounts must stay inside the stack folder or an allowed root (/srv, /mnt, /media).
    volumes:
      - ./html:/usr/share/nginx/html:ro
`;

  let list = $state([]);
  let error = $state('');
  let editorOpen = $state(false);
  let editing = $state(null); // existing stack name or null for new
  let name = $state('');
  let compose = $state('');
  let envText = $state('');
  let unsafeAck = $state(false);
  let check = $state(null);
  let busy = $state(false);
  let deleteOpen = $state(false);
  let deleteName = $state('');
  let deleteConfirm = $state('');
  let deleteVolumes = $state(false);

  async function load() {
    try {
      list = await get('/api/stacks');
    } catch (err) {
      error = describe(err);
    }
  }
  onMount(load);

  function newStack() {
    editing = null;
    name = '';
    compose = template;
    envText = '';
    unsafeAck = false;
    check = null;
    editorOpen = true;
  }

  async function edit(s) {
    try {
      const full = await get(`/api/stacks/${encodeURIComponent(s.name)}`);
      editing = full.name;
      name = full.name;
      compose = full.compose;
      envText = full.env;
      unsafeAck = full.unsafe_ack;
      check = null;
      editorOpen = true;
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function runCheck() {
    try {
      check = await post('/api/stacks/check', { name: name || 'check', compose, env: envText, unsafe_ack: unsafeAck });
    } catch (err) {
      check = { error: describe(err) };
    }
  }

  async function save(deploy) {
    busy = true;
    try {
      const body = { name, compose, env: envText, unsafe_ack: unsafeAck };
      if (editing) await put(`/api/stacks/${encodeURIComponent(editing)}`, body);
      else await post('/api/stacks', body);
      editing = name;
      toast(`Stack ${name} saved`);
      if (deploy) {
        await post(`/api/stacks/${encodeURIComponent(name)}/deploy`);
        toast(`Stack ${name} deployed`);
        editorOpen = false;
      }
      await load();
    } catch (err) {
      toast(describe(err), 'error');
      await runCheck();
    } finally {
      busy = false;
    }
  }

  async function action(s, what) {
    try {
      await post(`/api/stacks/${encodeURIComponent(s.name)}/${what}`);
      toast(`${s.name}: ${what === 'down' ? 'stopped' : 'deployed'}`);
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  function askDelete(s) {
    deleteName = s.name;
    deleteConfirm = '';
    deleteVolumes = false;
    deleteOpen = true;
  }

  async function doDelete() {
    try {
      await del(`/api/stacks/${encodeURIComponent(deleteName)}?volumes=${deleteVolumes}`);
      toast(`Stack ${deleteName} deleted`);
      deleteOpen = false;
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }
</script>

<div class="stack">
  <div class="spread">
    <p class="muted small">Docker Compose stacks managed by Marineris. Every stack is checked against the security policy before it is deployed.</p>
    <button class="primary" onclick={newStack}>New stack</button>
  </div>
  {#if error}<div class="alert error">{error}</div>{/if}
  <div class="card table-wrap">
    <table>
      <thead><tr><th>Name</th><th>Containers</th><th>Deployed</th><th></th></tr></thead>
      <tbody>
        {#each list as s (s.name)}
          <tr>
            <td><strong>{s.name}</strong> {#if s.unsafe_ack}<span class="badge warn">unsafe allowed</span>{/if}</td>
            <td>{s.running} / {s.containers} running</td>
            <td class="small muted">{s.deployed_at ? new Date(s.deployed_at).toLocaleString() : 'never'}</td>
            <td>
              <div class="row end">
                <button class="small" onclick={() => edit(s)}>Edit</button>
                <button class="small" onclick={() => action(s, 'deploy')}>Deploy</button>
                <button class="small" onclick={() => action(s, 'down')}>Stop</button>
                <button class="small danger" onclick={() => askDelete(s)}>Delete</button>
              </div>
            </td>
          </tr>
        {:else}
          <tr><td colspan="4" class="muted">No stacks yet. Import a docker-compose file with “New stack”.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal bind:open={editorOpen} title={editing ? `Edit stack ${editing}` : 'New stack'} wide>
  <div class="field">
    <label for="sn">Name</label>
    <input id="sn" bind:value={name} disabled={!!editing} placeholder="media" />
  </div>
  <div class="field">
    <label for="sc">compose.yaml</label>
    <textarea id="sc" class="code" bind:value={compose} spellcheck="false"></textarea>
  </div>
  <div class="field">
    <label for="se">.env <span class="muted small">(encrypted at rest; used for ${'{'}VARIABLE{'}'} interpolation)</span></label>
    <textarea id="se" class="code" bind:value={envText} spellcheck="false" placeholder="TZ=Europe/Paris"></textarea>
  </div>
  <label class="check"><input type="checkbox" bind:checked={unsafeAck} /> Allow unsafe settings (privileged, host namespaces, dangerous capabilities, devices) — requires confirming your identity</label>
  {#if check}
    {#if check.error}
      <div class="alert error">{check.error}</div>
    {:else if check.violations.length === 0}
      <div class="alert ok">No policy findings.</div>
    {:else}
      <div class="alert {check.blocking.length ? 'error' : 'warning'}">
        {#each check.violations as v}
          <div>• {v.service ? v.service + ': ' : ''}{v.message} {v.overridable ? '(can be allowed)' : '(never allowed)'}</div>
        {/each}
      </div>
    {/if}
  {/if}
  {#snippet actions()}
    <button onclick={runCheck}>Check policy</button>
    <button onclick={() => save(false)} disabled={busy || !name}>Save</button>
    <button class="primary" onclick={() => save(true)} disabled={busy || !name}>Save & deploy</button>
  {/snippet}
</Modal>

<Modal bind:open={deleteOpen} title="Delete stack">
  <p>This stops and removes the containers of <strong>{deleteName}</strong> and deletes its files.</p>
  <label class="check"><input type="checkbox" bind:checked={deleteVolumes} /> Also delete its named volumes (data loss)</label>
  <div class="field">
    <label for="dc">Type the stack name to confirm</label>
    <input id="dc" bind:value={deleteConfirm} autocomplete="off" />
  </div>
  {#snippet actions()}
    <button onclick={() => (deleteOpen = false)}>Cancel</button>
    <button class="danger" disabled={deleteConfirm !== deleteName} onclick={doDelete}>Delete</button>
  {/snippet}
</Modal>
