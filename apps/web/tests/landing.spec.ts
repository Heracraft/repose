// DECISIONS I-1452 (amending I-1446, I-961, I-837, I-290): the landing page
// converts in six focused bands after the hero: pricing from lib/plans.ts +
// GET /public/seats, Own hardware as a flush hairline row aligned with Plus,
// hero CTA + CodeCopy install command (untruncated on phones) + dynamic price
// meta line, zero looping cartoons on `/`, and strict honesty rules (no Mac
// start-time or Mac memory sizing claims).
import { test, expect } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

test('the pricing section shows the three plans, sizing guide and seats left', async ({ page }) => {
	await resetBilling();
	await page.goto('/');
	const pricing = page.locator('section#pricing');
	await expect(pricing.getByRole('heading', { name: 'Launch prices' })).toBeVisible();
	await expect(pricing.locator('[data-testid^="plan-"] h3.p-name')).toHaveText([
		'Solo',
		'Plus',
		'Pro'
	]);
	const solo = pricing.getByTestId('plan-solo');
	await expect(solo.locator('.p-price .was')).toHaveText('$49');
	await expect(solo.locator('.p-price .n')).toHaveText('$39');
	await expect(solo.locator('.p-then')).toHaveText('First 12 months, first 30 subscribers');
	await expect(solo.locator('.p-run')).toHaveText('8 GB running at once');
	await expect(solo.locator('.p-specs')).toHaveText('100 GB disk · 250 GB egress');
	await expect(pricing.locator('[data-testid^="plan-"] :is(a, button)')).toHaveCount(0);
	await expect(pricing.getByText('$89')).toBeVisible();
	await expect(pricing.getByText('$169')).toBeVisible();
	await expect(pricing.getByText('16 GB running at once')).toBeVisible();
	await expect(pricing.getByText('250 GB disk · 500 GB egress')).toBeVisible();
	await expect(pricing.getByText('32 GB running at once')).toBeVisible();
	await expect(pricing.getByText('500 GB disk · 1 TB egress')).toBeVisible();
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left');
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	await expect(pricing.locator('.pricing-sub')).toContainText(
		'bring your own agent subscriptions or API keys'
	);
	await expect(pricing.getByText('A change reaches you by email')).toHaveCount(0);
});

// Own hardware (I-1364, I-1452): flush hairline row under the three hosted
// plans with no rounded card border, aligned flush with the Plus column at 1440.
test('the pricing section offers Own hardware as a flush row aligned with Plus', async ({
	page
}) => {
	await resetBilling();
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	const own = pricing.getByTestId('plan-own');
	await expect(own.getByRole('heading', { level: 3, name: 'Own hardware' })).toBeVisible();
	await expect(own.locator('.o-price .o-n')).toHaveText('$10');
	await expect(own).toContainText('One host; its memory and disk are the limits.');
	await expect(pricing.locator('[data-testid^="plan-"] h3.p-name')).toHaveCount(3);
	const cells = await pricing.locator('[data-testid^="plan-"]').allTextContents();
	expect(cells[cells.length - 1]).toContain('Own hardware');
	await expect(own).toHaveCSS('border-top-left-radius', '0px');
	await expect(own).toHaveCSS('border-left-width', '0px');
	const [plusName, ownWhat] = await Promise.all([
		pricing.getByTestId('plan-plus').locator('.p-name').boundingBox(),
		own.locator('.o-what').boundingBox()
	]);
	expect(Math.abs(ownWhat!.x - plusName!.x)).toBeLessThanOrEqual(2);
});

test('with no founding places left, Solo and the hero meta show $49 and no founding line', async ({
	page
}) => {
	await setBilling({ founding_left: 0 });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	const solo = pricing.getByTestId('plan-solo');
	await expect(solo.locator('.p-price .n')).toHaveText('$49');
	await expect(solo.locator('.p-then')).toHaveCount(0);
	await expect(solo.locator('.p-price .was')).toHaveCount(0);
	await expect(pricing.getByText('$39')).toHaveCount(0);
	await expect(page.locator('.hero-meta')).toContainText('Hosted from $49/mo');
	await setBilling({ founding_left: 30 });
});

test('one seat free: the line is singular', async ({ page }) => {
	await setBilling({ seats: { total: 30, held: 29, waiting: 0 } });
	await page.goto('/');
	await expect(page.getByTestId('seats-line')).toHaveText('1 seat left');
	await resetBilling();
});

test('when full, the line says so with the number waiting', async ({ page }) => {
	await setBilling({ seats: { total: 30, held: 30, waiting: 41 } });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	await expect(page.getByTestId('seats-line')).toContainText('Full for now. 41 waiting;');
	await expect(page.getByTestId('seats-line')).toContainText(
		"join the list and you're emailed when a seat frees."
	);
});

