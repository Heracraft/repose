// The viewer page's paste from the laptop (DECISIONS I-726), against a
// real TigerVNC: an Xvnc display of its own with vncconfig and a Chromium
// showing a textarea (the machine's side), websockify serving the viewer
// from this checkout, and Playwright's Chromium and Firefox as the laptop.
// Each browser copies a line on its own clipboard, presses Ctrl+V in the
// viewer, and the textarea on the display must receive that line; then
// the bar's paste control does the same.
//
// Runs on a repose machine (or any Linux with Xvnc, vncconfig, websockify
// and chromium on PATH), never in CI:
//
//   PLAYWRIGHT_MODULE=/path/to/node_modules/playwright/index.js \
//   NOVNC_DIR=/path/with/core-and-vendor \
//   node nix/guest/tests/viewer-paste.mjs [chromium] [firefox]
//
// NOVNC_DIR is any directory holding noVNC's core/ and vendor/, such as
// /run/repose/desktop/web on a machine whose viewer has run once.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, symlinkSync, writeFileSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const pw = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const { chromium, firefox } = pw.default || pw;

const here = dirname(fileURLToPath(import.meta.url));
const viewerSrc = process.env.VIEWER_DIR || join(here, '..', 'base', 'desktop', 'viewer');
const novnc = process.env.NOVNC_DIR || '/run/repose/desktop/web';
const display = process.env.PASTE_DISPLAY || ':97';
const n = Number(display.slice(1));
const rfbPort = 5900 + n;
const webPort = 6000 + n;
const cdpPort = 9300 + n;
// chromium-denied: a Chromium that never granted the page clipboard
// access, as on a first visit; keyboard paste must not need it.
const which = process.argv.slice(2).length ? process.argv.slice(2) : ['chromium', 'chromium-denied', 'firefox'];

const tmp = mkdtempSync(join(tmpdir(), 'viewer-paste-'));
const procs = [];
function start(cmd, args, env = {}) {
  const p = spawn(cmd, args, { env: { ...process.env, DISPLAY: display, ...env }, stdio: ['ignore', 'ignore', 'pipe'] });
  let err = '';
  p.stderr.on('data', (d) => { err = (err + d).slice(-4000); });
  p.errText = () => err;
  procs.push(p);
  return p;
}
function cleanup() {
  for (const p of procs.reverse()) {
    try { p.kill('SIGTERM'); } catch {}
  }
  rmSync(tmp, { recursive: true, force: true });
}
process.on('exit', cleanup);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(what, fn, ms = 20000) {
  const t0 = Date.now();
  let last;
  while (Date.now() - t0 < ms) {
    try {
      last = await fn();
      if (last) return last;
    } catch (e) { last = e; }
    await sleep(200);
  }
  throw new Error(`timed out waiting for ${what}: ${last}`);
}
async function listening(port) {
  const out = execFileSync('ss', ['-Hltn', `sport = :${port}`]).toString();
  return out.includes('LISTEN');
}

// The machine's side.
const password = 'pastepw1';
writeFileSync(join(tmp, 'passwd'), execFileSync('vncpasswd', ['-f'], { input: password + '\n' }));
start('Xvnc', [display, '-geometry', '1280x800', '-depth', '24', '-ac', '-nolisten', 'tcp', '-noreset',
  '-localhost', '-rfbport', String(rfbPort), '-SecurityTypes', 'VncAuth', '-PasswordFile', join(tmp, 'passwd'),
  '-AlwaysShared', '-AcceptSetDesktopSize']);
await until('Xvnc', () => listening(rfbPort));
start('vncconfig', ['-nowin']);
const page = 'data:text/html,' + encodeURIComponent(
  '<title>paste target</title><body style="margin:0"><textarea id="t" autofocus style="width:100vw;height:100vh;font-size:24px"></textarea></body>');
start('chromium', [`--user-data-dir=${join(tmp, 'machine-profile')}`, `--remote-debugging-port=${cdpPort}`,
  '--no-first-run', '--no-default-browser-check', '--disable-gpu', '--password-store=basic',
  '--window-position=0,0', '--window-size=1280,800', `--app=${page}`]);
await until('the machine browser', () => listening(cdpPort), 30000);

