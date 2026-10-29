// Sample data of the static demo: a small home server. The shapes are those
// of the real API (recorded from a running Marineris); dates are relative to
// the moment the page loads.
import providers from './providers.json';
import configs from './configs.json';

const now = Date.now();
const ago = (minutes) => new Date(now - minutes * 60000).toISOString();
const unix = (minutes) => Math.floor((now - minutes * 60000) / 1000);
const id = (n) => n.toString(16).padStart(8, '0').repeat(8);
// Sample app icons drawn here, so that the demo loads nothing from elsewhere.
const svg = (body) => 'data:image/svg+xml,' + encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">${body}</svg>`);
const MEDIA_ICON = svg('<rect width="64" height="64" rx="14" fill="#5b3fd1"/><path d="M25 19v26l21-13z" fill="#fff"/>');
const REQUESTS_ICON = svg('<rect width="64" height="64" rx="14" fill="#c2410c"/><circle cx="28" cy="28" r="12" fill="none" stroke="#fff" stroke-width="5"/><path d="M37 37l12 12" stroke="#fff" stroke-width="6" stroke-linecap="round"/>');
const MONITOR_ICON = svg('<rect width="64" height="64" rx="14" fill="#1f9d63"/><path d="M10 34h11l5-12 8 22 6-14 4 4h10" fill="none" stroke="#fff" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>');

function container(n, name, image, extra = {}) {
  return {
    id: id(n),
    name,
    image,
    state: 'running',
    status: 'Up 3 days',
    health: 'none',
    created: unix(4400 + n * 7),
    ports: [],
    stack: '',
    networks: [],
    system: false,
    routable: false,
    ...extra,
  };
}

