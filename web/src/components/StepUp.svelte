<script>
  import Modal from './Modal.svelte';
  import { app, passkeyLogin } from '../lib/state.svelte.js';
  import { post, describe } from '../lib/api.js';
  import { passkeysSupported } from '../lib/webauthn.js';

  let password = $state('');
  let code = $state('');
  let error = $state('');
  let busy = $state(false);
  let open = $state(false);

  $effect(() => {
    open = !!app.stepUp;
  });

  function finish(ok) {
    const resolve = app.stepUp;
    app.stepUp = null;
    password = '';
    code = '';
    error = '';
    resolve?.(ok);
  }

  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await post('/api/auth/reauth', { password, code });
      finish(true);
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }

  async function withPasskey() {
    busy = true;
    error = '';
    try {
      await passkeyLogin();
      finish(true);
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }
</script>

<Modal bind:open title="Confirm it's you" onclose={() => app.stepUp && finish(false)}>
  <p class="muted">This action is sensitive. Confirm your identity to continue.</p>
  {#if error}<div class="alert error">{error}</div>{/if}
  <form onsubmit={submit}>
    <div class="field">
      <label for="su-pw">Password</label>
      <input id="su-pw" type="password" autocomplete="current-password" bind:value={password} required />
    </div>
    <div class="field">
      <label for="su-code">Authenticator code</label>
      <input id="su-code" inputmode="numeric" autocomplete="one-time-code" bind:value={code} required maxlength="7" />
    </div>
    <div class="row end">
      {#if passkeysSupported()}
        <button type="button" onclick={withPasskey} disabled={busy}>Use a passkey</button>
      {/if}
      <button class="primary" type="submit" disabled={busy}>Confirm</button>
    </div>
  </form>
</Modal>
