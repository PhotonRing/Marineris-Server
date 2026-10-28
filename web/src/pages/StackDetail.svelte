<script>
  import ArrowLeft from '@lucide/svelte/icons/arrow-left';
  import Star from '@lucide/svelte/icons/star';
  // One stack: its services with their status and actions, and per-service
  // tabs to read logs and edit the service (written back to compose.yaml).
  import { onMount, onDestroy } from 'svelte';
  import { get, post, put, del, describe } from '../lib/api.js';
  import { path, navigate, link } from '../lib/router.js';
  import { toast } from '../lib/state.svelte.js';
  import Modal from '../components/Modal.svelte';
  import AppIcon from '../components/AppIcon.svelte';
  import StateBadge from '../components/StateBadge.svelte';
  import ListEditor from '../components/ListEditor.svelte';
  import EnvEditor from '../components/EnvEditor.svelte';
  import MountsEditor from '../components/MountsEditor.svelte';

  const name = decodeURIComponent($path.split('/')[2] || '');
  const base = `/api/stacks/${encodeURIComponent(name)}`;
  const restartPolicies = ['', 'no', 'always', 'unless-stopped', 'on-failure'];
  const tabs = [
    ['overview', 'Overview'],
    ['logs', 'Logs'],
    ['env', 'Environment'],
    ['mounts', 'Mounts'],
    ['network', 'Network'],
    ['labels', 'Labels'],
    ['icon', 'Icon'],
    ['terminal', 'Terminal'],
  ];

  let summary = $state(null);
  let stack = $state(null);
  let error = $state('');
  let selected = $state(''); // service name; '' = the compose file
  let tab = $state('overview');
  let form = $state(null);
  let saved = $state(''); // JSON of the form as loaded, to detect changes
  let busy = $state('');
  let networkNames = $state([]);
  let dockerVolumes = $state([]);

  // Labels Marineris reads, offered when typing a label name.
  const labelSuggestions = [
    { key: 'io.marineris.main', values: ['true', 'false'], help: 'Main service: its icon represents the stack.' },
    { key: 'io.marineris.icon', placeholder: 'jellyfin or https://…/icon.png', help: 'Icon: a dashboard-icons name or an https URL, downloaded at the next deployment.' },
  ];
  let timer;

  // Compose editor (whole stack).
  let compose = $state('');
  let envText = $state('');
  let unsafeAck = $state(false);
  let check = $state(null);

  // Logs.
  let logs = $state('');
  let logsFrom = $state('');
  let follow = $state(false);
  let logsTimer;

  // Icon.
  let iconMode = $state('name');
  let iconName = $state('');
  let iconURL = $state('');
  let iconData = $state('');

  // Delete.
  let deleteOpen = $state(false);
  let deleteConfirm = $state('');
  let deleteVolumes = $state(false);

  let svc = $derived(summary?.services.find((s) => s.name === selected));
  let dirty = $derived(!!form && JSON.stringify(form) !== saved);
  let long = $derived(new Set(svc?.long_syntax || []));

  async function loadSummary() {
    try {
      summary = await get(`${base}/services`);
      error = '';
    } catch (err) {
      error = describe(err);
    }
  }

  async function loadStack() {
    stack = await get(base);
    compose = stack.compose;
    envText = stack.env;
    unsafeAck = stack.unsafe_ack;
  }

  onMount(async () => {
    try {
      await loadStack();
    } catch (err) {
      error = describe(err);
      return;
    }
    await loadSummary();
    if (summary) pick(summary.main || summary.services[0]?.name || '');
    get('/api/networks')
      .then((ns) => (networkNames = ns.map((n) => n.name)))
      .catch(() => {});
    get('/api/volumes')
      .then((vs) => (dockerVolumes = vs.filter((v) => !v.system && v.stack !== name).map((v) => v.name)))
      .catch(() => {});
    timer = setInterval(() => !document.hidden && loadSummary(), 5000);
  });

  onDestroy(() => {
    clearInterval(timer);
    clearInterval(logsTimer);
  });

  function resetForm() {
    if (!svc) {
      form = null;
      return;
    }
    form = {
      image: svc.image,
      restart: svc.restart,
      environment: svc.environment.map((v) => ({ key: v.key, value: v.value, unset: !!v.unset })),
      mounts: svc.mounts.map((m) => ({ ...m, external: false, newVolume: false })),
      labels: svc.labels.map((v) => ({ key: v.key, value: v.value, unset: !!v.unset })),
      ports: [...svc.ports],
      networks: [...svc.networks],
    };
    saved = JSON.stringify(form);
  }

  function pick(service) {
    selected = service;
    if (!service) {
      tab = 'compose';
      check = null;
    } else if (tab === 'compose') {
      tab = 'overview';
    }
    resetForm();
    iconName = summary?.services.find((s) => s.name === service)?.image.split('/').pop().split(':')[0] || '';
    logs = '';
    if (tab === 'logs') loadLogs();
  }

  function openTab(t) {
    tab = t;
    if (t === 'logs') loadLogs();
  }

  async function loadLogs() {
    const c = svc?.containers.find((x) => x.name === logsFrom) || svc?.containers[0];
    if (!c) {
      logs = 'This service has no container yet: deploy the stack.';
      return;
    }
    logsFrom = c.name;
    try {
      logs = (await get(`/api/containers/${encodeURIComponent(c.name)}/logs?tail=500`)).logs || '(no output)';
    } catch (err) {
      logs = describe(err);
    }
  }

  $effect(() => {
    clearInterval(logsTimer);
    if (follow && tab === 'logs') logsTimer = setInterval(() => !document.hidden && loadLogs(), 3000);
  });

  async function run(key, fn, done) {
    busy = key;
    try {
      await fn();
      if (done) toast(done);
      await loadSummary();
    } catch (err) {
      toast(describe(err), 'error');
    } finally {
      busy = '';
    }
  }

  const stackAction = (action, done) => run(action, () => post(`${base}/${action}`), done);
  const serviceAction = (action, done) => run(action, () => post(`${base}/services/${encodeURIComponent(selected)}/${action}`), done);

  async function saveService(deploy) {
    const body = { deploy };
    for (const k of ['image', 'restart', 'environment', 'ports', 'networks', 'labels']) {
      if (!long.has(k)) body[k] = $state.snapshot(form[k]);
    }
    if (!long.has('volumes')) {
      body.mounts = form.mounts.map((m) => ({ type: m.type, source: m.source.trim(), target: m.target.trim(), read_only: m.read_only, external: !!m.external }));
    }
    await run(
      'save',
      async () => {
        const res = await put(`${base}/services/${encodeURIComponent(selected)}`, body);
        compose = res.compose;
        stack = { ...stack, compose: res.compose };
      },
      deploy ? `${selected} saved and recreated` : `${selected} saved — recreate it to apply`,
    );
    resetForm();
  }

  async function runCheck() {
    try {
      check = await post('/api/stacks/check', { name, compose, env: envText, unsafe_ack: unsafeAck });
    } catch (err) {
      check = { error: describe(err) };
    }
  }

  async function saveCompose(deploy) {
    busy = 'compose';
    try {
      await put(base, { name, compose, env: envText, unsafe_ack: unsafeAck });
      toast(`Stack ${name} saved`);
      if (deploy) {
        await post(`${base}/deploy`);
        toast(`Stack ${name} deployed`);
      }
      await loadStack();
      await loadSummary();
      check = null;
    } catch (err) {
      toast(describe(err), 'error');
      await runCheck();
    } finally {
      busy = '';
    }
  }

  function pickIconFile(e) {
    const f = e.currentTarget.files?.[0];
    if (!f) return;
    if (f.size > 512 * 1024) {
      toast('Icons are limited to 512 KB.', 'error');
      return;
    }
    const reader = new FileReader();
    reader.onload = () => (iconData = String(reader.result).split(',')[1] || '');
    reader.readAsDataURL(f);
  }

  function saveIcon() {
    const url = `${base}/icons/${encodeURIComponent(selected)}`;
    let body = null;
    if (iconMode === 'name' && iconName.trim()) body = { name: iconName.trim() };
    if (iconMode === 'url' && iconURL.trim()) body = { url: iconURL.trim() };
    if (iconMode === 'upload' && iconData) body = { data: iconData };
    if (!body) return;
    run('icon', () => put(url, body), 'Icon updated');
  }

  async function doDelete() {
    try {
      await del(`${base}?volumes=${deleteVolumes}`);
      toast(`Stack ${name} deleted`);
      deleteOpen = false;
      navigate('/stacks');
    } catch (err) {
      toast(describe(err), 'error');
    }
  }

  const running = (s) => s?.containers.some((c) => c.state === 'running');
