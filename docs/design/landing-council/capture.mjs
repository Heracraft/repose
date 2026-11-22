// Landing UI council capture: a fixed, named screen set of / at 1x viewport.
//   node docs/design/landing-council/capture.mjs [baseUrl] [outDir]
//   ONLY=hero,pricing WIDTHS=390,1440 THEMES=light,dark node ...
// Writes <outDir>/<name>-<width>-<theme>.png plus manifest.json (missing, overflow).
// Every shot is the viewport at deviceScaleFactor 1 (CLAUDE.md "Judge visuals
// at real size"), except `full`, which is for the page's rhythm only.
import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync, rmSync } from 'node:fs';

const ROOT = new URL('../../../', import.meta.url).pathname;
const require = createRequire(ROOT + 'apps/web/package.json');
const { chromium } = require('@playwright/test');

const BASE = process.argv[2] || 'http://127.0.0.1:5185';
const OUT = process.argv[3] || '/tmp/claude-1000/landing-council/shots/';
const WIDTHS = (process.env.WIDTHS || '390,1440').split(',').map(Number);
const THEMES = (process.env.THEMES || 'light,dark').split(',');
const ONLY = process.env.ONLY ? process.env.ONLY.split(',') : null;
const CHROMIUM =
  process.env.PLAYWRIGHT_CHROMIUM_PATH ||
  '/nix/store/b3xq32a3v70bq0w9r7z88d7rrpba7cq7-chromium-157.0.8103.78/bin/chromium';
const HEADER = 57;

async function toTop(p, sel, extra = 0) {
  await p.evaluate(
    ([s, h, x]) => {
      const e = document.querySelector(s);
      if (!e) throw new Error('no ' + s);
      window.scrollTo(0, e.getBoundingClientRect().top + window.scrollY - h + x);
    },
    [sel, HEADER, extra]
  );
  await p.waitForTimeout(700);
}
/* Desktop: the wheel is scroll-driven; place the track at a fraction. Phone:
   the beats stack; scroll the nth beat to the top. */
async function wheel(p, i, w) {
  if (w >= 1024) {
    await p.evaluate(
      ([i, h]) => {
        const t = document.querySelector('#agents .sw-track');
        const r = t.getBoundingClientRect();
        const top = r.top + window.scrollY;
        const scrollable = r.height - (window.innerHeight - h);
        window.scrollTo(0, top - h + (scrollable * i) / 2);
      },
      [i, HEADER]
    );
    await p.waitForTimeout(900);
  } else {
    await toTop(p, `#agents .sw-mob-beats > li:nth-child(${i + 1})`, -16);
  }
}

const SCREENS = [
  { name: 'hero', run: async (p) => p.waitForTimeout(1500) },
  { name: 'wheel-where', run: (p, w) => wheel(p, 0, w) },
  { name: 'wheel-agents', run: (p, w) => wheel(p, 1, w) },
  { name: 'wheel-tools', run: (p, w) => wheel(p, 2, w) },
  { name: 'safety', run: (p) => toTop(p, '#safety') },
  { name: 'safety-figure', run: (p) => toTop(p, '#safety .fig') },
  { name: 'pricing', run: (p) => toTop(p, '#pricing') },
  { name: 'pricing-hardware', run: (p) => toTop(p, '#parts', -260) },
  { name: 'start-cloud', run: (p) => toTop(p, '#install') },
  {
    name: 'start-machine',
    run: async (p) => {
      await toTop(p, '#install');
      await p.getByRole('tab', { name: 'Your machine' }).click();
      await p.waitForTimeout(400);
    }
  },
  { name: 'full', full: true, reduce: true }
];

rmSync(OUT, { recursive: true, force: true });
mkdirSync(OUT, { recursive: true });
const manifest = { base: BASE, missing: [], overflow: [] };
const browser = await chromium.launch({ executablePath: CHROMIUM, args: ['--no-sandbox'] });
for (const w of WIDTHS)
  for (const theme of THEMES) {
    const mobile = w < 700;
    for (const s of SCREENS) {
      if (ONLY && !ONLY.includes(s.name)) continue;
      const ctx = await browser.newContext({
        viewport: { width: w, height: mobile ? 844 : 900 },
        deviceScaleFactor: 1,
        isMobile: mobile,
        hasTouch: mobile,
        colorScheme: theme,
        reducedMotion: s.reduce ? 'reduce' : 'no-preference'
      });
      const p = await ctx.newPage();
      await p.route('**/public/seats', (r) =>
        r.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ total: 30, held: 12, free: 18, waiting: 0, founding_left: 18 })
        })
      );
      const file = `${OUT}${s.name}-${w}-${theme}.png`;
      try {
        await p.goto(BASE + '/', { waitUntil: 'networkidle' });
        await p.waitForTimeout(600);
        if (s.run) await s.run(p, w);
        const o = await p.evaluate(() => [document.documentElement.scrollWidth, innerWidth]);
        if (o[0] > o[1] + 1) manifest.overflow.push(`${s.name}-${w}-${theme} (${o[0]} > ${o[1]})`);
        await p.screenshot({ path: file, fullPage: !!s.full });
      } catch (e) {
        manifest.missing.push(`${s.name}-${w}-${theme}: ${e.message.split('\n')[0]}`);
      }
      await ctx.close();
    }
  }
await browser.close();
writeFileSync(OUT + 'manifest.json', JSON.stringify(manifest, null, 2));
const total =
  SCREENS.filter((s) => !ONLY || ONLY.includes(s.name)).length * WIDTHS.length * THEMES.length;
console.log(
  `shots ${total - manifest.missing.length} missing ${manifest.missing.length} overflow ${manifest.overflow.length}`
);
