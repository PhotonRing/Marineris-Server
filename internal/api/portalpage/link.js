// Opens an Invite: the invited person chooses a password and sets up an
// authenticator; only then the User exists. Opens a Reset link too: the User
// sets only what it resets (link.sets). The page is on the portal's host, so
// it works without being signed in. The link's token is in the URL fragment
// (never sent to a server, nor logged).
const base = location.pathname.replace(/\/[^/]*$/, '');
const token = location.hash.slice(1);
// The token is not needed in the address bar, nor in the history.
history.replaceState(null, '', location.pathname);

const $ = (id) => document.getElementById(id);

function status(text, kind) {
  const el = $('status');
  el.textContent = text;
  el.className = 'status ' + (kind || '');
  el.hidden = !text;
}

async function call(path, body) {
  const res = await fetch(base + '/api/' + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Marineris-Link': token },
    body: JSON.stringify(body ?? {}),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || 'HTTP ' + res.status);
  return data;
}

async function finish(event) {
  event.preventDefault();
  if ($('password').value !== $('password2').value) {
    status('The two passwords differ.', 'error');
    return;
  }
  $('submit').disabled = true;
  status(reset ? 'Saving…' : 'Creating your account…');
  // Only what the link sets.
  const body = {};
  if (sets.includes('password')) body.password = $('password').value;
  if (sets.includes('second_factor')) body.totp_code = $('code').value.trim();
  try {
    const res = await call('finish', body);
    $('form').hidden = true;
    status(reset ? 'Done. On to the sign-in…' : 'Your account is ready. On to the sign-in…', 'ok');
    location.replace(res.redirect_url);
  } catch (err) {
    status(err.message, 'error');
    $('submit').disabled = false;
  }
}

// What the link sets (password, second_factor), and whether it is a Reset
// link.
let sets = [];
let reset = false;

// show shows or hides a part of the form; a hidden one is not required.
function show(part, visible) {
  part.hidden = !visible;
  part.querySelectorAll('input').forEach((i) => {
    if (!visible) i.required = false;
  });
}

async function main() {
  let link, totp;
  try {
    link = await call('read');
    sets = link.sets;
    reset = link.kind === 'reset';
    if (sets.includes('second_factor')) totp = await call('totp');
  } catch (err) {
    $('what').textContent = '';
    status(err.message, 'error');
    return;
  }
  if (reset) {
    document.title = `Reset your access to ${link.brand.name}`;
    $('title').textContent = `Hello ${link.display_name}`;
    $('what').textContent = {
      password: 'Choose a new password. Your authenticator and passkeys stay as they are.',
      second_factor: 'Set up your authenticator app again. Your old authenticator and passkeys will no longer work; your password stays.',
    }[sets.join(',')] || 'Choose a new password and set up your authenticator app again. Your old authenticator and passkeys will no longer work.';
    $('submit').textContent = 'Save';
  } else {
    document.title = `Welcome to ${link.brand.name}`;
    $('title').textContent = `Welcome to ${link.brand.name}, ${link.display_name}`;
    $('what').textContent = 'Choose a password and set up an authenticator app to create your account.';
  }
  $('username').value = link.username;
  show($('password').closest('label'), sets.includes('password'));
  show($('password2').closest('label'), sets.includes('password'));
  show($('code').closest('fieldset'), sets.includes('second_factor'));
  if (totp) {
    $('qr').src = totp.qr;
    $('secret').textContent = totp.totp_secret;
  }
  $('form').hidden = false;
  $('form').addEventListener('submit', finish);
}

main();
