<script>
  import BrandLogo from '../components/BrandLogo.svelte';
  import { post, describe } from '../lib/api.js';
  import { setSession, passkeyLogin } from '../lib/state.svelte.js';
  import { passkeysSupported } from '../lib/webauthn.js';

  let username = $state('');
  let password = $state('');
  let code = $state('');
  let mfaToken = $state('');
  let useRecovery = $state(false);
  let error = $state('');
  let busy = $state(false);

  async function submitPassword(e) {
    e.preventDefault();
    error = '';
    busy = true;
    try {
      const res = await post('/api/auth/login', { username: username.trim(), password });
      mfaToken = res.mfa_token;
      password = '';
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }

  async function submitCode(e) {
    e.preventDefault();
    error = '';
    busy = true;
    try {
      const body = { mfa_token: mfaToken };
      if (useRecovery) body.recovery_code = code.trim();
      else body.code = code.trim();
      setSession(await post('/api/auth/login/mfa', body));
    } catch (err) {
      error = describe(err);
      if (err.code === 'mfa_expired') mfaToken = '';
      code = '';
    } finally {
      busy = false;
    }
  }

  async function withPasskey() {
    error = '';
    busy = true;
    try {
      await passkeyLogin();
    } catch (err) {
      error = err?.name === 'NotAllowedError' ? 'The passkey prompt was cancelled.' : describe(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="center auth-bg">
  <div class="card auth-card stack">
    <BrandLogo />
    {#if error}<div class="alert error" role="alert">{error}</div>{/if}

    {#if !mfaToken}
      <form onsubmit={submitPassword}>
        <div class="field">
          <label for="u">Username</label>
          <input id="u" bind:value={username} autocomplete="username webauthn" required />
        </div>
        <div class="field">
          <label for="p">Password</label>
          <input id="p" type="password" bind:value={password} autocomplete="current-password" required />
        </div>
        <button class="primary w-full" type="submit" disabled={busy}>Continue</button>
      </form>
      {#if passkeysSupported()}
        <div class="divider">or</div>
        <button class="w-full" onclick={withPasskey} disabled={busy}>🔑 Sign in with a passkey</button>
      {/if}
    {:else}
      <form onsubmit={submitCode}>
        <div class="field">
          <label for="c">{useRecovery ? 'Recovery code' : 'Authenticator code'}</label>
          <!-- svelte-ignore a11y_autofocus -->
          <input id="c" bind:value={code} autocomplete="one-time-code" inputmode={useRecovery ? 'text' : 'numeric'} required autofocus />
        </div>
        <button class="primary w-full" type="submit" disabled={busy}>Sign in</button>
      </form>
      <button class="ghost small" onclick={() => (useRecovery = !useRecovery)}>
        {useRecovery ? 'Use the authenticator app instead' : 'Lost your authenticator? Use a recovery code'}
      </button>
    {/if}
  </div>
</div>
