<script>
  import { onMount } from 'svelte';
  import { get, post, del, describe } from '../lib/api.js';
  import { app, toast, registerPasskey } from '../lib/state.svelte.js';
  import { passkeysSupported } from '../lib/webauthn.js';
  import Modal from '../components/Modal.svelte';

  let passkeys = $state([]);
  let portalPasskeys = $state([]);
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

  const portalHost = $derived(app.meta.portal_url ? new URL(app.meta.portal_url).host : '');

  async function load() {
    try {
      passkeys = await get('/api/account/passkeys');
      if (app.meta.portal_url) portalPasskeys = await get('/api/account/authelia-passkeys');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  onMount(() => {
    // Back from the page that created an Authelia passkey.
    const q = new URLSearchParams(location.search);
    if (q.get('passkey') === 'added') {
      toast('Passkey added');
      history.replaceState(history.state, '', location.pathname);
    }
    load();
  });

  async function addPortalPasskey(e) {
    e.preventDefault();
    try {
      const { url } = await post('/api/account/authelia-passkeys', { name: passkeyName.trim() || 'Passkey' });
      location.assign(url);
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  async function removePortalPasskey(p) {
    if (!confirm(`Remove passkey “${p.name}”? Also delete it from your password manager or device.`)) return;
    try {
      await del(`/api/account/authelia-passkeys/${p.id}`);
      toast('Passkey removed');
      await load();
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

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
      <!-- Behind Authelia, its sign-in is the sign-in: a passkey that signs in
           (to every app, the portal and this interface) is Authelia's. It is
           created on the portal's address, where it belongs: this page sends
           the browser there for a moment, then back. -->
      <p class="small">
        Sign in without a password, with Face ID, Touch ID, Windows Hello, a password manager or a security key, to every
        app, the Authelia portal and this interface.
      </p>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Name</th><th>Added</th><th>Last used</th><th></th></tr></thead>
          <tbody>
            {#each portalPasskeys as p (p.id)}
              <tr>
                <td>{p.name}{#if p.host !== portalHost}<span class="small muted"> · {p.host}</span>{/if}</td>
                <td class="small">{new Date(p.created_at).toLocaleString()}</td>
                <td class="small">{p.last_used_at ? new Date(p.last_used_at).toLocaleString() : 'never'}</td>
                <td class="right"><button class="small danger" onclick={() => removePortalPasskey(p)}>Remove</button></td>
              </tr>
            {:else}
              <tr><td colspan="4" class="muted">No passkeys yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <form class="row mt" onsubmit={addPortalPasskey}>
        <!-- A label, not a credential: password managers must not offer to fill it. -->
        <input class="w-full" bind:value={passkeyName} placeholder="Name, e.g. “MacBook Touch ID”" maxlength="64"
          autocomplete="off" data-protonpass-ignore="true" data-1p-ignore data-bwignore data-lpignore="true" />
        <button class="primary" type="submit">Add passkey</button>
      </form>
      <p class="small muted mt">The passkey is created on {portalHost}, the address of the sign-in page.</p>
      {#if passkeys.length}
        <div class="alert warning mt">
          The passkeys below were added to this interface only. They cannot sign you in, and a password manager may still offer
          them on the sign-in page, where they fail. Remove them, here and in your password manager.
        </div>
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
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {:else}
      <p class="small muted">
        Sign in without a password using Face ID, Touch ID, Windows Hello or a security key. Passkeys are tied to the address
        you register them on ({app.meta.listener === 'local' ? 'localhost, through the SSH tunnel' : app.meta.ui_host}).
      </p>
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
          <!-- A label, not a credential: password managers must not offer to fill it. -->
          <input class="w-full" bind:value={passkeyName} placeholder="Name, e.g. “MacBook Touch ID”" maxlength="64"
            autocomplete="off" data-protonpass-ignore="true" data-1p-ignore data-bwignore data-lpignore="true" />
          <button class="primary" onclick={addPasskey}>Add passkey</button>
        </div>
      {:else}
        <p class="small muted">This browser or address cannot use passkeys (use https or http://localhost).</p>
      {/if}
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
      For some actions in the portal, Authelia asks for a one-time code that it would e-mail; Marineris shows that
      message here. Passkeys added on this page need none.
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
        <!-- The new authenticator's code: a saved one would be the old one. -->
        <input id="tc" inputmode="numeric" bind:value={totpCode} required maxlength="7"
          autocomplete="off" data-protonpass-ignore="true" data-1p-ignore data-bwignore data-lpignore="true" />
      </div>
    </form>
  {/if}
  {#snippet actions()}
    <button class="primary" type="submit" form="totp-form">Confirm</button>
  {/snippet}
</Modal>
