// Landing page contract: 5-chapter layout (Hero + HeroVisual, 01 How it works
// ScrollWheel, 02 Safety Wreck, 03 Pricing + the Mac mini or Linux server row, 04 Start
// with a Cloud / Your machine toggle), live pricing from lib/plans.ts +
// GET /public/seats, strict honesty rules, zero horizontal overflow, and
// preserved `?hero=orbitv1` gallery contract.
import { test, expect } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

test('the pricing section shows the three plans and what each runs at once', async ({ page }) => {
	await resetBilling();
	await page.goto('/');
	const pricing = page.locator('section#pricing');
	await expect(pricing.getByRole('heading', { name: 'Pay for what runs at once' })).toBeVisible();
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
	await expect(pricing.locator('.plans--public [data-testid^="plan-"] :is(a, button)')).toHaveCount(
		0
	);
	await expect(pricing.getByText('$89')).toBeVisible();
	await expect(pricing.getByText('$169')).toBeVisible();
	await expect(pricing.getByText('16 GB running at once')).toBeVisible();
	await expect(pricing.getByText('250 GB disk · 500 GB egress')).toBeVisible();
	await expect(pricing.getByText('32 GB running at once')).toBeVisible();
	await expect(pricing.getByText('500 GB disk · 1 TB egress')).toBeVisible();
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	// Each plan's memory drawn as the 4 GB sandboxes it runs at once.
	await expect(pricing.getByTestId('mem-solo')).toContainText('Two 4 GB sandboxes');
	await expect(pricing.getByTestId('mem-plus')).toContainText('Four 4 GB sandboxes');
	await expect(pricing.getByTestId('mem-pro')).toContainText('Eight 4 GB sandboxes');
	await expect(pricing.locator('[data-testid="mem-solo"] .mem-bar i.on')).toHaveCount(2);
	// The seats line and the button stay in pricing (owner, 2026-11-22; I-1552).
	await expect(page.getByTestId('seats-line')).toHaveText('18 cloud seats left');
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	await expect(pricing.locator('.pricing-lead')).toHaveText(
		"Stopped sandboxes don't count against your memory."
	);
	await expect(pricing.getByText('A change reaches you by email')).toHaveCount(0);
});

test('the pricing section offers a Mac mini or Linux server as a flush row under the plans', async ({
	page
}) => {
	await resetBilling();
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	const own = pricing.getByTestId('plan-own');
	await expect(own.getByRole('heading', { level: 3, name: 'Your machine' })).toBeVisible();
	await expect(own.locator('.hw-price .n')).toHaveText('$10');
	await expect(own).toContainText(
		'Any number of machines. Each runs as many sandboxes as its memory and disk hold.'
	);
	await expect(own).not.toContainText(/own hardware|box you own|x86/i);
	await expect(own.locator(':is(a, button)')).toHaveCount(0);
	await expect(pricing.locator('[data-testid^="plan-"] h3.p-name')).toHaveCount(3);
	// The plan cells' canvas: no fill of its own, one rule above, no box.
	await expect(own).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
	await expect(own).toHaveCSS('border-left-width', '0px');
	const [ownName, ownWhat, plus] = await Promise.all([
		own.locator('.hw-name').boundingBox(),
		own.locator('.hw-what').boundingBox(),
		pricing.getByTestId('plan-plus').locator('.p-name').boundingBox()
	]);
	expect(ownWhat!.x).toBeGreaterThanOrEqual(ownName!.x + ownName!.width);
	expect(Math.abs(ownWhat!.x - plus!.x)).toBeLessThanOrEqual(16);
});

test('with no founding places left, Solo shows $49 and no founding line', async ({ page }) => {
	await setBilling({ founding_left: 0 });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	const solo = pricing.getByTestId('plan-solo');
	await expect(solo.locator('.p-price .n')).toHaveText('$49');
	await expect(solo.locator('.p-then')).toHaveCount(0);
	await expect(solo.locator('.p-price .was')).toHaveCount(0);
	await setBilling({ founding_left: 30 });
});

test('one seat free: the line is singular', async ({ page }) => {
	await setBilling({ seats: { total: 30, held: 29, waiting: 0 } });
	await page.goto('/');
	await expect(page.getByTestId('seats-line')).toHaveText('1 cloud seat left');
	await resetBilling();
});

test('when full, the line says so with the number waiting and notes your own machine has no waitlist', async ({
	page
}) => {
	await setBilling({ seats: { total: 30, held: 30, waiting: 41 } });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	await expect(page.getByTestId('seats-line')).toHaveText(
		'Cloud full for now, 41 waiting. Your machine has no waitlist.'
	);
});

