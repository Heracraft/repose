import { chromium } from '@playwright/test';
const OUT = process.env.OUT;
const STATE = '/home/azureuser/projects/factory/apps/web/tests-live/.auth/state.json';
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] });
const SEL = {
  name: '#trigger-name',
  fires: '[aria-labelledby="trigger-kind-label"]',
  then: '[aria-labelledby="trigger-then-label"]',
  promptLabel: '#trigger-prompt-label',
  promptField: '#trigger-prompt, #trigger-prompt-path, #trigger-target',
  more: 'button:has-text("More settings")',
  submit: 'button[type=submit]',
  form: '[data-testid="trigger-form"]',
  when: '#trigger-shape',
  zone: '#trigger-zone'
};
async function box(page, sel) {
  const el = page.locator(sel).first();
  if (!(await el.count()) || !(await el.isVisible().catch(() => false))) return null;
  const b = await el.boundingBox();
  return b && { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width), h: Math.round(b.height) };
}
async function snap(page, w, name) {
  const form = page.locator(SEL.form);
  await page.waitForTimeout(250);
  const out = { state: name, w };
  for (const k of Object.keys(SEL)) out[k] = await box(page, SEL[k]);
  console.log(JSON.stringify(out));
  await form.scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${OUT}/${name}-${w}.png` });
}
for (const w of [1440, 390]) {
  const ctx = await browser.newContext({ storageState: STATE, viewport: { width: w, height: w === 1440 ? 900 : 844 }, colorScheme: 'light', deviceScaleFactor: 1 });
  const page = await ctx.newPage();
  await page.goto('https://repose.run/projects', { waitUntil: 'networkidle' });
  const href = await page.locator('a[href^="/projects/"]').first().getAttribute('href');
  await page.goto(`https://repose.run${href.split('/').slice(0, 3).join('/')}/triggers`, { waitUntil: 'networkidle' });
  await page.getByRole('button', { name: 'Add trigger' }).first().click();
  await page.locator(SEL.name).fill('triage');
  await snap(page, w, '01-webhook-agent-write');
  await page.getByRole('button', { name: 'A file in the machine' }).click();
  await snap(page, w, '02-webhook-agent-file');
  await page.getByRole('button', { name: 'Write it here' }).click();
  await snap(page, w, '03-webhook-agent-write-again');
  await page.getByRole('button', { name: 'Forward to your app' }).click();
  await snap(page, w, '04-webhook-forward');
  await page.getByRole('button', { name: 'Start an agent' }).click();
  await snap(page, w, '05-webhook-agent-again');
  await page.getByRole('button', { name: 'A schedule' }).click();
  await snap(page, w, '06-schedule-default');
  const opts = await page.locator(`${SEL.when} option`).evaluateAll((os) => os.map((o) => o.value));
  for (const v of opts) {
    await page.locator(SEL.when).selectOption(v);
    await snap(page, w, `07-schedule-${v}`);
  }
  await page.getByRole('button', { name: 'A file in the machine' }).click();
  await snap(page, w, '08-schedule-file');
  await page.getByRole('button', { name: 'A webhook' }).click();
  await snap(page, w, '09-webhook-again');
  await page.getByRole('button', { name: 'More settings' }).click();
  await snap(page, w, '10-webhook-more');
  await page.getByRole('button', { name: 'A schedule' }).click();
  await snap(page, w, '11-schedule-more');
  await ctx.close();
}
await browser.close();