test('the run step shows and copies repose run alone', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const step = page.locator('.steps li', { hasText: 'Run in any checkout' });
	await expect(step.locator('.code-copy')).toContainText('repose run');
	await expect(step).not.toContainText('cd ~/code/your-project');
	await step.getByRole('button', { name: 'Copy repose run' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose run');
	await expect(page.getByText('job-alerts')).toHaveCount(0);
});

// The install command shows twice: in the hero's CodeCopy bar and as step 6.1.
// Its copy button copies the exact command without the `$ ` prompt.
test('the install command shows in the hero and the first step, and copies cleanly', async ({
	page,
	context
}) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const install = 'curl -fsSL https://repose.run/install.sh | sh';
	await expect(page.locator('.hero .hero-install .code-copy .line')).toContainText(install);
	await expect(page.locator('.steps li').first().locator('.code-copy')).toContainText(install);
	expect(
		await page.locator('main').evaluate((m, i) => m.textContent!.split(i).length - 1, install)
	).toBe(2);
	const button = page.locator('.hero').getByRole('button', { name: 'Copy the install command' });
	await button.click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(install);
});

test('the headline starts under the logo, and stands centred over the terminal from 1024px', async ({
	page
}) => {
	for (const width of [390, 768, 1024, 1440]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const logo = (await page
			.getByRole('link', { name: 'repose, home' })
			.locator('svg')
			.filter({ visible: true })
			.first()
			.boundingBox())!;
		const headline = (await page.locator('h1').boundingBox())!;
		if (width < 1024) {
			expect(Math.abs(logo.x - headline.x), `at ${width}`).toBeLessThanOrEqual(1);
			continue;
		}
		const pic = (await page.locator('.hero .term').boundingBox())!;
		const mid = (b: { x: number; width: number }) => b.x + b.width / 2;
		expect(Math.abs(mid(headline) - mid(pic)), `at ${width}`).toBeLessThanOrEqual(1);
		expect(headline.x, `at ${width}`).toBeGreaterThan(logo.x);
	}
});

test('the page never scrolls sideways', async ({ page }) => {
	for (const width of [320, 390, 768, 1024, 1120, 1240, 1440]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const over = await page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(over, `at ${width}`).toBeLessThanOrEqual(0);
	}
});

// Below 640px the hero and step install commands wrap cleanly after `/` so a
// phone visitor sees the full `curl ... | sh` command without clipping.
test('the hero install command shows in full without clipping on a phone', async ({ page }) => {
	for (const width of [360, 390]) {
		await page.setViewportSize({ width, height: 844 });
		await page.goto('/');
		const line = page.locator('.hero .hero-install .code-copy .line');
		await expect(line).toHaveCSS('font-size', '12px');
		const m = await line.evaluate((e) => ({
			scrollW: e.scrollWidth,
			clientW: e.clientWidth,
			right: e.getBoundingClientRect().right,
			text: e.textContent ?? ''
		}));
		expect(m.text, `at ${width}`).toContain('install.sh | sh');
		expect(m.scrollW, `at ${width}`).toBeLessThanOrEqual(m.clientW + 1);
		expect(m.right, `at ${width}`).toBeLessThanOrEqual(width);
	}
});

test("each picture band's stage ends on the next band's rule", async ({ page }) => {
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const gaps = await page.locator('main > .band:has(> .fig)').evaluateAll((bands) =>
			bands.map((b) => {
				const fig = b.querySelector(':scope > .fig')!.getBoundingClientRect();
				const next = b.nextElementSibling!.getBoundingClientRect();
				return Math.round(next.top - fig.bottom);
			})
		);
		// Hero and 03 Agents & tools.
		expect(gaps.length, `at ${width}`).toBe(2);
		for (const g of gaps) expect(g, `at ${width}`).toBe(0);
	}
});

test('one numbering: the bands, the cells and the steps', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const heads = page.locator('main .band > .head');
	await expect(heads.locator(':scope > .num')).toHaveText([
		'01 How it works',
		'02 Workflow',
		'03 Agents & tools',
		'04 Pricing',
		'05 Questions',
		'06 Start'
	]);
	await expect(page.locator('#how .cols > li > .num')).toHaveText([
		'1.1 One-command sync',
		'1.2 Full permissions',
		'1.3 Snapshots'
	]);
	await expect(page.locator('#features .cell-copy > .num')).toHaveText([
		'2.1 Ports',
		'2.2 Browser',
		'2.3 Editor',
		'2.4 Away & idle'
	]);
	await expect(page.locator('.steps .step-title .label')).toHaveText(['6.1', '6.2', '6.3']);
	const m = await page.evaluate(() => {
		const box = (e: Element) => e.getBoundingClientRect();
		const head = document.querySelector('#install-h')!.closest('.head')!;
		const num = head.querySelector('.num')!;
		const n = num.querySelector('.n')!;
		const name = document.createRange();
		name.selectNodeContents(num.lastChild!);
		const step = document.querySelector('.steps .step-title')!;
		const stepName = document.createRange();
		stepName.selectNodeContents(step.lastChild!);
		return {
			num: box(num).left,
			h2: box(head.querySelector('h2')!).left,
			n: getComputedStyle(n).color,
			label: getComputedStyle(num).color,
			name: name.getBoundingClientRect().left,
			stepName: stepName.getBoundingClientRect().left
		};
	});
	expect(Math.abs(m.num - m.h2)).toBeLessThanOrEqual(1);
	expect(m.n).not.toBe(m.label);
	expect(Math.abs(m.name - m.stepName)).toBeLessThanOrEqual(1);
});

