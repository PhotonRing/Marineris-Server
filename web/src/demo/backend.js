// In-browser backend of the static demo. It answers the UI's API calls from
// sample data kept in memory (lost on reload), mimicking the real server:
// same response shapes, validation of the main inputs, configuration
// history and audit log. Nothing leaves the browser.
import { seed } from './data.js';

const db = seed();
let nextId = 100;

const ok = (data) => ({ ok: true, status: 200, data });
const fail = (status, code, error, details) => ({ ok: false, status, data: { code, error, ...(details ? { details } : {}) } });
const notFound = () => fail(404, 'not_found', 'not found');
const unauthenticated = () => fail(401, 'unauthenticated', 'not signed in');
const notInDemo = (what) => fail(400, 'demo', `${what} needs a real server: not available in the demo.`);
const iso = () => new Date().toISOString();

function audit(action, target, details = '') {
  db.audit.unshift({ id: nextId++, at: iso(), actor: 'admin', ip: '203.0.113.24', action, target, success: true, details });
}

// Every change goes through "validate, snapshot, apply" on the real server:
// record it in the configuration history.
function applied(reason, components = ['caddy']) {
  for (const component of components) {
    for (const v of db.history) if (v.component === component && v.status === 'applied') v.status = 'superseded';
    db.history.unshift({ id: nextId++, component, created_at: iso(), actor: 'admin', reason, status: 'applied', error: '' });
  }
}

const HOST = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$/;
const inDomain = (host) => db.domains.some((d) => host === d.name || host.endsWith('.' + d.name));

function validateRoute(b, self) {
  const errors = [];
  if (!b.name || !/^[a-z0-9][a-z0-9-]{0,62}$/.test(b.name)) return fail(400, 'invalid', 'name: lowercase letters, digits and "-"', { field: 'name' });
  if (!HOST.test(b.host || '')) return fail(400, 'invalid', 'host: is not a valid hostname', { field: 'host' });
  if (db.routes.some((r) => r.name === b.name && r.id !== self)) return fail(409, 'conflict', `a route named "${b.name}" already exists`);
  if (!inDomain(b.host)) errors.push(`route "${b.name}": host "${b.host}" is not inside a managed domain`);
  if (b.host === db.settings.ui_host) errors.push(`route "${b.name}": host "${b.host}" is the UI host`);
  if (b.kind === 'container') {
    const c = db.containers.find((x) => x.name === b.target?.container);
    if (!c) errors.push(`route "${b.name}": container "${b.target?.container}" does not exist`);
    if (!(b.target?.port > 0 && b.target.port < 65536)) errors.push(`route "${b.name}": port must be between 1 and 65535`);
  }
  if (b.kind === 'external' && !/^https?:\/\/[^/]+/.test(b.target?.url || '')) errors.push(`route "${b.name}": the upstream must be an http(s) URL`);
  if (b.kind === 'external' && /\/\/(127\.|localhost|\[::1\])/.test(b.target?.url || '')) errors.push(`route "${b.name}": the upstream must not target localhost`);
  if (b.kind === 'static' && (!b.target?.folder || b.target.folder.includes('..'))) errors.push(`route "${b.name}": the folder must be inside the static directory`);
  if (errors.length) return fail(400, 'invalid_config', 'the change would produce an invalid configuration', errors);
  return null;
}

function certificates() {
  const hosts = new Set();
  if (db.settings.ui_host) hosts.add(db.settings.ui_host);
  for (const d of db.domains) hosts.add(`${db.settings.auth_subdomain}.${d.name}`);
  for (const r of db.routes) if (r.enabled) hosts.add(r.host);
  const staging = db.settings.acme_staging;
  return [...hosts].sort().map((host) => {
    const d = db.domains.find((x) => host === x.name || host.endsWith('.' + x.name));
    if (!d) return { host, ok: false, days_left: 0, staging: false, wildcard: false, trusted: false, error: 'no certificate served' };
    const wildcard = d.wildcard && d.dns_profile_id && host !== d.name && host.split('.').length === d.name.split('.').length + 1;
    const issued = new Date(Date.now() - 20 * 86400000);
    return {
      host, ok: true,
      issuer: staging ? '(STAGING) Ersatz Edamame E1' : "Let's Encrypt E6",
      dns_names: wildcard ? [d.name, '*.' + d.name] : [host],
      not_before: issued.toISOString(), not_after: new Date(issued.getTime() + 90 * 86400000).toISOString(),
      days_left: 69, staging, wildcard: !!wildcard, trusted: !staging,
    };
  });
}

