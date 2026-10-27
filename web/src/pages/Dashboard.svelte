<script>
  import { onMount } from 'svelte';
  import { get, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';

  let data = $state(null);
  let error = $state('');

  onMount(async () => {
    try {
      data = await get('/api/overview');
    } catch (err) {
      error = describe(err);
    }
  });
</script>

{#if error}
  <div class="alert error">{error}</div>
{:else if !data}
  <p class="muted">Loading…</p>
{:else}
  <div class="stack">
    {#if data.alerts.length}
      <div class="stack">
        {#each data.alerts as a}
          <div class="alert {a.level === 'error' ? 'error' : a.level === 'warning' ? 'warning' : ''}">
            {a.message}
            {#if a.link}<a href={a.link} use:link> → open</a>{/if}
          </div>
        {/each}
      </div>
    {/if}
    <div class="grid">
      <a class="card stat" href="/containers" use:link>
        <span class="value">{data.containers.running}<span class="muted small"> / {data.containers.total}</span></span>
        <span class="label">containers running</span>
      </a>
      <a class="card stat" href="/stacks" use:link>
        <span class="value">{data.stacks}</span>
        <span class="label">stacks</span>
      </a>
      <a class="card stat" href="/routes" use:link>
        <span class="value">{data.routes.enabled}<span class="muted small"> / {data.routes.total}</span></span>
        <span class="label">routes enabled</span>
      </a>
      <a class="card stat" href="/domains" use:link>
        <span class="value">{data.domains}</span>
        <span class="label">domains</span>
      </a>
      <a class="card stat" href="/dns" use:link>
        <span class="value">{data.dns_profiles}</span>
        <span class="label">DNS profiles</span>
      </a>
      <div class="card stat">
        <span class="value">{data.acme_staging ? 'Staging' : 'Production'}</span>
        <span class="label">Let's Encrypt CA</span>
      </div>
    </div>
    <p class="muted small">Marineris {data.version} · Docker {data.docker_ok ? 'reachable' : 'unreachable'}</p>
  </div>
{/if}