// The viewer, as desktop.nix lays out its web root.
const web = join(tmp, 'web');
mkdirSync(web);
for (const f of readdirSync(viewerSrc)) symlinkSync(join(viewerSrc, f), join(web, f));
symlinkSync(join(novnc, 'core'), join(web, 'core'));
symlinkSync(join(novnc, 'vendor'), join(web, 'vendor'));
writeFileSync(join(web, 'project.json'), JSON.stringify({ name: 'paste-test', idle_minutes: 30 }));
start('websockify', ['--file-only', '--web', web, `127.0.0.1:${webPort}`, `127.0.0.1:${rfbPort}`]);
await until('websockify', () => listening(webPort));

const machine = await chromium.connectOverCDP(`http://127.0.0.1:${cdpPort}`);
const target = await until('the target page', () =>
  machine.contexts().flatMap((c) => c.pages()).find((p) => p.url().startsWith('data:')));
const typed = () => target.evaluate(() => document.getElementById('t').value);
const clearTarget = () => target.evaluate(() => { const t = document.getElementById('t'); t.value = ''; t.focus(); });

const viewerUrl = `http://127.0.0.1:${webPort}/#p=${password}`;
let failed = 0;

for (const name of which) {
  const type = name === 'firefox' ? firefox : chromium;
  const opts = name === 'firefox'
    ? { firefoxUserPrefs: { 'dom.events.testing.asyncClipboard': true, 'dom.events.asyncClipboard.readText': true } }
    : { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH || undefined };
  const browser = await type.launch(opts);
  const context = await browser.newContext({ viewport: { width: 1280, height: 832 } });
  if (name === 'chromium') {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: `http://127.0.0.1:${webPort}` });
  }
  const viewer = await context.newPage();
  await viewer.goto(viewerUrl);
  await viewer.waitForSelector('#state[data-state="connected"]', { timeout: 20000 });

  // The laptop's clipboard, filled the way a person does: select and copy.
  async function copyOnLaptop(text) {
    const scratch = await context.newPage();
    await scratch.setContent('<textarea id="s"></textarea>');
    await scratch.fill('#s', text);
    await scratch.focus('#s');
    await scratch.keyboard.press('Control+A');
    await scratch.keyboard.press('Control+C');
    await scratch.close();
    await viewer.bringToFront();
  }

  async function check(label, text, act) {
    await clearTarget();
    await copyOnLaptop(text);
    await act();
    try {
      await until(`${label} in ${name}`, async () => (await typed()) === text, 10000);
      console.log(`ok   ${name}: ${label}`);
    } catch (e) {
      failed += 1;
      console.log(`FAIL ${name}: ${label}: the machine got ${JSON.stringify(await typed())}`);
    }
  }

  const canvas = viewer.locator('#screen canvas');
  await check('Ctrl+V in the viewer pastes the laptop clipboard', `from ${name} keys ${Date.now()}`, async () => {
    await canvas.click({ position: { x: 300, y: 300 } });
    await viewer.keyboard.press('Control+V');
  });
  await check('a second Ctrl+V pastes a new clipboard', `again ${name} ${Date.now()} é ü 日本 ✓`, async () => {
    await canvas.click({ position: { x: 300, y: 300 } });
    await viewer.keyboard.press('Control+V');
  });
  if (name !== 'chromium-denied') {
    await check('the paste control pastes the laptop clipboard', `from ${name} button ${Date.now()}`, async () => {
      await viewer.click('#paste');
    });
  }
  // Typing still reaches the machine, and Ctrl+A on the machine is untouched.
  await clearTarget();
  await canvas.click({ position: { x: 300, y: 300 } });
  await viewer.keyboard.type('typed ok');
  try {
    await until(`typing in ${name}`, async () => (await typed()) === 'typed ok', 10000);
    console.log(`ok   ${name}: typing still reaches the machine`);
  } catch {
    failed += 1;
    console.log(`FAIL ${name}: typing: the machine got ${JSON.stringify(await typed())}`);
  }
  await browser.close();
}

await machine.close().catch(() => {});
console.log(failed ? `${failed} failed` : 'all passed');
process.exit(failed ? 1 : 0);
