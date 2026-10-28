// Minimal compose.yaml reader and service editor for the static demo. It
// understands the block style the sample stacks (and the UI) write: two
// spaces of indentation, lists of strings, maps of strings. The real server
// uses a full YAML parser (internal/stacks/services.go).

const unquote = (s) => {
  s = s.trim();
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) return s.slice(1, -1).replace(/\\"/g, '"');
  return s;
};
const indentOf = (line) => line.length - line.trimStart().length;
const quote = (s) => (/^[\w./@${}:-]+$/.test(s) && !/^\d+(:\d+)*$/.test(s) && !/[0-9]:[0-9]/.test(s) ? s : JSON.stringify(s));

// Line range [start, end) of each service block.
function blocks(lines) {
  const out = [];
  let inServices = false;
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    if (!l.trim() || l.trim().startsWith('#')) continue;
    if (indentOf(l) === 0) {
      inServices = /^services:\s*$/.test(l);
      if (out.length && out[out.length - 1].end === undefined) out[out.length - 1].end = i;
      continue;
    }
    if (inServices && indentOf(l) === 2) {
      if (out.length && out[out.length - 1].end === undefined) out[out.length - 1].end = i;
      out.push({ name: l.trim().replace(/:\s*$/, ''), start: i });
    }
  }
  if (out.length && out[out.length - 1].end === undefined) out[out.length - 1].end = lines.length;
  return out;
}

export function parseServices(compose) {
  const lines = compose.split('\n');
  return blocks(lines).map(({ name, start, end }) => {
    const s = { name, image: '', restart: '', environment: [], volumes: [], ports: [], networks: [], labels: [], long_syntax: [], build: false };
    let key = '';
    for (let i = start + 1; i < end; i++) {
      const l = lines[i];
      if (!l.trim() || l.trim().startsWith('#')) continue;
      const ind = indentOf(l);
      const t = l.trim().replace(/\s+#.*$/, '');
      if (ind === 4) {
        const [k, ...rest] = t.split(':');
        const v = rest.join(':').trim();
        key = k;
        if (k === 'image') s.image = unquote(v);
        else if (k === 'restart') s.restart = unquote(v);
        else if (k === 'build') s.build = true;
        else if (['volumes', 'ports', 'networks'].includes(k) && v.startsWith('[')) {
          s[k] = v.slice(1, -1).split(',').map(unquote).filter(Boolean);
        }
      } else if (ind >= 6 && key) {
        if (['volumes', 'ports', 'networks'].includes(key)) {
          if (t.startsWith('- ')) s[key].push(unquote(t.slice(2)));
          else if (!s.long_syntax.includes(key)) s.long_syntax.push(key);
        } else if (key === 'environment' || key === 'labels') {
          const target = key === 'environment' ? s.environment : s.labels;
          if (t.startsWith('- ')) {
            const [k, ...v] = unquote(t.slice(2)).split('=');
            target.push(v.length ? { key: k, value: v.join('=') } : { key: k, value: '', unset: true });
          } else {
            const at = t.indexOf(':');
            const v = t.slice(at + 1).trim();
            target.push(v ? { key: t.slice(0, at), value: unquote(v) } : { key: t.slice(0, at), value: '', unset: true });
          }
        }
      }
    }
    return s;
  });
}

// Returns compose with one service's fields replaced (keys absent from edit
// are kept as they are).
export function editService(compose, name, edit) {
  const lines = compose.split('\n');
  const b = blocks(lines).find((x) => x.name === name);
  if (!b) throw new Error(`no service "${name}"`);
  const current = parseServices(compose).find((x) => x.name === name);
  const next = { ...current, ...Object.fromEntries(Object.entries(edit).filter(([, v]) => v !== undefined)) };
  const keep = [];
  let skipping = false;
  for (let i = b.start + 1; i < b.end; i++) {
    const l = lines[i];
    if (indentOf(l) === 4 && l.trim()) {
      const k = l.trim().split(':')[0];
      skipping = ['image', 'restart', 'environment', 'volumes', 'ports', 'networks'].includes(k) && edit[k] !== undefined;
    }
    if (!skipping) keep.push(l);
  }
  const out = [];
  if (edit.image !== undefined && next.image) out.push(`    image: ${next.image}`);
  if (edit.restart !== undefined && next.restart) out.push(`    restart: ${next.restart}`);
  if (edit.environment !== undefined && next.environment.length) {
    out.push('    environment:');
    for (const v of next.environment) if (v.key) out.push(v.unset ? `      ${v.key}:` : `      ${v.key}: ${JSON.stringify(v.value)}`);
  }
  for (const k of ['volumes', 'ports', 'networks']) {
    const items = (edit[k] !== undefined ? next[k] : []).map((x) => x.trim()).filter(Boolean);
    if (items.length) out.push(`    ${k}:`, ...items.map((x) => `      - ${quote(x)}`));
  }
  return [...lines.slice(0, b.start + 1), ...out, ...keep, ...lines.slice(b.end)].join('\n');
}
