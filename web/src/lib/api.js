// Thin client for the Marineris API. It carries the CSRF token, turns error
// responses into ApiError and transparently asks for a step-up confirmation
// when an action needs a recent login.

let csrf = '';
let stepUpHandler = null;
let unauthenticatedHandler = null;

export function setCsrf(token) {
  csrf = token || '';
}

export function onStepUp(fn) {
  stepUpHandler = fn;
}

export function onUnauthenticated(fn) {
  unauthenticatedHandler = fn;
}

export class ApiError extends Error {
  constructor(status, body) {
    super(body?.error || `HTTP ${status}`);
    this.status = status;
    this.code = body?.code || '';
    this.details = body?.details;
  }
}

export async function api(method, path, body, { raw = false, retry = true } = {}) {
  const opts = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined && body !== null) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = raw ? body : JSON.stringify(body);
  }
  if (method !== 'GET') opts.headers['X-CSRF-Token'] = csrf;
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { error: text };
  }
  if (!res.ok) {
    const err = new ApiError(res.status, data);
    if (err.code === 'reauth_required' && retry && stepUpHandler) {
      if (await stepUpHandler()) return api(method, path, body, { raw, retry: false });
    }
    if (err.code === 'unauthenticated' && unauthenticatedHandler) unauthenticatedHandler();
    throw err;
  }
  if (data && typeof data === 'object' && data.csrf_token) csrf = data.csrf_token;
  return data;
}

export const get = (p) => api('GET', p);
export const post = (p, b) => api('POST', p, b ?? {});
export const put = (p, b) => api('PUT', p, b);
export const del = (p) => api('DELETE', p);

// Human-readable message for an error, including validation details.
export function describe(err) {
  if (!(err instanceof ApiError)) return err?.message || String(err);
  if (Array.isArray(err.details)) {
    const lines = err.details.map((d) => (typeof d === 'string' ? d : d.service ? `${d.service}: ${d.message}` : d.message));
    return `${err.message}:\n• ${lines.join('\n• ')}`;
  }
  return err.message;
}
