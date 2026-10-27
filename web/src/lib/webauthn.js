// WebAuthn helpers: convert the server's JSON options (base64url fields) to
// the ArrayBuffers the browser API expects, and serialise credentials back.

export function passkeysSupported() {
  return typeof window.PublicKeyCredential !== 'undefined' && window.isSecureContext;
}

function b64urlToBuf(s) {
  const pad = '='.repeat((4 - (s.length % 4)) % 4);
  const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/');
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

function bufToB64url(buf) {
  const bytes = new Uint8Array(buf);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function creationOptions(pk) {
  if (window.PublicKeyCredential?.parseCreationOptionsFromJSON) {
    return window.PublicKeyCredential.parseCreationOptionsFromJSON(pk);
  }
  return {
    ...pk,
    challenge: b64urlToBuf(pk.challenge),
    user: { ...pk.user, id: b64urlToBuf(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials || []).map((c) => ({ ...c, id: b64urlToBuf(c.id) })),
  };
}

function requestOptions(pk) {
  if (window.PublicKeyCredential?.parseRequestOptionsFromJSON) {
    return window.PublicKeyCredential.parseRequestOptionsFromJSON(pk);
  }
  return {
    ...pk,
    challenge: b64urlToBuf(pk.challenge),
    allowCredentials: (pk.allowCredentials || []).map((c) => ({ ...c, id: b64urlToBuf(c.id) })),
  };
}

function serialize(cred) {
  if (typeof cred.toJSON === 'function') return cred.toJSON();
  const r = cred.response;
  const response = { clientDataJSON: bufToB64url(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = bufToB64url(r.attestationObject);
    if (r.getTransports) response.transports = r.getTransports();
  }
  if (r.authenticatorData) response.authenticatorData = bufToB64url(r.authenticatorData);
  if (r.signature) response.signature = bufToB64url(r.signature);
  if (r.userHandle) response.userHandle = bufToB64url(r.userHandle);
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment || undefined,
    clientExtensionResults: cred.getClientExtensionResults?.() || {},
    response,
  };
}

export async function createPasskey(options) {
  const cred = await navigator.credentials.create({ publicKey: creationOptions(options.publicKey) });
  return serialize(cred);
}

export async function getPasskey(options) {
  const cred = await navigator.credentials.get({ publicKey: requestOptions(options.publicKey), mediation: 'optional' });
  return serialize(cred);
}
