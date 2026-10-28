<script>
  import { onMount } from 'svelte';
  import { get, post, del, describe } from '../lib/api.js';
  import { app, toast, registerPasskey } from '../lib/state.svelte.js';
  import { passkeysSupported } from '../lib/webauthn.js';
  import Modal from '../components/Modal.svelte';

  let passkeys = $state([]);
  let passkeyName = $state('');
  let current = $state('');
  let next = $state('');
  let next2 = $state('');
  let codes = $state(null);
  let codesOpen = $state(false);
  let totp = $state(null);
  let totpCode = $state('');
  let totpOpen = $state(false);
  let notification = $state(null);

  async function load() {
    try {
      passkeys = await get('/api/account/passkeys');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  onMount(load);

  async function addPasskey() {
    try {
      await registerPasskey(passkeyName.trim() || 'Passkey');
      passkeyName = '';
      toast('Passkey added');
      await load();
    } catch (err) {
      toast(err?.name === 'NotAllowedError' ? 'Passkey creation was cancelled' : describe(err), 'error');
    }
  }

  async function removePasskey(p) {
    if (!confirm(`Remove passkey “${p.name}”?`)) return;
    try {
      await del(`/api/account/passkeys/${p.id}`);
      toast('Passkey removed');
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function changePassword(e) {
    e.preventDefault();
    if (next !== next2) return toast('The new passwords do not match', 'error');
    try {
      await post('/api/account/password', { current_password: current, new_password: next });
      current = next = next2 = '';
      toast('Password changed. Other sessions were logged out.');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function newCodes() {
    if (!confirm('Generate new recovery codes? The old ones stop working.')) return;
    try {
      codes = (await post('/api/account/recovery-codes')).recovery_codes;
      codesOpen = true;
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function startTOTP() {
    try {
      totp = await post('/api/account/totp/begin');
      totpCode = '';
      totpOpen = true;
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function finishTOTP(e) {
    e.preventDefault();
    try {
      await post('/api/account/totp/finish', { code: totpCode.trim() });
      totpOpen = false;
      toast('Authenticator replaced');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function revokeOthers() {
    try {
      await post('/api/account/sessions/revoke-others');
      toast('All other sessions were logged out');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function loadNotification() {
    try {
      notification = await get('/api/authelia/notification');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }
</script>

<div class="stack">
  <div class="card">
    <h2>Passkeys</h2>
    {#if app.meta.portal_url}
      <p class="small">
        To sign in with a passkey (Face ID, Touch ID, Windows Hello or a security key) to every app, the Authelia portal and
        this interface, add it in the
        <a href={`${app.meta.portal_url}/settings/two-factor-authentication`} target="_blank" rel="noopener noreferrer">Authelia portal</a>:
        it asks for your authenticator code to confirm.
      </p>
      <p class="small muted">
        The passkeys below only confirm sensitive actions on {app.meta.ui_host}. Passkeys for the break-glass listener are added
        there (localhost, through the SSH tunnel).
      </p>
    {:else}
      <p class="small muted">
        Sign in without a password using Face ID, Touch ID, Windows Hello or a security key. Passkeys are tied to the address
        you register them on ({app.meta.listener === 'local' ? 'localhost, through the SSH tunnel' : app.meta.ui_host}).
      </p>
    {/if}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Name</th><th>Added</th><th>Last used</th><th></th></tr></thead>
        <tbody>
          {#each passkeys as p (p.id)}
            <tr>
              <td>{p.name}</td>
              <td class="small">{new Date(p.created_at).toLocaleString()}</td>
              <td class="small">{p.last_used_at ? new Date(p.last_used_at).toLocaleString() : 'never'}</td>
              <td class="right"><button class="small danger" onclick={() => removePasskey(p)}>Remove</button></td>
            </tr>
          {:else}
            <tr><td colspan="4" class="muted">No passkeys yet.</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if passkeysSupported()}
      <div class="row mt">
        <input class="w-full" bind:value={passkeyName} placeholder="Name, e.g. “MacBook Touch ID”" maxlength="64" />
        <button class="primary" onclick={addPasskey}>Add passkey</button>
      </div>
    {:else}
      <p class="small muted">This browser or address cannot use passkeys (use https or http://localhost).</p>
    {/if}
  </div>

  <div class="card">
    <h2>Password</h2>
    <form onsubmit={changePassword}>
      <div class="grid">
        <div class="field"><label for="cp">Current password</label><input id="cp" type="password" autocomplete="current-password" bind:value={current} required /></div>
        <div class="field"><label for="np">New password</label><input id="np" type="password" autocomplete="new-password" minlength="12" bind:value={next} required /></div>
        <div class="field"><label for="np2">Repeat</label><input id="np2" type="password" autocomplete="new-password" minlength="12" bind:value={next2} required /></div>
      </div>
      <button type="submit">Change password</button>
    </form>
    <p class="small muted mt">The same password is used for the Authelia portal.</p>
  </div>

  <div class="card">
    <h2>Two-factor authentication</h2>
    <div class="row">
      <button onclick={startTOTP}>Replace authenticator app</button>
      <button onclick={newCodes}>New recovery codes</button>
      <button onclick={revokeOthers}>Log out other sessions</button>
    </div>
  </div>

  <div class="card">
    <h2>Authelia messages</h2>
    <p class="small muted">
      Your password and authenticator app also sign you in to the Authelia portal: Marineris registers them there.
      For some actions in the portal, such as adding a passkey, Authelia asks for a one-time code that it would
      e-mail; Marineris shows that message here.
    </p>
    <button onclick={loadNotification}>Show latest message</button>
    {#if notification}
      {#if notification.present}
        <p class="small muted mt">Written {new Date(notification.modified_at).toLocaleString()}</p>
        <pre>{notification.content}</pre>
      {:else}
        <p class="muted mt">No message yet.</p>
      {/if}
    {/if}
  </div>
</div>

<Modal bind:open={codesOpen} title="New recovery codes">
  <p>Store them safely. Each works once. They will not be shown again.</p>
  <pre class="codes">{codes?.join('\n')}</pre>
</Modal>

<Modal bind:open={totpOpen} title="Replace authenticator">
  {#if totp}
    <img class="qr" src={totp.qr} alt="TOTP QR code" />
    <p class="small muted mono">{totp.totp_secret}</p>
    <form id="totp-form" onsubmit={finishTOTP}>
      <div class="field">
        <label for="tc">Code from the new authenticator</label>
        <input id="tc" inputmode="numeric" bind:value={totpCode} required maxlength="7" />
      </div>
    </form>
  {/if}
  {#snippet actions()}
    <button class="primary" type="submit" form="totp-form">Confirm</button>
  {/snippet}
</Modal>