test('the start section toggles between Cloud and your machine and copies commands cleanly', async ({
	page,
	context
}) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const install = page.locator('#install');
	const cloudTab = install.getByRole('tab', { name: 'Cloud' });
	const ownTab = install.getByRole('tab', { name: 'Your machine' });
	await expect(install.getByRole('heading', { level: 2 })).toHaveText('Run it in your repo');
	await expect(cloudTab).toHaveAttribute('aria-selected', 'true');
	await expect(ownTab).toHaveAttribute('aria-selected', 'false');

	const step2 = install.locator('.steps li').nth(1);
	await expect(step2).toContainText('Sign in');
	await expect(step2.locator('.code-copy')).toContainText('repose login');

	const step3 = install.locator('.steps li', { hasText: 'Run in a repo' });
	await expect(step3.locator('.code-copy')).toContainText('repose run');
	await expect(step3).not.toContainText('cd ~/code/your-project');
	await step3.getByRole('button', { name: 'Copy repose run' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose run');

	await ownTab.click();
	await expect(ownTab).toHaveAttribute('aria-selected', 'true');
	await expect(install.getByRole('heading', { level: 2 })).toHaveText('Run it in your repo');
	// Adding a machine needs an account first, so sign-in stays the second step.
	await expect(step2).toContainText('Sign in');
	await expect(step2.locator('.code-copy')).toContainText('repose login');
	const step3own = install.locator('.steps li').nth(2);
	await expect(step3own).toContainText('Add your machine');
	await expect(step3own.locator('.code-copy')).toContainText('repose host add');
	await step3own.getByRole('button', { name: 'Copy the host add command' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose host add');
});

// The install command shows twice, under the hero's buttons and as the first
// step (I-843, kept by the owner in I-1552). Its copy button copies the exact
// command without the `$ ` prompt.
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

test('the headline aligns with the logo edge and sits beside the HeroVisual from 1024px', async ({
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
		expect(Math.abs(logo.x - headline.x), `at ${width}`).toBeLessThanOrEqual(2);
		const hv = (await page.locator('.hero .hv').boundingBox())!;
		if (width >= 1024) {
			expect(hv.x, `at ${width}`).toBeGreaterThan(headline.x + headline.width - 10);
		} else {
			expect(hv.y, `at ${width}`).toBeGreaterThan(headline.y + headline.height);
		}
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

test('one numbering: the chapters and start steps', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page.locator('#agents .sw-desktop .num')).toHaveText('01 Setup');
	await expect(page.locator('main .band > .head > .num')).toHaveText([
		'02 Safety',
		'03 Pricing',
		'04 Start'
	]);
	// The steps are an ordered list; they carry no 4.n numbers of their own.
	await expect(page.locator('.steps .step-title .label')).toHaveCount(0);
	await expect(page.locator('.steps .step-title')).toHaveText([
		'Install',
		'Sign in',
		'Run in a repo'
	]);
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
	await expect(page).toHaveTitle('repose: sandboxes for your coding agents');
	await expect(page.locator('meta[name="description"]')).toHaveAttribute(
		'content',
		/isolated Linux VMs with whole-disk undo/
	);
	await expect(page.locator('.hero .eyebrow')).toHaveCount(0);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('A sandbox per repo, with undo');
	await expect(page.locator('.hero .hk-lead')).toContainText('a Linux VM set up like your laptop');
	await expect(page.locator('.hero .hk-lead')).toContainText('Mac mini or Linux server');
	await expect(page.locator('.hero .hk-lead')).toContainText('full permissions');
	await expect(page.locator('.hero .hero-meta')).toContainText('7 days free');
	await expect(page.locator('.hero .hero-meta')).toContainText('Cloud from $39/mo');
	await expect(page.locator('.hero .hero-meta')).toContainText(
		'Your Mac mini or Linux server $10/mo'
	);
	// The agents are drawn by their marks alone, named only for hover and screen readers.
	const agents = page.locator('.hero .hv .hv-agents li');
	await expect(agents).toHaveCount(5);
	await expect(agents).toHaveText(['', '', '', '', '']);
	expect(await agents.evaluateAll((l) => l.map((e) => e.getAttribute('title')))).toEqual([
		'Claude Code',
		'Codex',
		'opencode',
		'Gemini CLI',
		'pi'
	]);
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
		'No token markup',
		'Full permissions',
		'get pinged',
		'—'
	])
		expect(text).not.toContain(gone);
	// "Own hardware" is the plan's billing name; the landing says what the machine is.
	expect(text).not.toMatch(/own hardware/i);
	for (const claim of [
		/burst/i,
		/bare metal/i,
		/repose trigger/i,
		/--size tiny/i,
		/\btiny class\b/i
	])
		expect(text).not.toMatch(claim);
});

test('the hero visual renders cleanly and holds zero animations under reduced motion', async ({
	browser
}) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/');
	const hv = page.locator('.hero .hv');
	await expect(hv).toHaveAttribute('aria-label', /SSH keys and documents stay on the laptop/);
	await expect(hv).toContainText('Your laptop');
	await expect(hv).toContainText('repose run');
	await expect(hv).toContainText('snapshots');
	const pane = (await hv.boundingBox())!;
	expect(pane.height).toBeLessThanOrEqual(900 - 64);
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
	await expect(orb).toHaveAttribute(
		'aria-label',
		/Claude Code, Codex, opencode, Gemini CLI and pi/
	);
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
	await expect(orb.locator('.carry .chip')).toHaveText([
		'your code, tools, logins',
		'your secrets'
	]);
	await expect(orb.locator('.phone .q')).toHaveText('Roll back the last deploy?');
	const text = (await orb.textContent())!;
	for (const jargon of [/repose run/, /[A-Z]+_[A-Z]+/]) expect(text).not.toMatch(jargon);
	const box = (await orb.boundingBox())!;
	expect(box.height).toBeLessThanOrEqual(900 - 64);
	expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	await expect(page.locator('.orb-frame')).not.toHaveAttribute('data-motion', 'play');
	await context.close();
});
