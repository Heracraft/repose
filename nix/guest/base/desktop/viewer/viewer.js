// The viewer page for the machine's desktop (docs/features/browser.md):
// noVNC's RFB core, connected at once with the password from the URL
// fragment, the remote screen sized to this tab, the clipboard bridged
// both ways where the browser allows it. Plain ES modules; the Nix
// derivation copies this directory as it is.
import RFB from './core/rfb.js';
import KeyTable from './core/input/keysym.js';
import { isMac } from './core/util/browser.js';

const $ = (id) => document.getElementById(id);
const screen = $('screen');
const stateEl = $('state');
const sizeEl = $('size');
const notice = $('notice');
const form = $('password-form');

const query = new URLSearchParams(location.search);
const fragment = new URLSearchParams(location.hash.replace(/^#/, ''));

// The password never leaves the browser: a fragment is not sent with the
// request, so the forward, websockify and any log see only the path.
let password = fragment.get('p') || '';

const clampInt = (v, lo, hi, dflt) => {
  const n = Number.parseInt(v ?? '', 10);
  return Number.isInteger(n) && n >= lo && n <= hi ? n : dflt;
};
// ?q= is the JPEG quality (0 to 9, 9 by default: the screen is text) and
// ?c= the compression level (0 to 9, 1 by default: CPU stays with the
// browser the user watches).
const quality = clampInt(query.get('q'), 0, 9, 9);
const compression = clampInt(query.get('c'), 0, 9, 1);

const wsUrl = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/websockify`;

let rfb = null;
let attempts = 0;      // failed connections since the last good one
let everConnected = false;
let retryTimer = null;
let stopped = false;   // the user closed the connection on purpose (unused today, kept for the form)
let lastClipboard = '';
let idleMinutes = 30;

fetch('project.json', { cache: 'no-store' })
  .then((r) => (r.ok ? r.json() : null))
  .then((p) => {
    if (!p) return;
    if (p.name) {
      $('project').textContent = p.name;
      document.title = `${p.name}, the agent's browser`;
    }
    if (Number.isInteger(p.idle_minutes)) idleMinutes = p.idle_minutes;
  })
  .catch(() => {});

function setState(s, label) {
  stateEl.dataset.state = s;
  stateEl.textContent = label || s;
}

function showNotice(title, text, hint, askPassword) {
  $('notice-title').textContent = title;
  $('notice-text').innerHTML = text;
  $('notice-hint').innerHTML = hint || '';
  form.hidden = !askPassword;
  notice.hidden = false;
  if (askPassword) $('password').focus();
}

function hideNotice() {
  notice.hidden = true;
}

