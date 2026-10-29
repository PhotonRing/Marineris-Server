// Opens an Invite: the invited person chooses a password and sets up an
// authenticator; only then the User exists. The page is on the portal's
// host, so it works without being signed in. The link's token is in the URL
// fragment (never sent to a server, nor logged).
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
  status('Creating your account…');
  try {
    const res = await call('finish', { password: $('password').value, totp_code: $('code').value.trim() });
    $('form').hidden = true;
    status('Your account is ready. On to the sign-in…', 'ok');
    location.replace(res.redirect_url);
  } catch (err) {
    status(err.message, 'error');
    $('submit').disabled = false;
  }
}

async function main() {
  let link, totp;
  try {
    link = await call('read');
    totp = await call('totp');
  } catch (err) {
    $('what').textContent = '';
    status(err.message, 'error');
    return;
  }
  document.title = `Welcome to ${link.brand.name}`;
  $('title').textContent = `Welcome to ${link.brand.name}, ${link.display_name}`;
  $('what').textContent = 'Choose a password and set up an authenticator app to create your account.';
  $('username').value = link.username;
  $('qr').src = totp.qr;
  $('secret').textContent = totp.totp_secret;
  $('form').hidden = false;
  $('form').addEventListener('submit', finish);
}

main();