test('the footer links to the refund policy', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('contentinfo')).toHaveCount(1);
	await expect(
		page.getByRole('contentinfo').getByRole('link', { name: 'Refunds' })
	).toHaveAttribute('href', '/refunds');
});

test('the hero and own-hardware copy uphold the product rules and stay honest', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page).toHaveTitle('repose: isolated Linux machines for your coding agents');
	await expect(page.locator('meta[name="description"]')).toHaveAttribute(
		'content',
		/^repose run gives your repo an isolated Linux VM/
	);
	await expect(page.locator('.hero .eyebrow')).toHaveCount(0);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText(
		'Your coding agents, on a machine of their own'
	);
	await expect(page.locator('.hero .hk-lead')).toContainText('Claude Code, Codex');
	await expect(page.locator('.hero .hk-lead')).toContainText('Mac mini or x86 box');
	await expect(page.locator('.hero .hero-meta')).toContainText('7 days free');
	await expect(page.locator('.hero .hero-meta')).toContainText(
		'Bring your own agent subscriptions or API keys'
	);
	// No start time or Mac memory-sizing claims on own hardware (ENG-70, ENG-91).
	const ownNote = (await page.locator('#parts').textContent())!;
	for (const implied of [/\bseconds\b/, /\bminutes\b/, /\d+\s?GB of memory\b/])
		expect(ownNote).not.toMatch(implied);
	const text = (await page.locator('main').textContent())!;
	for (const gone of [
		'replicated in the cloud',
		'For solo founders',
		'A framework for running',
		'Build it in layers',
		'—'
	])
		expect(text).not.toContain(gone);
	for (const claim of [
		/burst/i,
		/bare metal/i,
		/repose trigger/i,
		/--size tiny/i,
		/\btiny class\b/i
	])
		expect(text).not.toMatch(claim);
});

test('the hero picture is the real repose run session, and holds under reduced motion', async ({
	browser
}) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/');
	const term = page.locator('.hero .term');
	await expect(term).toHaveAttribute('role', 'region');
	await expect(term).toHaveAttribute('aria-label', 'A repose run session');
	await expect(term).toContainText('repose run -p');
	await expect(term).toContainText('Ready in');
	const pane = (await term.boundingBox())!;
	expect(pane.height).toBeLessThanOrEqual(900 - 64);
	await expect(page.locator('.cmp')).toHaveCount(0);
	expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	await context.close();
});

test('the Orbit keeps its contract as a gallery candidate', async ({ browser }) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/?hero=orbitv1');
	const orb = page.locator('.hero .orb');
	await expect(orb).toHaveAttribute('role', 'img');
	await expect(orb).toHaveAttribute('aria-label', /^repose in the middle: one always-on machine\./);
	await expect(orb).toHaveAttribute('aria-label', /Claude Code, Codex, opencode, Gemini CLI and pi/);
	await expect(orb.locator('.ag .nm')).toHaveText([
		'Claude Code',
		'Codex',
		'opencode',
		'Gemini CLI',
		'pi'
	]);
	await expect(orb.locator('.ag .bub svg')).toHaveCount(5);
	await expect(orb.locator('.ag.active')).toHaveCount(1);
	await expect(orb.locator('.wake .chip')).toHaveText([
		'every night at 2',
		'an alert goes off',
		'a webhook arrives'
	]);
	await expect(orb.locator('.carry .chip')).toHaveText(['your code, tools, logins', 'your secrets']);
	await expect(orb.locator('.phone .q')).toHaveText('Roll back the last deploy?');
	const text = (await orb.textContent())!;
	for (const jargon of [/repose run/, /[A-Z]+_[A-Z]+/]) expect(text).not.toMatch(jargon);
	const box = (await orb.boundingBox())!;
	expect(box.height).toBeLessThanOrEqual(900 - 64);
	expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	await expect(page.locator('.orb-frame')).not.toHaveAttribute('data-motion', 'play');
	await context.close();
});
