// WebAuthn helpers of the link page: decoding the server's creation options
// and encoding the browser's answer.

function b64urlToBuf(s) {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)).buffer;
}

function bufToB64url(buf) {
  let bin = '';
  for (const b of new Uint8Array(buf)) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function canCreatePasskeys() {
  return !!window.PublicKeyCredential && window.isSecureContext;
}

export function creationOptions(pk) {
  if (window.PublicKeyCredential?.parseCreationOptionsFromJSON) {
    return PublicKeyCredential.parseCreationOptionsFromJSON(pk);
  }
  return {
    ...pk,
    challenge: b64urlToBuf(pk.challenge),
    user: { ...pk.user, id: b64urlToBuf(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials || []).map((c) => ({ ...c, id: b64urlToBuf(c.id) })),
  };
}

export function serialize(cred) {
  try {
    if (typeof cred.toJSON === 'function') return cred.toJSON();
  } catch {
    // Some password managers return objects whose toJSON fails: fall back.
  }
  const r = cred.response;
  return {
    id: cred.id,
    rawId: bufToB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment || undefined,
    clientExtensionResults: cred.getClientExtensionResults?.() || {},
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      attestationObject: bufToB64url(r.attestationObject),
      transports: r.getTransports ? r.getTransports() : [],
    },
  };
}
