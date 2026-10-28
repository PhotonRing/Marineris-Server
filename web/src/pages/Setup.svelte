<script>
  import BrandLogo from '../components/BrandLogo.svelte';
  import { post, describe } from '../lib/api.js';
  import { app, setSession, loadMeta } from '../lib/state.svelte.js';

  let step = $state(1);
  let error = $state('');
  let busy = $state(false);
  let token = $state('');
  let username = $state('admin');
  let enrol = $state(null);
  let password = $state('');
  let password2 = $state('');
  let email = $state('');
  let uiHost = $state('');
  let code = $state('');
  let result = $state(null);
  let saved = $state(false);

  async function begin(e) {
    e.preventDefault();
    error = '';
    busy = true;
    try {
      enrol = await post('/api/setup/begin', { setup_token: token.trim(), username: username.trim() });
      step = 2;
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }

  async function finish(e) {
    e.preventDefault();
    error = '';
    if (password !== password2) {
      error = 'The passwords do not match.';
      return;
    }
    busy = true;
    try {
      result = await post('/api/setup/finish', {
        setup_token: token.trim(),
        username: username.trim(),
        password,
        totp_code: code.trim(),
        acme_email: email.trim(),
        ui_host: uiHost.trim(),
      });
      step = 3;
    } catch (err) {
      error = describe(err);
    } finally {
      busy = false;
    }
  }

  async function done() {
    setSession(result);
    await loadMeta();
  }
</script>

<div class="center auth-bg">
  <div class="card auth-card stack">
    <BrandLogo />
    <h2>First-run setup · step {step} of 3</h2>
    {#if error}<div class="alert error" role="alert">{error}</div>{/if}

    {#if step === 1}
      <p class="muted small">
        The setup token is printed in the server logs (<code>docker logs marineris</code>) and stored in
        <code>data/secrets/setup-token</code>. It proves you have access to the server.
      </p>
      <form onsubmit={begin}>
        <div class="field">
          <label for="token">Setup token</label>
          <input id="token" bind:value={token} required autocomplete="off" spellcheck="false" />
        </div>
        <div class="field">
          <label for="user">Administrator username</label>
          <input id="user" bind:value={username} required minlength="3" maxlength="32" autocomplete="username" />
          <div class="help">3–32 characters: lowercase letters, digits, “.”, “_” or “-”.</div>
        </div>
        <button class="primary w-full" type="submit" disabled={busy}>Continue</button>
      </form>
    {:else if step === 2}
      <p class="small">Scan this code with an authenticator app (Aegis, 2FAS, Google Authenticator, 1Password…).</p>
      {#if enrol.qr}<img class="qr" src={enrol.qr} alt="TOTP QR code" />{/if}
      <details class="small">
        <summary>Can't scan? Enter the key manually</summary>
        <code class="mono">{enrol.totp_secret}</code>
      </details>
      <form onsubmit={finish}>
        <div class="field">
          <label for="pw">Password</label>
          <input id="pw" type="password" bind:value={password} required minlength="12" autocomplete="new-password" />
          <div class="help">At least 12 characters. A long passphrase is best.</div>
        </div>
        <div class="field">
          <label for="pw2">Repeat password</label>
          <input id="pw2" type="password" bind:value={password2} required minlength="12" autocomplete="new-password" />
        </div>
        <div class="field">
          <label for="email">E-mail for Let's Encrypt</label>
          <input id="email" type="email" bind:value={email} required />
          <div class="help">Used for certificate expiry notices.</div>
        </div>
        <div class="field">
          <label for="host">Public hostname of this UI (optional)</label>
          <input id="host" bind:value={uiHost} placeholder="marineris.example.com" />
          <div class="help">Leave empty to use Marineris only through the SSH tunnel for now. It must belong to a domain you add later.</div>
        </div>
        <div class="field">
          <label for="code">Code from the authenticator</label>
          <input id="code" inputmode="numeric" autocomplete="one-time-code" bind:value={code} required maxlength="7" />
        </div>
        <button class="primary w-full" type="submit" disabled={busy}>Create administrator</button>
      </form>
    {:else}
      <div class="alert ok">The administrator account is ready.</div>
      <p>
        <strong>Recovery codes.</strong> Each code works once if you lose your authenticator. Store them somewhere safe —
        they will not be shown again.
      </p>
      <pre class="codes">{result.recovery_codes.join('\n')}</pre>
      <label class="check"><input type="checkbox" bind:checked={saved} /> I have stored my recovery codes</label>
      <button class="primary w-full" disabled={!saved} onclick={done}>Open Marineris</button>
    {/if}
  </div>
</div>
