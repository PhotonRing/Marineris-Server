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
  import NotAllowed from './pages/NotAllowed.svelte';
  import House from '@lucide/svelte/icons/house';
  import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';
  import Box from '@lucide/svelte/icons/box';
  import Layers from '@lucide/svelte/icons/layers';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Route from '@lucide/svelte/icons/route';
  import GlobeLock from '@lucide/svelte/icons/globe-lock';
  import Network from '@lucide/svelte/icons/network';
  import FileCog from '@lucide/svelte/icons/file-cog';
  import ScrollText from '@lucide/svelte/icons/scroll-text';
  import Settings2 from '@lucide/svelte/icons/settings';
  import CircleUserRound from '@lucide/svelte/icons/circle-user-round';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';

  // Icons: Lucide (ISC licence), drawn in the text colour. Pages marked
  // admin are for Administrators only (the API refuses everyone else too).
  const pages = [
    { path: '/hub', label: 'Hub', icon: House, component: Hub },
    { path: '/dashboard', label: 'Dashboard', icon: LayoutDashboard, component: Dashboard, admin: true },
    { section: 'Workloads', admin: true },
    { path: '/containers', label: 'Containers', icon: Box, component: Containers, admin: true },
    { path: '/stacks', label: 'Stacks', icon: Layers, component: Stacks, admin: true },
    { path: '/storage', label: 'Volumes & networks', icon: HardDrive, component: Storage, admin: true },
    { section: 'Edge', admin: true },
    { path: '/routes', label: 'Routes', icon: Route, component: Routes, admin: true },
    { path: '/domains', label: 'Domains & certificates', icon: GlobeLock, component: Domains, admin: true },
    { path: '/dns', label: 'DNS profiles', icon: Network, component: DNS, admin: true },
    { section: 'System', admin: true },
    { path: '/proxy', label: 'Proxy & SSO', icon: FileCog, component: Config, admin: true },
    { path: '/audit', label: 'Audit log', icon: ScrollText, component: Audit, admin: true },
    { path: '/settings', label: 'Settings', icon: Settings2, component: Settings, admin: true },
    { path: '/account', label: 'Account', icon: CircleUserRound, component: Account },
  ];

  let isAdmin = $derived(!!app.session?.rights?.administrator);
  let navPages = $derived(pages.filter((p) => isAdmin || !p.admin));

  // "/" shows the page chosen in Settings → Home page (always the Hub for
  // non-Administrators). /stacks/<name> is the page of one stack
  // (highlighted as Stacks in the menu). An admin page opened by someone
  // else shows that they may not open it.
  let current = $derived.by(() => {
    const page = $path.startsWith('/stacks/')
      ? { path: $path, nav: '/stacks', label: 'Stacks', component: StackDetail, admin: true }
      : pages.find((p) => p.path === ($path === '/' ? (isAdmin && app.home === 'dashboard' ? '/dashboard' : '/hub') : $path)) || pages[0];
    return page.admin && !isAdmin ? { path: page.path, label: 'Not allowed', component: NotAllowed } : page;
  });

  $effect(() => {
    // Only Administrators may read the settings; everyone else's home is the Hub.
    if (app.session && !isAdmin) app.home = 'hub';
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
        {#each navPages as p (p.path || p.section)}
          {#if p.section}
            <div class="section">{p.section}</div>
          {:else}
            <a href={p.path} use:link class:active={(current.nav || current.path) === p.path}><p.icon size={18} aria-hidden="true" />{p.label}</a>
          {/if}
        {/each}
      </nav>
    </aside>
    <main class="main">
      <div class="topbar">
        {#if current.path === '/hub'}<span></span>{:else}<h1>{current.label}</h1>{/if}
        <div class="row">
          {#if app.meta.listener === 'local'}<span class="badge warn" title="Connected through the loopback listener">break-glass</span>{/if}
          <span class="muted small row user-chip"><CircleUserRound size={16} aria-hidden="true" />{app.session.user.username}</span>
          <button class="ghost small" onclick={toggleTheme} aria-label="Toggle theme" title="Light / dark"><Sun size={16} aria-hidden="true" /><Moon size={16} aria-hidden="true" /></button>
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