function connect() {
  clearTimeout(retryTimer);
  retryTimer = null;
  if (rfb) {
    rfb.disconnect();
    rfb = null;
  }
  screen.replaceChildren();
  setState('connecting', everConnected ? 'reconnecting' : 'connecting');
  rfb = new RFB(screen, wsUrl, { credentials: { password }, shared: true });
  // The screen takes the tab's size (SetDesktopSize, which Xvnc honours),
  // and whatever fraction is left is scaled; nothing is clipped.
  rfb.scaleViewport = true;
  rfb.resizeSession = true;
  rfb.clipViewport = false;
  rfb.qualityLevel = quality;
  rfb.compressionLevel = compression;
  rfb.showDotCursor = false;
  rfb.focusOnClick = true;
  rfb.background = '#000';

  rfb.addEventListener('connect', () => {
    attempts = 0;
    everConnected = true;
    hideNotice();
    setState('connected');
    rfb.focus();
    // A stray fragment-less link pasted somewhere keeps working while this
    // tab is open; the link the copy button gives has the password again.
    if (password && !location.hash.includes('p=')) {
      history.replaceState(null, '', `#p=${encodeURIComponent(password)}`);
    }
  });

  rfb.addEventListener('disconnect', (e) => {
    setState('off', 'disconnected');
    if (stopped) return;
    attempts += 1;
    // Reconnecting brings the desktop back through the guest's socket
    // activation when the viewer idled out (the 30-minute stop), so the
    // page just tries again, faster at first, then every 15 seconds.
    const delay = Math.min(1000 * 2 ** Math.min(attempts - 1, 4), 15000);
    if (attempts >= 3) {
      showNotice(
        "The machine's desktop is off",
        'The page can reach the machine but nothing answers on its display. Start it with <code>repose browser</code> on your laptop; this page connects as soon as it is up.',
        e.detail && e.detail.clean === false ? `Trying again every ${Math.round(delay / 1000)} seconds.` : ''
      );
    } else if (everConnected) {
      showNotice(
        'Reconnecting',
        `The view sleeps after ${idleMinutes} idle minutes; connecting wakes it.`,
        ''
      );
    }
    retryTimer = setTimeout(connect, delay);
  });

  rfb.addEventListener('credentialsrequired', () => {
    clearTimeout(retryTimer);
    setState('off', 'password needed');
    showNotice(
      'This link has no password',
      'The link <code>repose browser</code> prints carries it after the <code>#</code>. Run the command again, or type the password it printed.',
      '',
      true
    );
  });

  rfb.addEventListener('securityfailure', (e) => {
    clearTimeout(retryTimer);
    stopped = true;
    setState('off', 'refused');
    showNotice(
      'The password was refused',
      'The machine has a new password since this link was made (it changes when the machine boots). Run <code>repose browser</code> again for a fresh link, or type the password it prints.',
      e.detail && e.detail.reason ? e.detail.reason : '',
      true
    );
  });

  // The machine's clipboard to this one.
  rfb.addEventListener('clipboard', (e) => {
    const text = e.detail && e.detail.text;
    if (!text || text === lastClipboard) return;
    lastClipboard = text;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).catch(() => {});
    }
  });
}

const connected = () => rfb && stateEl.dataset.state === 'connected';

function sendClipboard(text) {
  lastClipboard = text;
  rfb.clipboardPasteFrom(text);
}

// This clipboard to the machine's, when the tab comes back to the front,
// where Chromium lets the page read it once the user has allowed it.
// Firefox reads it only for a click or a key, which the paste below covers.
async function pushClipboard() {
  if (!connected()) return;
  if (!navigator.clipboard || !navigator.clipboard.readText) return;
  try {
    const text = await navigator.clipboard.readText();
    if (text && text !== lastClipboard) sendClipboard(text);
  } catch {
    // no permission, or nothing readable: the machine keeps its own
  }
}
window.addEventListener('focus', pushClipboard);
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') pushClipboard();
});

// Paste from this computer (DECISIONS I-726). noVNC turns Ctrl+V into
// key events for the machine and cancels the key here, so the machine
// pasted its own clipboard and this one never left the laptop. The paste
// key is held back from noVNC instead; the browser then fires its paste
// event with this clipboard's text, needing no permission in Chromium or
// Firefox, and the text goes to the machine's clipboard ahead of a Ctrl+V
// on the RFB connection, which carries both in order. On a Mac, Cmd+V
// becomes Ctrl+V on the machine (noVNC sends Cmd as Alt, released first).
const mac = isMac();
let heldPaste = null; // the modifier of a paste key held back, until its paste event
let heldTimer = null;

function isPasteKey(e) {
  if (e.altKey || !(e.code === 'KeyV' || (e.key || '').toLowerCase() === 'v')) return false;
  return mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
}

// Ctrl+V on the machine. `held` is what the laptop holds down, which
// noVNC already sent: 'ctrl', 'meta' (a Mac's Cmd) or nothing.
function pressPaste(held) {
  if (!connected()) return;
  if (held === 'meta') {
    rfb.sendKey(KeyTable.XK_Alt_L, 'MetaLeft', false);
    rfb.sendKey(KeyTable.XK_Super_L, 'MetaRight', false);
  }
  if (held !== 'ctrl') rfb.sendKey(KeyTable.XK_Control_L, 'ControlLeft', true);
  rfb.sendKey(KeyTable.XK_v, 'KeyV');
  if (held !== 'ctrl') rfb.sendKey(KeyTable.XK_Control_L, 'ControlLeft', false);
}

