// Adds an Authelia passkey for the Marineris UI: the passkey is created here,
// on the portal's host, because a passkey belongs to the host it is created
// on. The ticket (in the URL fragment) comes from the UI's account page.
const base = location.pathname.replace(/\/[^/]*$/, '');
const ticket = location.hash.slice(1);
// The ticket is not needed in the address bar, nor in the history.
history.replaceState(null, '', location.pathname);

const $ = (id) => document.getElementById(id);
let begun = null;
let returnURL = null;

function status(text, kind) {
  const el = $('status');
  el.textContent = text;
  el.className = 'status ' + (kind || '');
  el.hidden = !text;
}

async function call(path, body) {
  const res = await fetch(base + '/api/' + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Marineris-Ticket': ticket },
    body: JSON.stringify(body ?? {}),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'HTTP ' + res.status);
  return data;
}

function b64urlToBuf(s) {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (s.length % 4)) % 4);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)).buffer;
}

function bufToB64url(buf) {
  let bin = '';
  for (const b of new Uint8Array(buf)) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function creationOptions(pk) {
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

function serialize(cred) {
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

async function create(auto) {
  $('create').hidden = true;
  status('Follow your browser or password manager…');
  try {
    const cred = await navigator.credentials.create({ publicKey: creationOptions(begun.options.publicKey) });
    status('Saving…');
    const res = await call('finish', serialize(cred));
    status('Passkey added. Back to Marineris…', 'ok');
    location.replace(res.return_url);
  } catch (err) {
    if (err?.name === 'NotAllowedError' || err?.name === 'AbortError') {
      // Cancelled, or the browser wants a click first.
      status(auto ? '' : 'Passkey creation was cancelled.');
      // Each attempt needs a new challenge.
      begun = await call('begin').catch((e) => {
        status(e.message, 'error');
        return null;
      });
      $('create').hidden = !begun;
      return;
    }
    if (err?.name === 'InvalidStateError') {
      status('This device or password manager already has a passkey for you here.', 'error');
    } else {
      status(err?.message || String(err), 'error');
    }
  }
}

async function main() {
  if (!window.PublicKeyCredential || !window.isSecureContext) {
    $('what').textContent = 'This browser cannot create passkeys.';
    return;
  }
  try {
    begun = await call('begin');
  } catch (err) {
    $('what').textContent = '';
    status(err.message, 'error');
    return;
  }
  returnURL = begun.return_url;
  $('back').href = returnURL.replace(/\?.*$/, '');
  $('back').hidden = false;
  $('what').textContent = `“${begun.name}”, for ${begun.username}: it signs you in to every app, the portal and Marineris.`;
  $('create').addEventListener('click', () => create(false));
  create(true);
}

main();