</script>

<div class="stack">
  <a href="/stacks" use:link class="small row tight"><ArrowLeft size={14} aria-hidden="true" />All stacks</a>
  {#if error}<div class="alert error">{error}</div>{/if}

  {#if stack && summary}
    <div class="card spread">
      <div class="row">
        <AppIcon src={summary.icon} {name} size={52} />
        <div>
          <h2 class="no-margin">{name}</h2>
          <div class="row small">
            <StateBadge state={summary.state} />
            <span class="muted">{summary.services.length} service{summary.services.length === 1 ? '' : 's'} · deployed {stack.deployed_at ? new Date(stack.deployed_at).toLocaleString() : 'never'}</span>
            {#if stack.unsafe_ack}<span class="badge warn">unsafe allowed</span>{/if}
          </div>
        </div>
      </div>
      <div class="row">
        <button class="primary" disabled={!!busy} onclick={() => stackAction('deploy', `${name} deployed`)} title="Create or update the containers from the saved compose file">{busy === 'deploy' ? 'Deploying…' : 'Deploy'}</button>
        <button disabled={!!busy} onclick={() => stackAction('pull', `${name} updated`)} title="Pull newer images and recreate every service">{busy === 'pull' ? 'Updating…' : 'Update images'}</button>
        <button disabled={!!busy || summary.state === 'not_deployed'} onclick={() => stackAction('restart', `${name} restarted`)}>Restart</button>
        <button disabled={!!busy || summary.state === 'not_deployed'} onclick={() => stackAction('down', `${name} stopped`)} title="Stop and remove the containers (volumes are kept)">Stop</button>
        <button class="danger" onclick={() => ((deleteOpen = true), (deleteConfirm = ''), (deleteVolumes = false))}>Delete</button>
      </div>
    </div>

    <div class="stack-layout">
      <nav class="card service-list" aria-label="Services">
        {#each summary.services as s (s.name)}
          <button class="service-item" class:active={selected === s.name} onclick={() => pick(s.name)}>
            <AppIcon src={s.icon} name={s.name} size={32} />
            <span class="service-text">
              <span class="service-name">{s.name}{#if s.main}<span class="main-star" title="Main service: its icon is the stack's"><Star size={13} aria-label="main service" /></span>{/if}</span>
              <span class="muted small service-image">{s.image}</span>
            </span>
            <span class="dot {s.state === 'running' ? 'ok' : s.state === 'not_deployed' ? '' : s.state === 'stopped' || s.state === 'unhealthy' ? 'error' : 'warn'}" title={s.state}></span>
          </button>
        {/each}
        <button class="service-item" class:active={selected === ''} onclick={() => pick('')}>
          <span class="app-icon tile-initials file-icon" aria-hidden="true">{'{ }'}</span>
          <span class="service-text"><span class="service-name">compose.yaml</span><span class="muted small">whole stack and .env</span></span>
        </button>
      </nav>

      <section class="card service-panel">
        {#if selected === ''}
          <h3>compose.yaml</h3>
          <div class="field">
            <label for="sc">compose.yaml</label>
            <textarea id="sc" class="code tall" bind:value={compose} spellcheck="false"></textarea>
          </div>
          <div class="field">
            <label for="se">.env <span class="muted small">(encrypted at rest; used for ${'{'}VARIABLE{'}'} interpolation)</span></label>
            <textarea id="se" class="code" bind:value={envText} spellcheck="false" placeholder="TZ=Europe/Paris"></textarea>
          </div>
          <label class="check"><input type="checkbox" bind:checked={unsafeAck} /> Allow unsafe settings (privileged, host namespaces, dangerous capabilities, devices) — requires confirming your identity</label>
          <div class="help small muted mt">Labels: <code>io.marineris.main: "true"</code> picks the service whose icon represents the stack; <code>io.marineris.icon: jellyfin</code> (a <a href="https://dashboardicons.com" target="_blank" rel="noopener noreferrer">dashboard-icons</a> name or an https URL) sets a service's icon.</div>
          {#if check}
            {#if check.error}
              <div class="alert error mt">{check.error}</div>
            {:else if check.violations.length === 0}
              <div class="alert ok mt">No policy findings.</div>
            {:else}
              <div class="alert mt {check.blocking.length ? 'error' : 'warning'}">
                {#each check.violations as v, i (i)}
                  <div>• {v.service ? v.service + ': ' : ''}{v.message} {v.overridable ? '(can be allowed)' : '(never allowed)'}</div>
                {/each}
              </div>
            {/if}
          {/if}
          <div class="row end mt">
            <button onclick={runCheck}>Check policy</button>
            <button onclick={() => saveCompose(false)} disabled={!!busy}>Save</button>
            <button class="primary" onclick={() => saveCompose(true)} disabled={!!busy}>{busy === 'compose' ? 'Deploying…' : 'Save & deploy'}</button>
          </div>
        {:else if svc && form}
          <div class="spread">
            <div class="row">
              <AppIcon src={svc.icon} name={svc.name} size={40} />
              <div>
                <h3 class="no-margin">{svc.name}</h3>
                <div class="row small"><StateBadge state={svc.state} /><span class="muted mono">{svc.image}</span></div>
              </div>
            </div>
            <div class="row">
              {#if running(svc)}
                <button class="small" disabled={!!busy} onclick={() => serviceAction('stop', `${selected} stopped`)}>Stop</button>
              {:else}
                <button class="small" disabled={!!busy || !svc.containers.length} onclick={() => serviceAction('start', `${selected} started`)}>Start</button>
              {/if}
              <button class="small" disabled={!!busy || !svc.containers.length} onclick={() => serviceAction('restart', `${selected} restarted`)}>Restart</button>
              <button class="small" disabled={!!busy} onclick={() => serviceAction('recreate', `${selected} recreated`)} title="Recreate the container from the saved definition">{busy === 'recreate' ? 'Recreating…' : 'Recreate'}</button>
              <button class="small" disabled={!!busy} onclick={() => serviceAction('update', `${selected} updated`)} title="Pull a newer image and recreate">{busy === 'update' ? 'Updating…' : 'Update image'}</button>
            </div>
          </div>

          <div class="tabs mt" role="tablist">
            {#each tabs as [id, label] (id)}
              <button role="tab" aria-selected={tab === id} class:active={tab === id} onclick={() => openTab(id)}>{label}</button>
            {/each}
          </div>

          {#if tab === 'overview'}
            <div class="grid">
              <div class="field">
                <label for="img">Image</label>
                <input id="img" class="mono" bind:value={form.image} disabled={svc.build} />
              </div>
              <div class="field">
                <label for="rp">Restart policy</label>
                <select id="rp" bind:value={form.restart}>
                  {#each restartPolicies as p (p)}<option value={p}>{p || '(not set: no)'}</option>{/each}
                </select>
              </div>
            </div>
            <h4>Containers</h4>
            {#if svc.containers.length}
              <div class="table-wrap">
                <table>
                  <thead><tr><th>Name</th><th>State</th><th>Ports</th><th>Networks</th></tr></thead>
                  <tbody>
                    {#each svc.containers as c (c.id)}
                      <tr>
                        <td class="mono small">{c.name}</td>
                        <td><span class="badge {c.state === 'running' ? 'ok' : 'error'}">{c.state}</span> <span class="muted small">{c.status}</span>{#if c.health && c.health !== 'none'} <span class="badge {c.health === 'healthy' ? 'ok' : 'warn'}">{c.health}</span>{/if}</td>
                        <td class="mono small">{c.ports.join(', ') || '—'}</td>
                        <td class="small">{c.networks.join(', ')}{#if c.routable} <span class="badge ok" title="Reachable by Caddy: can be published with a route">routable</span>{/if}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else}
              <p class="muted">No container: deploy the stack to create it.</p>
            {/if}
          {:else if tab === 'logs'}
            <div class="row">
              {#if svc.containers.length > 1}
                <select bind:value={logsFrom} onchange={loadLogs} aria-label="Container">
                  {#each svc.containers as c (c.id)}<option value={c.name}>{c.name}</option>{/each}
                </select>
              {/if}
              <button class="small" onclick={loadLogs}>Refresh</button>
              <label class="check small"><input type="checkbox" bind:checked={follow} /> Follow</label>
            </div>
            <pre class="logs mt">{logs || 'Loading…'}</pre>
          {:else if tab === 'env'}
            {#if long.has('environment')}
              <div class="alert warning">The environment is written in a form this editor cannot change: edit it in compose.yaml.</div>
            {/if}
            <p class="muted small">Values are stored in compose.yaml. For secrets, tick “.env” and put the value in the stack's .env (encrypted at rest).</p>
            <EnvEditor bind:vars={form.environment} disabled={long.has('environment')} />
          {:else if tab === 'mounts'}
            {#if long.has('volumes')}
              <div class="alert warning">Some mounts use the long syntax, with options this editor does not know: edit them in compose.yaml.</div>
            {/if}
            <MountsEditor bind:mounts={form.mounts} declared={summary.volumes} {dockerVolumes} bindRoots={summary.bind_roots} disabled={long.has('volumes')} />
          {:else if tab === 'network'}
            <div class="grid">
              <div class="field">
                <span class="label">Networks</span>
                {#if long.has('networks')}<div class="alert warning">Networks use the long syntax: edit them in compose.yaml.</div>{/if}
                <ListEditor bind:items={form.networks} placeholder="default" addLabel="Add network" disabled={long.has('networks')} suggestions={['default', ...networkNames]} />
                <div class="help small muted">To publish the service with a route, attach it to <code>marineris-apps</code> (declared as an external network) or let the Routes page attach it.</div>
              </div>
              <div class="field">
                <span class="label">Published ports</span>
                {#if long.has('ports')}<div class="alert warning">Ports use the long syntax: edit them in compose.yaml.</div>{/if}
                <ListEditor bind:items={form.ports} placeholder="127.0.0.1:8080:80" addLabel="Add port" disabled={long.has('ports')} />
                <div class="help small muted">Usually not needed: routes reach the container directly. A published port bypasses Caddy, Authelia and CrowdSec.</div>
              </div>
            </div>
          {:else if tab === 'labels'}
            {#if long.has('labels')}
              <div class="alert warning">The labels are written in a form this editor cannot change: edit them in compose.yaml.</div>
            {/if}
            <p class="muted small">Docker labels of the service's containers. Start typing <code>io.marineris</code> for the ones Marineris reads.</p>
            <EnvEditor bind:vars={form.labels} unsetOption={false} suggestions={labelSuggestions} keyPlaceholder="io.marineris.main" addLabel="Add label" disabled={long.has('labels')} />
          {:else if tab === 'icon'}
            <div class="row">
              <AppIcon src={svc.icon} name={svc.name} size={64} />
              <div class="muted small">
                {#if svc.icon_source === 'custom'}Custom icon.{:else if svc.icon_source === 'route'}Icon of the route that publishes this service.{:else}No icon yet: initials are shown.{/if}
                {#if svc.main}<div>This is the stack's main service: its icon represents the stack.</div>
                {:else}<div>To use it for the stack, add the label <code>io.marineris.main</code> = <code>true</code> in the Labels tab.</div>{/if}
              </div>
            </div>
            <div class="tabs mt">
              <button class:active={iconMode === 'name'} onclick={() => (iconMode = 'name')}>By name</button>
              <button class:active={iconMode === 'url'} onclick={() => (iconMode = 'url')}>From a URL</button>
              <button class:active={iconMode === 'upload'} onclick={() => (iconMode = 'upload')}>Upload</button>
            </div>
            {#if iconMode === 'name'}
              <div class="field"><label for="in">Icon name (<a href="https://dashboardicons.com" target="_blank" rel="noopener noreferrer">dashboard-icons</a>)</label><input id="in" bind:value={iconName} placeholder="jellyfin" /></div>
            {:else if iconMode === 'url'}
              <div class="field"><label for="iu">Image URL</label><input id="iu" bind:value={iconURL} placeholder="https://…/icon.png" /></div>
            {:else}
              <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/svg+xml,image/x-icon" onchange={pickIconFile} aria-label="Icon file" />
            {/if}
            <p class="muted small">The image is downloaded once by the server and served by Marineris: browsers never load it from elsewhere.</p>
            <div class="row end">
              {#if svc.icon_source === 'custom'}<button onclick={() => run('icon', () => del(`${base}/icons/${encodeURIComponent(selected)}`), 'Icon removed')}>Remove icon</button>{/if}
              <button class="primary" onclick={saveIcon} disabled={!!busy}>Set icon</button>
            </div>
          {:else if tab === 'terminal'}
            <div class="alert">
              <strong>Coming in Phase 3.</strong> A terminal in the browser needs Docker's <code>exec</code> API, which the socket proxy refuses
              today on purpose; it will come with its own audit trail and step-up confirmation. Meanwhile, over SSH:
              <pre class="mt">docker exec -it {svc.containers[0]?.name || `${name}-${svc.name}-1`} sh</pre>
            </div>
          {/if}

          {#if ['overview', 'env', 'mounts', 'network', 'labels'].includes(tab)}
            <div class="row end mt">
              {#if dirty}<span class="muted small">Unsaved changes</span>{/if}
              <button onclick={resetForm} disabled={!dirty || !!busy}>Reset</button>
              <button onclick={() => saveService(false)} disabled={!dirty || !!busy}>Save</button>
              <button class="primary" onclick={() => saveService(true)} disabled={!dirty || !!busy}>{busy === 'save' ? 'Applying…' : 'Save & recreate'}</button>
            </div>
          {/if}
        {/if}
      </section>
    </div>
  {:else if !error}
    <p class="muted">Loading…</p>
  {/if}
</div>

<Modal bind:open={deleteOpen} title="Delete stack">
  <p>This stops and removes the containers of <strong>{name}</strong> and deletes its files.</p>
  <label class="check"><input type="checkbox" bind:checked={deleteVolumes} /> Also delete its named volumes (data loss)</label>
  <div class="field">
    <label for="dc">Type the stack name to confirm</label>
    <input id="dc" bind:value={deleteConfirm} autocomplete="off" />
  </div>
  {#snippet actions()}
    <button onclick={() => (deleteOpen = false)}>Cancel</button>
    <button class="danger" disabled={deleteConfirm !== name} onclick={doDelete}>Delete</button>
  {/snippet}
</Modal>