function overview() {
  const alerts = [];
  if (db.settings.acme_staging) alerts.push({ level: 'warning', message: "Certificates come from the Let's Encrypt staging CA (not trusted by browsers). Switch to production in Settings once everything works.", link: '/settings' });
  for (const p of db.profiles) if (p.last_test_at && !p.last_test_ok) alerts.push({ level: 'error', message: `DNS profile "${p.name}" failed its last test.`, link: '/dns' });
  return {
    acme_staging: db.settings.acme_staging, alerts,
    containers: { running: db.containers.filter((c) => c.state === 'running').length, total: db.containers.length },
    dns_profiles: db.profiles.length, docker_ok: true, domains: db.domains.length,
    routes: { enabled: db.routes.filter((r) => r.enabled).length, total: db.routes.length },
    stacks: db.stacks.length, version: db.meta.version,
  };
}

// Host metrics that move a little, like a real server.
let cpu = 7;
function stats() {
  cpu = Math.min(95, Math.max(2, cpu + (Math.random() - 0.5) * 6));
  const gib = 1024 ** 3;
  return {
    time: iso(), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', utc_offset: -new Date().getTimezoneOffset() * 60,
    hostname: 'mars', cpu_percent: cpu, cpus: 8, load1: +(cpu / 12).toFixed(2),
    mem_total: 32 * gib, mem_used: (9.4 + Math.random() * 0.3) * gib,
    disk_total: 1800 * gib, disk_used: 1135 * gib,
    net_rx_rate: 180000 + Math.random() * 220000, net_tx_rate: 40000 + Math.random() * 60000, net_host: true,
    uptime_seconds: (Date.now() - db.started) / 1000,
  };
}

function hub() {
  return {
    title: db.settings.hub_title, widgets: db.settings.hub_widgets, background: db.settings.hub_background,
    tiles: db.routes
      .filter((r) => r.enabled && r.hub.visible)
      .map((r) => ({ id: r.id, title: r.hub.title || r.name, subtitle: r.hub.subtitle, url: `https://${r.host}${r.path_prefix || ''}/`, auth: r.auth, ...(db.icons.has(r.id) ? { icon: db.icons.get(r.id) } : {}) }))
      .sort((a, b) => a.title.localeCompare(b.title)),
  };
}

