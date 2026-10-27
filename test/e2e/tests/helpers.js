import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

export const PASSWORD = 'Valles-Marineris-E2E-2026!';

export function setupToken() {
  return fs.readFileSync(path.join(process.cwd(), '.data', 'secrets', 'setup-token'), 'utf8').trim();
}

function base32(s) {
  const a = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of s.replace(/=+$/, '')) bits += a.indexOf(c).toString(2).padStart(5, '0');
  const out = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) out.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(out);
}

// RFC 6238 code; offset lets tests use the next time step, since a code
// can only be used once (replay protection).
export function totp(secret, offset = 0) {
  const step = Math.floor(Date.now() / 30000) + offset;
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const h = crypto.createHmac('sha1', base32(secret)).update(msg).digest();
  const o = h[h.length - 1] & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1e6).padStart(6, '0');
}

// Fails the test on any page error or CSP violation.
export function watchConsole(page) {
  const problems = [];
  page.on('pageerror', (e) => problems.push('pageerror: ' + e.message));
  page.on('console', (m) => {
    const t = m.text();
    if (/Content Security Policy|Refused to/i.test(t)) problems.push(t);
  });
  return problems;
}
