<script>
  import { onMount } from 'svelte';
  import { app, loadMeta, loadSession, loadTheme, logout, applyTheme } from './lib/state.svelte.js';
  import { onStepUp, onUnauthenticated } from './lib/api.js';
  import { path, link, navigate } from './lib/router.js';
  import StepUp from './components/StepUp.svelte';
  import BrandLogo from './components/BrandLogo.svelte';
  import Setup from './pages/Setup.svelte';
  import Login from './pages/Login.svelte';
  import Dashboard from './pages/Dashboard.svelte';
  import Hub from './pages/Hub.svelte';
  import { get } from './lib/api.js';
  import Containers from './pages/Containers.svelte';
  import Stacks from './pages/Stacks.svelte';
  import StackDetail from './pages/StackDetail.svelte';
  import Storage from './pages/Storage.svelte';
  import Routes from './pages/Routes.svelte';
  import Domains from './pages/Domains.svelte';
  import DNS from './pages/DNS.svelte';
  import Config from './pages/Config.svelte';
  import Audit from './pages/Audit.svelte';
  import Settings from './pages/Settings.svelte';
  import Account from './pages/Account.svelte';

  const pages = [
    { path: '/hub', label: 'Hub', icon: '⌂', component: Hub },
    { path: '/dashboard', label: 'Dashboard', icon: '◉', component: Dashboard },
    { section: 'Workloads' },
    { path: '/containers', label: 'Containers', icon: '▣', component: Containers },
    { path: '/stacks', label: 'Stacks', icon: '☰', component: Stacks },
    { path: '/storage', label: 'Volumes & networks', icon: '⛁', component: Storage },
    { section: 'Edge' },
    { path: '/routes', label: 'Routes', icon: '⇄', component: Routes },
    { path: '/domains', label: 'Domains & certificates', icon: '🔒', component: Domains },
    { path: '/dns', label: 'DNS profiles', icon: '⌘', component: DNS },
    { section: 'System' },
    { path: '/config', label: 'Configuration', icon: '⚙', component: Config },
    { path: '/audit', label: 'Audit log', icon: '☷', component: Audit },
    { path: '/settings', label: 'Settings', icon: '✦', component: Settings },
    { path: '/account', label: 'Account', icon: '☺', component: Account },
  ];

  // "/" shows the page chosen in Settings → Home page. /stacks/<name> is the
  // page of one stack (highlighted as Stacks in the menu).
  let current = $derived(
    $path.startsWith('/stacks/')
      ? { path: $path, nav: '/stacks', label: 'Stacks', component: StackDetail }
      : pages.find((p) => p.path === ($path === '/' ? (app.home === 'dashboard' ? '/dashboard' : '/hub') : $path)) || pages[0],
  );

  $effect(() => {
    if (app.session && !app.home) {
      get('/api/settings')
        .then((s) => (app.home = s.home_page || 'hub'))
        .catch(() => (app.home = 'hub'));
    }
  });

  onMount(async () => {
    onStepUp(() => new Promise((resolve) => (app.stepUp = resolve)));
    onUnauthenticated(() => (app.session = null));
    await loadTheme();
    try {
      await loadMeta();
      if (app.meta.setup_complete) await loadSession();
    } finally {
      app.loading = false;
    }
  });

  async function doLogout() {
    await logout();
    navigate('/', { replace: true });
  }

  function toggleTheme() {
    applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
  }
</script>

{#if app.loading}
  <div class="center"><p class="muted">Loading…</p></div>
{:else if !app.meta}
  <div class="center"><div class="alert error">Cannot reach the Marineris API.</div></div>
{:else if !app.meta.setup_complete && !app.session}
  {#if app.meta.listener === 'local'}
    <Setup />
  {:else}
    <div class="center auth-bg">
      <div class="card auth-card stack">
        <BrandLogo />
        <h2>Setup pending</h2>
        <p>
          This server has not been set up yet. For security, the first-run setup is only available on the local listener.
          Open an SSH tunnel to the server and continue there:
        </p>
        <pre>ssh -L 8081:127.0.0.1:8081 you@your-server
# then open http://localhost:8081</pre>
      </div>
    </div>
  {/if}
{:else if !app.session}
  <Login />
{:else}
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <img src={app.meta.brand?.logo || '/branding/icon-192.png'} alt="" />
        {#if app.meta.brand?.custom_name}<span class="brand-name">{app.meta.brand.name}</span>{:else}<span class="wordmark" role="img" aria-label="Marineris"></span>{/if}
      </div>
      <nav class="nav">
        {#each pages as p (p.path || p.section)}
          {#if p.section}
            <div class="section">{p.section}</div>
          {:else}
            <a href={p.path} use:link class:active={(current.nav || current.path) === p.path}><span aria-hidden="true">{p.icon}</span>{p.label}</a>
          {/if}
        {/each}
      </nav>
    </aside>
    <main class="main">
      <div class="topbar">
        {#if current.path === '/hub'}<span></span>{:else}<h1>{current.label}</h1>{/if}
        <div class="row">
          {#if app.meta.listener === 'local'}<span class="badge warn" title="Connected through the loopback listener">break-glass</span>{/if}
          <span class="muted small">{app.session.user.username}</span>
          <button class="ghost small" onclick={toggleTheme} aria-label="Toggle theme" title="Light / dark">☀ / ☾</button>
          <button class="small" onclick={doLogout}>Log out</button>
        </div>
      </div>
      {#key current.path}
        <current.component />
      {/key}
    </main>
  </div>
{/if}

<StepUp />

<div class="toasts" aria-live="polite">
  {#each app.toasts as t (t.id)}
    <div class="toast {t.kind}">{t.message}</div>
  {/each}
</div>