function stackCheck(compose, ack) {
  const v = [];
  const service = (compose.match(/^ {2}([a-z0-9_-]+):/m) || [])[1] || 'service';
  const add = (rule, message, overridable) => v.push({ service, rule, message, overridable });
  if (/privileged:\s*true/.test(compose)) add('privileged', 'privileged mode gives the container full control of the host', true);
  if (/docker\.sock/.test(compose)) add('bind_mount', 'mounting the Docker socket is never allowed', false);
  if (/network_mode:\s*["']?host/.test(compose)) add('host_network', "the host network bypasses Marineris' isolation", true);
  if (/pid:\s*["']?host/.test(compose)) add('host_pid', 'the host PID namespace exposes every process of the host', true);
  if (/cap_add:[\s\S]*SYS_ADMIN/.test(compose)) add('capability', 'SYS_ADMIN is almost equivalent to root on the host', true);
  if (/-\s*\/(etc|proc|sys|boot|root)(\/|:)/.test(compose)) add('bind_mount', 'host system folders cannot be mounted', false);
  return { violations: v, blocking: v.filter((x) => !(ack && x.overridable)) };
}

function stackView(s) {
  const own = db.containers.filter((c) => c.stack === s.name);
  return { name: s.name, containers: own.length, running: own.filter((c) => c.state === 'running').length, unsafe_ack: s.unsafe_ack, created_at: s.created_at, updated_at: s.updated_at, deployed_at: s.deployed_at };
}

function containerDetail(c) {
  const volumes = db.volumes.filter((v) => v.stack && v.stack === c.stack && !v.system).map((v) => `${v.name}:/data (volume)`);
  return {
    ...c, command: c.name === 'marineris' ? ['/usr/local/bin/marineris', 'serve'] : [],
    env: ['TZ=••••', 'PATH=••••'],
    labels: { 'com.docker.compose.project': c.stack || '', 'com.docker.compose.service': c.name, ...(c.system ? { 'io.marineris.system': 'true' } : {}) },
    mounts: c.name === 'jellyfin' ? [...volumes, '/srv/media:/media (bind, read-only)'] : volumes,
    restart_policy: 'unless-stopped', exposed_ports: c.ports,
    started_at: new Date(c.created * 1000).toISOString(), exit_code: 0,
  };
}

function logs(c) {
  const t = (s) => new Date(Date.now() - s * 1000).toISOString();
  if (c.name === 'marineris') {
    return [
      `${t(900)} {"level":"INFO","msg":"listening","addr":":8080"}`,
      `${t(600)} {"level":"INFO","msg":"configuration applied","component":"caddy","version":${db.history[0]?.id ?? 1}}`,
      `${t(300)} {"level":"INFO","msg":"certificate status checked","hosts":${certificates().length}}`,
      `${t(5)} {"level":"INFO","msg":"login","user":"admin","method":"passkey"}`,
    ].join('\n');
  }
  return [`${t(3600)} ${c.name}: started`, `${t(1800)} ${c.name}: ready`, `${t(60)} ${c.name}: healthy`].join('\n');
}

const QR = 'data:image/svg+xml,' + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="160" height="160"><rect width="160" height="160" fill="#fff"/><text x="80" y="84" font-family="sans-serif" font-size="13" text-anchor="middle">QR code (demo)</text></svg>');
const code = () => Array.from({ length: 2 }, () => Math.random().toString(36).slice(2, 6)).join('-');

// [method, pattern, handler(params, body, query)]
const routes = [
  ['GET', '/api/health', () => ok({ status: 'ok' })],
  ['GET', '/api/meta', () => ok({ ...db.meta, setup_complete: true })],
  ['POST', '/api/auth/login', (p, b) => (b.username && b.password ? ok({ mfa_required: true, mfa_token: 'demo' }) : fail(401, 'invalid_credentials', 'wrong username or password'))],
  ['POST', '/api/auth/login/mfa', (p, b) => {
    if (!(b.code || b.recovery_code)) return fail(401, 'invalid_code', 'wrong code');
    db.session = { ...db.sessionTemplate, auth_at: iso() };
    audit('login', 'admin', b.recovery_code ? 'recovery code' : 'totp');
    return ok({ user: db.user, csrf_token: 'demo', method: 'totp' });
  }],
  ['POST', '/api/auth/passkey/begin', () => notInDemo('Signing in with a passkey')],
  ['GET', '/api/auth/session', () => (db.session ? ok(db.session) : unauthenticated())],
  ['POST', '/api/auth/logout', () => { db.session = null; return ok({ ok: true }); }],
  ['POST', '/api/auth/reauth', () => ok({ ok: true })],

  ['GET', '/api/account/passkeys', () => ok(db.passkeys)],
  ['POST', '/api/account/passkeys/begin', () => notInDemo('Registering a passkey')],
  ['DELETE', '/api/account/passkeys/{id}', (p) => { db.passkeys = db.passkeys.filter((k) => k.id !== p.id); audit('passkey.delete', 'admin'); return ok({ ok: true }); }],
  ['POST', '/api/account/password', (p, b) => ((b.new_password || b.password || '').length < 12 ? fail(400, 'invalid', 'password: use at least 12 characters', { field: 'new_password' }) : (audit('password.change', 'admin'), ok({ ok: true })))],
  ['POST', '/api/account/recovery-codes', () => { audit('recovery_codes.regenerate', 'admin'); return ok({ recovery_codes: Array.from({ length: 10 }, code) }); }],
  ['POST', '/api/account/totp/begin', () => ok({ totp_secret: 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/Marineris:admin?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Marineris', qr: QR })],
  ['POST', '/api/account/totp/finish', () => { audit('totp.change', 'admin'); return ok({ ok: true }); }],
  ['POST', '/api/account/sessions/revoke-others', () => ok({ ok: true })],

  ['GET', '/api/hub', () => ok(hub())],
  ['GET', '/api/system/stats', () => ok(stats())],
  ['PUT', '/api/routes/{id}/icon', (p, b) => {
    const r = db.routes.find((x) => x.id === +p.id);
    if (!r) return notFound();
    let url = '';
    if (b.name) {
      if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(b.name)) return fail(400, 'bad_request', "icons: an icon name uses lowercase letters, digits and '-'");
      url = `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/${b.name}.png`;
    } else if (b.url) {
      if (!/^https:\/\//.test(b.url)) return fail(400, 'bad_request', 'icons: the icon URL must be an https:// URL');
      url = b.url;
    } else if (b.data) {
      url = `data:${b.data.startsWith('PHN2') || b.data.startsWith('PD94') ? 'image/svg+xml' : 'image/png'};base64,${b.data}`;
    } else return fail(400, 'bad_request', 'give an icon name, url or data');
    db.icons.set(r.id, url);
    r.hub.has_icon = true;
    return ok({ ok: true, content_type: 'image/png' });
  }],
  ['DELETE', '/api/routes/{id}/icon', (p) => { db.icons.delete(+p.id); const r = db.routes.find((x) => x.id === +p.id); if (r) r.hub.has_icon = false; return ok({ ok: true }); }],

  ['GET', '/api/overview', () => ok(overview())],
  ['GET', '/api/settings', () => ok(db.settings)],
  ['PUT', '/api/settings', (p, b) => {
    if (b.ui_host && !inDomain(b.ui_host)) return fail(400, 'invalid', 'ui_host: must belong to one of your domains (add the domain first)', { field: 'ui_host' });
    if (db.routes.some((r) => r.host === b.ui_host)) return fail(400, 'invalid', 'ui_host: already used by a route', { field: 'ui_host' });
    db.settings = { ...db.settings, ...b, setup_complete: true };
    db.meta.ui_host = db.settings.ui_host;
    applied('settings updated', ['caddy', 'authelia']);
    audit('settings.update', 'settings');
    return ok(db.settings);
  }],
  ['GET', '/api/audit', () => ok(db.audit.slice(0, 200))],
  ['GET', '/api/authelia/notification', () => ok({ present: false })],

  ['GET', '/api/dns/providers', () => ok(db.providers)],
  ['GET', '/api/dns/profiles', () => ok(db.profiles)],
  ['POST', '/api/dns/profiles', (p, b) => {
    if (!b.name) return fail(400, 'invalid', 'name: is required', { field: 'name' });
    const prov = db.providers.find((x) => x.id === b.provider);
    if (!prov) return fail(400, 'invalid', 'provider: unknown provider', { field: 'provider' });
    const config = {};
    for (const f of prov.fields) {
      const v = b.config?.[f.key];
      if (f.required && !v) return fail(400, 'invalid', `${f.label}: is required`, { field: f.key });
      if (v) config[f.key] = f.secret ? { set: true } : v;
    }
    const prof = { id: nextId++, name: b.name, provider: b.provider, created_at: iso(), updated_at: iso(), last_test_at: null, last_test_ok: false, last_test_msg: '', config, domains: [] };
    db.profiles.push(prof);
    audit('dns.create', prof.name, prof.provider);
    return ok(prof);
  }],
  ['PUT', '/api/dns/profiles/{id}', (p, b) => {
    const prof = db.profiles.find((x) => x.id === +p.id);
    if (!prof) return notFound();
    prof.name = b.name || prof.name;
    for (const [k, v] of Object.entries(b.config || {})) if (v) prof.config[k] = typeof prof.config[k] === 'object' ? { set: true } : v;
    prof.updated_at = iso();
    return ok(prof);
  }],
  ['DELETE', '/api/dns/profiles/{id}', (p) => {
    if (db.domains.some((d) => d.dns_profile_id === +p.id)) return fail(409, 'conflict', 'the profile is used by a domain');
    db.profiles = db.profiles.filter((x) => x.id !== +p.id);
    return ok({ ok: true });
  }],
  ['POST', '/api/dns/profiles/{id}/test', (p, b) => {
    const prof = db.profiles.find((x) => x.id === +p.id);
    if (!prof) return notFound();
    const zone = b.zone || prof.domains[0] || 'example.org';
    const steps = [`✓ create TXT _marineris-check.${zone}`, '✓ read back through DNS', '✓ delete TXT'];
    Object.assign(prof, { last_test_at: iso(), last_test_ok: true, last_test_msg: steps.join('\n') });
    audit('dns.test', prof.name, 'ok (simulated in the demo)');
    return ok({ ok: true, steps });
  }],

  ['GET', '/api/domains', () => ok(db.domains)],
  ['POST', '/api/domains', (p, b) => {
    if (!HOST.test(b.name || '')) return fail(400, 'invalid', 'name: is not a valid domain name', { field: 'name' });
    if (db.domains.some((d) => d.name === b.name)) return fail(409, 'conflict', `the domain ${b.name} already exists`);
    const d = { id: nextId++, name: b.name, dns_profile_id: b.dns_profile_id ?? null, wildcard: !!(b.wildcard && b.dns_profile_id), created_at: iso(), updated_at: iso() };
    db.domains.push(d);
    for (const prof of db.profiles) if (prof.id === d.dns_profile_id) prof.domains.push(d.name);
    applied(`domain ${d.name} added`, ['caddy', 'authelia']);
    audit('domain.create', d.name);
    return ok(d);
  }],
  ['PUT', '/api/domains/{id}', (p, b) => {
    const d = db.domains.find((x) => x.id === +p.id);
    if (!d) return notFound();
    Object.assign(d, { dns_profile_id: b.dns_profile_id ?? null, wildcard: !!(b.wildcard && b.dns_profile_id), updated_at: iso() });
    applied(`domain ${d.name} updated`);
    return ok(d);
  }],
  ['DELETE', '/api/domains/{id}', (p) => {
    const d = db.domains.find((x) => x.id === +p.id);
    if (!d) return notFound();
    if (db.routes.some((r) => r.host === d.name || r.host.endsWith('.' + d.name))) return fail(409, 'conflict', 'routes still use this domain: delete them first');
    if (db.settings.ui_host === d.name || db.settings.ui_host.endsWith('.' + d.name)) return fail(409, 'conflict', 'the UI host is inside this domain');
    db.domains = db.domains.filter((x) => x !== d);
    applied(`domain ${d.name} removed`, ['caddy', 'authelia']);
    audit('domain.delete', d.name);
    return ok({ ok: true });
  }],
  ['GET', '/api/certificates', () => ok(certificates())],

  ['GET', '/api/routes', () => ok(db.routes)],
  ['POST', '/api/routes', (p, b) => {
    const err = validateRoute(b, 0);
    if (err) return err;
    const r = { id: nextId++, name: b.name, host: b.host, path_prefix: b.path_prefix || '', kind: b.kind, target: b.target, auth: b.auth, rate_limit: b.rate_limit ?? null, enabled: b.enabled !== false, created_at: iso(), updated_at: iso(), hub: { visible: true, title: '', subtitle: '', ...(b.hub || {}), has_icon: false } };
    db.routes.push(r);
    applied(`route ${r.name} added`, r.auth === 'authelia' ? ['caddy', 'authelia'] : ['caddy']);
    audit('route.create', r.name, r.host);
    return ok(r);
  }],
  ['PUT', '/api/routes/{id}', (p, b) => {
    const r = db.routes.find((x) => x.id === +p.id);
    if (!r) return notFound();
    const err = validateRoute(b, r.id);
    if (err) return err;
    Object.assign(r, { name: b.name, host: b.host, path_prefix: b.path_prefix || '', kind: b.kind, target: b.target, auth: b.auth, rate_limit: b.rate_limit ?? null, enabled: b.enabled !== false, updated_at: iso() });
    if (b.hub) r.hub = { ...r.hub, ...b.hub, has_icon: r.hub.has_icon };
    applied(`route ${r.name} updated`, ['caddy', 'authelia']);
    audit('route.update', r.name, r.host);
    return ok(r);
  }],
  ['DELETE', '/api/routes/{id}', (p) => {
    const r = db.routes.find((x) => x.id === +p.id);
    if (!r) return notFound();
    db.routes = db.routes.filter((x) => x !== r);
    db.icons.delete(r.id);
    applied(`route ${r.name} removed`, ['caddy', 'authelia']);
    audit('route.delete', r.name, r.host);
    return ok({ ok: true });
  }],

  ['GET', '/api/containers', () => ok(db.containers)],
  ['GET', '/api/containers/{id}', (p) => { const c = db.containers.find((x) => x.name === p.id); return c ? ok(containerDetail(c)) : notFound(); }],
  ['GET', '/api/containers/{id}/logs', (p) => { const c = db.containers.find((x) => x.name === p.id); return c ? ok({ logs: logs(c) }) : notFound(); }],
  ['POST', '/api/containers/{id}/{action}', (p) => {
    const c = db.containers.find((x) => x.name === p.id);
    if (!c) return notFound();
    if (!['start', 'stop', 'restart'].includes(p.action)) return notFound();
    if (c.system && p.action === 'stop') return fail(403, 'forbidden', `${c.name} is part of Marineris and cannot be stopped from the UI`);
    c.state = p.action === 'stop' ? 'exited' : 'running';
    c.status = p.action === 'stop' ? 'Exited (0) less than a second ago' : 'Up less than a second';
    audit(`container.${p.action}`, c.name);
    return ok({ ok: true });
  }],
  ['DELETE', '/api/containers/{id}', (p) => {
    const c = db.containers.find((x) => x.name === p.id);
    if (!c) return notFound();
    if (c.system) return fail(403, 'forbidden', `${c.name} is part of Marineris and cannot be removed`);
    db.containers = db.containers.filter((x) => x !== c);
    audit('container.remove', c.name);
    return ok({ ok: true });
  }],

  ['GET', '/api/stacks', () => ok(db.stacks.map(stackView))],
  ['POST', '/api/stacks/check', (p, b) => ok(stackCheck(b.compose || '', b.unsafe_ack))],
  ['POST', '/api/stacks', (p, b) => {
    if (!/^[a-z0-9][a-z0-9_-]{0,62}$/.test(b.name || '')) return fail(400, 'invalid', 'name: lowercase letters, digits, "-" and "_"', { field: 'name' });
    if (db.stacks.some((s) => s.name === b.name)) return fail(409, 'conflict', `a stack named "${b.name}" already exists`);
    if (!/^services:/m.test(b.compose || '')) return fail(400, 'bad_request', 'compose: the file must define services');
    const { blocking } = stackCheck(b.compose, b.unsafe_ack);
    if (blocking.length) return fail(422, 'policy', 'the stack violates the security policy', blocking);
    const s = { name: b.name, compose: b.compose, env: b.env || '', unsafe_ack: !!b.unsafe_ack, created_at: iso(), updated_at: iso(), deployed_at: null };
    db.stacks.push(s);
    audit('stack.create', s.name);
    return ok(s);
  }],
  ['GET', '/api/stacks/{name}', (p) => { const s = db.stacks.find((x) => x.name === p.name); return s ? ok(s) : notFound(); }],
  ['PUT', '/api/stacks/{name}', (p, b) => {
    const s = db.stacks.find((x) => x.name === p.name);
    if (!s) return notFound();
    const { blocking } = stackCheck(b.compose || '', b.unsafe_ack);
    if (blocking.length) return fail(422, 'policy', 'the stack violates the security policy', blocking);
    Object.assign(s, { compose: b.compose, env: b.env || '', unsafe_ack: !!b.unsafe_ack, updated_at: iso() });
    audit('stack.update', s.name);
    return ok(s);
  }],
  ['POST', '/api/stacks/{name}/{action}', (p) => {
    const s = db.stacks.find((x) => x.name === p.name);
    if (!s || !['deploy', 'down'].includes(p.action)) return notFound();
    const services = [...s.compose.matchAll(/^ {2}([a-z0-9_-]+):\s*$/gm)].map((m) => m[1]);
    const images = [...s.compose.matchAll(/image:\s*(\S+)/g)].map((m) => m[1]);
    db.containers = db.containers.filter((c) => c.stack !== s.name);
    if (p.action === 'deploy') {
      services.forEach((name, i) => db.containers.push({ id: (nextId++).toString(16).padStart(64, '0'), name, image: images[i] || 'image', state: 'running', status: 'Up less than a second', health: 'none', created: Math.floor(Date.now() / 1000), ports: [], stack: s.name, networks: [`${s.name}_default`], system: false, routable: /marineris-apps/.test(s.compose) }));
      s.deployed_at = iso();
    }
    audit(`stack.${p.action}`, s.name);
    return ok({ ok: true });
  }],
  ['DELETE', '/api/stacks/{name}', (p) => {
    db.stacks = db.stacks.filter((x) => x.name !== p.name);
    db.containers = db.containers.filter((c) => c.stack !== p.name);
    audit('stack.delete', p.name);
    return ok({ ok: true });
  }],

  ['GET', '/api/volumes', () => ok(db.volumes)],
  ['DELETE', '/api/volumes/{name}', (p) => {
    const v = db.volumes.find((x) => x.name === p.name);
    if (!v) return notFound();
    if (v.system) return fail(403, 'forbidden', 'system volumes cannot be removed');
    if (v.stack && db.containers.some((c) => c.stack === v.stack)) return fail(409, 'conflict', 'the volume is in use');
    db.volumes = db.volumes.filter((x) => x !== v);
    audit('volume.delete', v.name);
    return ok({ ok: true });
  }],
  ['GET', '/api/networks', () => ok(db.networks)],

  ['GET', '/api/config/components/{component}/current', (p) => (db.configs[p.component] ? ok({ component: p.component, content: db.configs[p.component] }) : notFound())],
  ['GET', '/api/config/components/{component}/history', (p) => ok(db.history.filter((v) => v.component === p.component))],
  ['GET', '/api/config/versions/{id}', (p) => { const v = db.history.find((x) => x.id === +p.id); return v ? ok({ version: v, content: db.configs[v.component] }) : notFound(); }],
  ['POST', '/api/config/versions/{id}/rollback', (p) => {
    const v = db.history.find((x) => x.id === +p.id);
    if (!v) return notFound();
    applied(`rollback to version ${v.id}`, [v.component]);
    audit('config.rollback', v.component, `version ${v.id}`);
    return ok({ ok: true });
  }],
  ['POST', '/api/config/apply', () => { applied('manual apply', ['caddy', 'authelia']); return ok({ ok: true, at: iso() }); }],
];

const compiled = routes.map(([method, pattern, fn]) => {
  const names = [];
  const re = new RegExp('^' + pattern.replace(/\{(\w+)\}/g, (_, n) => (names.push(n), '([^/]+)')) + '$');
  return { method, re, names, fn };
});

db.sessionTemplate = db.session;

// Answers one API call like the real server would.
export async function handle(method, url, body) {
  await new Promise((r) => setTimeout(r, 60 + Math.random() * 120)); // a little latency
  const [path, qs = ''] = url.split('?');
  for (const r of compiled) {
    if (r.method !== method) continue;
    const m = path.match(r.re);
    if (!m) continue;
    const params = Object.fromEntries(r.names.map((n, i) => [n, decodeURIComponent(m[i + 1])]));
    const open = path === '/api/meta' || path === '/api/health' || path.startsWith('/api/auth/login') || path === '/api/auth/passkey/begin';
    if (!open && !db.session) return unauthenticated();
    return r.fn(params, body || {}, new URLSearchParams(qs));
  }
  return notFound();
}

export function iconURL(id) {
  return db.icons.get(+id) || '';
}