export function seed() {
  const admin = { id: 1, username: 'admin', display_name: 'admin', email: '', disabled: false, groups: ['admins'], created_at: ago(43200), updated_at: ago(43200) };
  const containers = [
    container(1, 'marineris', 'ghcr.io/photonring/marineris:0.1.0', { health: 'healthy', status: 'Up 3 days (healthy)', ports: [8080, 8081], stack: 'marineris', service: 'marineris', networks: ['marineris_auth', 'marineris_docker-api', 'marineris_outbound', 'marineris_ui'], system: true }),
    container(2, 'marineris-caddy', 'ghcr.io/photonring/marineris-caddy:0.1.0', { ports: [80, 443, 2019], stack: 'marineris', service: 'caddy', networks: ['marineris-apps', 'marineris_auth', 'marineris_crowdsec', 'marineris_edge', 'marineris_ui'], system: true }),
    container(3, 'marineris-authelia', 'authelia/authelia:4.39.28', { health: 'healthy', status: 'Up 3 days (healthy)', ports: [9091], stack: 'marineris', service: 'authelia', networks: ['marineris_auth'], system: true }),
    container(4, 'marineris-crowdsec', 'crowdsecurity/crowdsec:v1.8.1', { ports: [6060, 8080], stack: 'marineris', service: 'crowdsec', networks: ['marineris_crowdsec'], system: true }),
    container(5, 'marineris-socket-proxy', 'wollomatic/socket-proxy:1', { ports: [2375], stack: 'marineris', service: 'socket-proxy', networks: ['marineris_docker-api'], system: true }),
    container(6, 'jellyfin', 'jellyfin/jellyfin:10.11', { status: 'Up 2 days', ports: [8096, 8920], stack: 'media', service: 'jellyfin', networks: ['marineris-apps', 'media_default'], routable: true }),
    container(9, 'jellyseerr', 'fallenbagel/jellyseerr:2', { status: 'Up 2 days', ports: [5055], stack: 'media', service: 'jellyseerr', networks: ['media_default'] }),
    container(7, 'uptime-kuma', 'louislam/uptime-kuma:2', { health: 'healthy', status: 'Up 2 days (healthy)', ports: [3001], stack: 'monitoring', service: 'uptime-kuma', networks: ['marineris-apps', 'monitoring_default'], routable: true }),
    container(8, 'whoami', 'traefik/whoami:v1.11', { state: 'exited', status: 'Exited (0) 5 hours ago', ports: [80], networks: ['marineris-apps'], routable: true }),
  ];
  const route = (rid, name, host, kind, target, auth, hub, minutes) => ({
    id: rid, name, host, path_prefix: '', kind, target, auth, rate_limit: null, enabled: true,
    created_at: ago(minutes), updated_at: ago(minutes), hub: { has_icon: false, icon_link: '', ...hub },
  });
  const routes = [
    route(1, 'jellyfin', 'media.mars.example', 'container', { container: 'jellyfin', port: 8096 }, 'none', { visible: true, title: 'Jellyfin', subtitle: 'Films and series' }, 2900),
    route(2, 'status', 'status.mars.example', 'container', { container: 'uptime-kuma', port: 3001 }, 'authelia', { visible: true, title: 'Uptime Kuma', subtitle: 'Service monitoring' }, 2800),
    route(3, 'nas', 'nas.mars.example', 'external', { url: 'http://192.168.1.20:5000' }, 'admins', { visible: true, title: 'NAS', subtitle: 'Synology DSM on the LAN' }, 2000),
    route(4, 'docs', 'docs.valles.example', 'static', { folder: 'docs' }, 'none', { visible: true, title: 'Documentation', subtitle: 'Static site' }, 1500),
  ];
  return {
    meta: { listener: 'edge', name: 'Marineris', setup_complete: true, ui_host: 'mars.example', version: '0.1.0 (demo)' },
    user: admin,
    session: {
      auth_at: ago(5), csrf_token: 'demo', expires_at: new Date(now + 12 * 3600000).toISOString(),
      method: 'passkey', step_up_ok: true, user: admin,
      rights: { administrator: true, proxy_admin: false, owner: true },
    },
    settings: {
      setup_complete: true, acme_email: 'admin@mars.example', acme_staging: false, acme_directory: '', acme_trusted_root_file: '',
      dns_resolvers: null, dns_propagation_timeout: '2m', ui_host: 'mars.example', ui_protect_with_authelia: true, auth_subdomain: 'auth',
      home_page: 'hub', hub_title: 'Marineris', hub_widgets: ['clock', 'cpu', 'memory', 'disk', 'network', 'uptime'], hub_background: true,
      crowdsec_enabled: true, default_rate_limit: { events: 0, window: '' }, login_rate_limit: { events: 20, window: '1m' },
    },
    providers,
    profiles: [
      {
        id: 1, name: 'OVH personal', provider: 'ovh', created_at: ago(3000), updated_at: ago(3000),
        last_test_at: ago(2990), last_test_ok: true, last_test_msg: '✓ create TXT _marineris-check.mars.example\n✓ read back through DNS\n✓ delete TXT',
        config: { application_key: 'demo-application-key', application_secret: { set: true }, consumer_key: { set: true }, endpoint: 'ovh-eu' },
        domains: ['mars.example'],
      },
    ],
    domains: [
      { id: 1, name: 'mars.example', dns_profile_id: 1, wildcard: true, created_at: ago(2995), updated_at: ago(2995) },
      { id: 2, name: 'valles.example', dns_profile_id: null, wildcard: false, created_at: ago(1600), updated_at: ago(1600) },
    ],
    routes,
    // Services own their icons; the routes to them follow (no copy).
    serviceIcons: new Map([
      ['media/jellyfin', MEDIA_ICON],
      ['media/jellyseerr', REQUESTS_ICON],
      ['monitoring/uptime-kuma', MONITOR_ICON],
    ]),
    icons: new Map(), // routes' own icons
    containers,
    stacks: [
      {
        name: 'media', unsafe_ack: false, created_at: ago(3100), updated_at: ago(3100), deployed_at: ago(2950), env: 'TZ=Europe/Paris\n',
        compose: '# Films and series, and a request page for the family.\nservices:\n  jellyfin:\n    image: jellyfin/jellyfin:10.11\n    container_name: jellyfin\n    restart: unless-stopped\n    environment:\n      TZ: ${TZ}\n      JELLYFIN_PublishedServerUrl: "https://media.mars.example"\n    volumes:\n      - config:/config\n      - /srv/media:/media:ro\n    networks:\n      - default\n      - apps\n    labels:\n      io.marineris.main: "true"\n  jellyseerr:\n    image: fallenbagel/jellyseerr:2\n    container_name: jellyseerr\n    restart: unless-stopped\n    environment:\n      TZ: ${TZ}\n      LOG_LEVEL: info\n    volumes:\n      - requests:/app/config\nvolumes:\n  config: {}\n  requests: {}\nnetworks:\n  apps:\n    external: true\n    name: marineris-apps\n',
      },
      {
        name: 'monitoring', unsafe_ack: false, created_at: ago(2850), updated_at: ago(2850), deployed_at: ago(2840), env: '',
        compose: 'services:\n  uptime-kuma:\n    image: louislam/uptime-kuma:2\n    container_name: uptime-kuma\n    restart: unless-stopped\n    volumes:\n      - data:/app/data\n    networks:\n      - default\n      - apps\nvolumes:\n  data: {}\nnetworks:\n  apps:\n    external: true\n    name: marineris-apps\n',
      },
    ],
    volumes: [
      ...['authelia-data', 'caddy-admin', 'caddy-config', 'caddy-data', 'caddy-logs', 'crowdsec-config', 'crowdsec-db'].map((v) => ({
        name: 'marineris_' + v, driver: 'local', mountpoint: `/var/lib/docker/volumes/marineris_${v}/_data`, created_at: ago(4400), stack: 'marineris', system: true,
      })),
      { name: 'media_config', driver: 'local', mountpoint: '/var/lib/docker/volumes/media_config/_data', created_at: ago(2950), stack: 'media', system: false },
      { name: 'media_requests', driver: 'local', mountpoint: '/var/lib/docker/volumes/media_requests/_data', created_at: ago(2950), stack: 'media', system: false },
      { name: 'monitoring_data', driver: 'local', mountpoint: '/var/lib/docker/volumes/monitoring_data/_data', created_at: ago(2840), stack: 'monitoring', system: false },
      { name: 'old_backup', driver: 'local', mountpoint: '/var/lib/docker/volumes/old_backup/_data', created_at: ago(9000), system: false },
    ],
    networks: [
      { id: id(101), name: 'bridge', driver: 'bridge', scope: 'local', internal: false },
      { id: id(102), name: 'host', driver: 'host', scope: 'local', internal: false },
      { id: id(103), name: 'marineris-apps', driver: 'bridge', scope: 'local', internal: true, stack: 'marineris', system: true },
      ...['auth', 'docker-api', 'ui'].map((n, i) => ({ id: id(110 + i), name: 'marineris_' + n, driver: 'bridge', scope: 'local', internal: true, stack: 'marineris', system: true })),
      ...['crowdsec', 'edge', 'outbound'].map((n, i) => ({ id: id(120 + i), name: 'marineris_' + n, driver: 'bridge', scope: 'local', internal: false, stack: 'marineris', system: true })),
      { id: id(130), name: 'media_default', driver: 'bridge', scope: 'local', internal: false, stack: 'media' },
      { id: id(131), name: 'monitoring_default', driver: 'bridge', scope: 'local', internal: false, stack: 'monitoring' },
    ],
    configs: { caddy: configs.caddy, authelia: configs.authelia },
    history: [
      { id: 6, component: 'caddy', created_at: ago(1500), actor: 'admin', reason: 'route docs added', status: 'applied', error: '' },
      { id: 5, component: 'caddy', created_at: ago(1600), actor: 'admin', reason: 'domain valles.example added', status: 'superseded', error: '' },
      { id: 4, component: 'authelia', created_at: ago(2000), actor: 'admin', reason: 'route nas added', status: 'applied', error: '' },
      { id: 3, component: 'caddy', created_at: ago(2000), actor: 'admin', reason: 'route nas added', status: 'superseded', error: '' },
      { id: 2, component: 'caddy', created_at: ago(2010), actor: 'admin', reason: 'route nas: upstream unreachable during health check', status: 'rolled_back', error: 'health check failed: probe listener did not answer' },
      { id: 1, component: 'caddy', created_at: ago(2900), actor: 'admin', reason: 'route jellyfin added', status: 'superseded', error: '' },
    ],
    audit: [
      ['route.create', 'docs', 'docs.valles.example', 1500],
      ['domain.create', 'valles.example', '', 1600],
      ['route.create', 'nas', 'nas.mars.example', 2000],
      ['config.rollback', 'caddy', 'automatic: health check failed', 2010],
      ['route.create', 'status', 'status.mars.example', 2800],
      ['stack.deploy', 'monitoring', '', 2840],
      ['route.create', 'jellyfin', 'media.mars.example', 2900],
      ['stack.deploy', 'media', '', 2950],
      ['dns.test', 'OVH personal', 'ok', 2990],
      ['passkey.register', 'admin', 'Laptop', 4300],
      ['login', 'admin', 'passkey', 5],
    ]
      .sort((a, b) => a[3] - b[3])
      .map(([action, target, details, minutes], i, all) => ({
        id: all.length - i, at: ago(minutes), actor: 'admin', ip: '203.0.113.24', action, target, success: true, details,
      })),
    brand: { name: '', accent: '', images: { favicon: '', logo: '', background: '' } },
    passkeys: [{ id: 'ZGVtby1sYXB0b3A', name: 'Laptop', created_at: ago(4300), last_used_at: ago(5) }],
    started: now - (3 * 86400 + 4 * 3600) * 1000,
  };
}