document.addEventListener('keydown', (e) => {
  if (!connected() || !screen.contains(e.target) || !isPasteKey(e)) return;
  // Before noVNC's own listener on the canvas, which would cancel it.
  e.stopPropagation();
  heldPaste = mac ? 'meta' : 'ctrl';
  clearTimeout(heldTimer);
  // A browser that fires no paste event still gets its key through,
  // and the machine pastes what it has.
  heldTimer = setTimeout(() => {
    const held = heldPaste;
    heldPaste = null;
    if (held) pressPaste(held);
  }, 300);
}, true);

document.addEventListener('paste', (e) => {
  if (!connected() || (!heldPaste && !screen.contains(e.target))) return;
  e.preventDefault();
  clearTimeout(heldTimer);
  const held = heldPaste;
  heldPaste = null;
  const text = e.clipboardData ? e.clipboardData.getData('text/plain') : '';
  if (text) sendClipboard(text);
  pressPaste(held);
});

form.addEventListener('submit', (e) => {
  e.preventDefault();
  password = $('password').value;
  $('password').value = '';
  stopped = false;
  attempts = 0;
  hideNotice();
  connect();
});

// The remote size: the canvas noVNC draws is the framebuffer's size in
// pixels, whatever the CSS scale.
setInterval(() => {
  const canvas = screen.querySelector('canvas');
  if (!canvas || stateEl.dataset.state !== 'connected' || !canvas.width) {
    sizeEl.textContent = '';
    return;
  }
  const dpr = window.devicePixelRatio || 1;
  sizeEl.textContent = `${canvas.width} × ${canvas.height}` + (dpr > 1 ? ` at 1x` : '');
}, 500);

// A button's answer for a moment, then its own label again.
function flash(b, label, ok) {
  const rest = b.dataset.label || (b.dataset.label = b.textContent);
  b.textContent = label;
  b.classList.toggle('done', ok);
  clearTimeout(b.flashTimer);
  b.flashTimer = setTimeout(() => {
    b.textContent = rest;
    b.classList.remove('done');
  }, 1500);
}

$('copy').addEventListener('click', async () => {
  try {
    // location.href keeps the fragment, so the link carries the password.
    await navigator.clipboard.writeText(location.href);
    flash($('copy'), 'copied', true);
  } catch {
    flash($('copy'), 'copy failed', false);
  }
});

// The same paste for a pointer: the browser asks once (Chromium) or shows
// its own Paste button (Firefox) before the page may read the clipboard.
$('paste').addEventListener('click', async () => {
  const b = $('paste');
  if (!connected()) return;
  let text;
  try {
    text = await navigator.clipboard.readText();
  } catch {
    flash(b, 'not allowed', false);
    rfb.focus();
    return;
  }
  if (text) sendClipboard(text);
  pressPaste(null);
  rfb.focus();
  flash(b, text ? 'pasted' : 'clipboard empty', !!text);
});

$('full').addEventListener('click', () => {
  if (document.fullscreenElement) {
    document.exitFullscreen();
  } else {
    document.documentElement.requestFullscreen().catch(() => {});
  }
});
// noVNC watches its container's size, so the bar going away resizes the
// remote screen by itself.
document.addEventListener('fullscreenchange', () => {
  document.body.classList.toggle('full', !!document.fullscreenElement);
});

if (!password) {
  setState('off', 'password needed');
  showNotice(
    'This link has no password',
    'The link <code>repose browser</code> prints carries it after the <code>#</code>. Run the command again, or type the password it printed.',
    '',
    true
  );
} else {
  connect();
}
