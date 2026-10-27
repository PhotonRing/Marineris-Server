<script>
  import { onMount, onDestroy } from 'svelte';
  import { get, describe } from '../lib/api.js';
  import { link } from '../lib/router.js';

  let hub = $state(null);
  let stats = $state(null);
  let error = $state('');
  let search = $state('');
  let now = $state(Date.now());
  let offsetMs = 0; // server clock minus browser clock
  let statsTimer;
  let clockTimer;

  let tiles = $derived(
    (hub?.tiles || []).filter((t) => {
      const q = search.trim().toLowerCase();
      return !q || t.title.toLowerCase().includes(q) || t.subtitle.toLowerCase().includes(q) || t.url.includes(q);
    }),
  );
  let widgets = $derived(new Set(hub?.widgets || []));

  async function loadStats() {
    if (document.hidden) return;
    try {
      stats = await get('/api/system/stats');
      offsetMs = new Date(stats.time).getTime() - Date.now();
    } catch {
      stats = null;
    }
  }

  onMount(async () => {
    try {
      hub = await get('/api/hub');
    } catch (err) {
      error = describe(err);
      return;
    }
    if (hub.widgets.length) {
      await loadStats();
      statsTimer = setInterval(loadStats, 3000);
    }
    clockTimer = setInterval(() => (now = Date.now()), 1000);
  });

  onDestroy(() => {
    clearInterval(statsTimer);
    clearInterval(clockTimer);
  });

  // Server time, shown in the server's time zone.
  function serverClock(n) {
    if (!stats) return { time: '', date: '' };
    const d = new Date(n + offsetMs + stats.utc_offset * 1000);
    const pad = (x) => String(x).padStart(2, '0');
    return {
      time: `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}:${pad(d.getUTCSeconds())}`,
      date: d.toLocaleDateString(undefined, { timeZone: 'UTC', weekday: 'long', day: 'numeric', month: 'long' }),
    };
  }
  let clock = $derived(serverClock(now));

  function bytes(n) {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
    return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${u[i]}`;
  }
  const pct = (a, b) => (b ? Math.round((a / b) * 100) : 0);
  function uptime(s) {
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    return d ? `${d} d ${h} h` : `${h} h ${m} min`;
  }
  function level(p) {
    return p >= 90 ? 'error' : p >= 75 ? 'warn' : 'ok';
  }
  function initials(title) {
    return title
      .split(/\s+/)
      .map((w) => w[0])
      .join('')
      .slice(0, 2)
      .toUpperCase();
  }
</script>

<div class="hub" class:hub-bg={hub?.background}>
  {#if error}
    <div class="alert error">{error}</div>
  {:else if hub}
    <div class="spread hub-head">
      <div>
        <h2 class="hub-title">{hub.title || 'Marineris'}</h2>
        {#if widgets.has('clock') && stats}
          <div class="muted">{clock.date}{stats.hostname ? ` · ${stats.hostname}` : ''}</div>
        {/if}
      </div>
      {#if widgets.has('clock') && stats}
        <div class="hub-clock" title={stats.timezone}>{clock.time}</div>
      {/if}
    </div>

    {#if stats && widgets.size}
      <div class="widgets">
        {#if widgets.has('cpu')}
          <div class="widget">
            <div class="w-label">CPU <span class="muted">· {stats.cpus} cores</span></div>
            <div class="w-value">{Math.round(stats.cpu_percent)} %</div>
            <div class="bar"><span class="bar-fill {level(stats.cpu_percent)}" style:width={`${stats.cpu_percent}%`}></span></div>
            <div class="w-sub muted">load {stats.load1.toFixed(2)}</div>
          </div>
        {/if}
        {#if widgets.has('memory')}
          {@const p = pct(stats.mem_used, stats.mem_total)}
          <div class="widget">
            <div class="w-label">Memory</div>
            <div class="w-value">{p} %</div>
            <div class="bar"><span class="bar-fill {level(p)}" style:width={`${p}%`}></span></div>
            <div class="w-sub muted">{bytes(stats.mem_used)} / {bytes(stats.mem_total)}</div>
          </div>
        {/if}
        {#if widgets.has('disk')}
          {@const p = pct(stats.disk_used, stats.disk_total)}
          <div class="widget">
            <div class="w-label">Disk</div>
            <div class="w-value">{p} %</div>
            <div class="bar"><span class="bar-fill {level(p)}" style:width={`${p}%`}></span></div>
            <div class="w-sub muted">{bytes(stats.disk_used)} / {bytes(stats.disk_total)}</div>
          </div>
        {/if}
        {#if widgets.has('network')}
          <div class="widget">
            <div class="w-label">Network{stats.net_host ? '' : ' (container)'}</div>
            <div class="w-value net"><span>↓ {bytes(stats.net_rx_rate)}/s</span><span>↑ {bytes(stats.net_tx_rate)}/s</span></div>
          </div>
        {/if}
        {#if widgets.has('uptime')}
          <div class="widget">
            <div class="w-label">Uptime</div>
            <div class="w-value">{uptime(stats.uptime_seconds)}</div>
          </div>
        {/if}
      </div>
    {/if}

    <input class="hub-search" type="search" placeholder="Search apps…" bind:value={search} aria-label="Search apps" />

    {#if !hub.tiles.length}
      <div class="alert">No apps yet. Publish one on the <a href="/routes" use:link>Routes</a> page: it appears here.</div>
    {:else}
      <div class="tiles">
        {#each tiles as t (t.id)}
          <a class="tile" href={t.url} target="_blank" rel="noopener noreferrer">
            {#if t.icon}
              <img class="tile-icon" src={t.icon} alt="" loading="lazy" />
            {:else}
              <span class="tile-icon tile-initials" aria-hidden="true">{initials(t.title)}</span>
            {/if}
            <span class="tile-text">
              <span class="tile-title">{t.title}{#if t.auth === 'authelia'}<span class="tile-lock" title="Protected by Authelia">🔒</span>{/if}</span>
              {#if t.subtitle}<span class="tile-sub muted">{t.subtitle}</span>{/if}
            </span>
          </a>
        {/each}
      </div>
    {/if}
  {:else}
    <p class="muted">Loading…</p>
  {/if}
</div>
