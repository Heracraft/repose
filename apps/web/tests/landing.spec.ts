// DECISIONS I-290: the landing page's pricing section shows the three plans
// and one live line from GET /public/seats, and stands without it. I-656:
// the page sits on the frame every page shares, under the site header and
// over the site footer. I-837: the plans are PlanCells' public cards. I-961
// (option D): the section's head is the h2 "Launch prices".
import { test, expect } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

test('the pricing section shows the three plans and the seats left', async ({ page }) => {
	await resetBilling();
	await page.goto('/');
	const pricing = page.locator('section', {
		has: page.getByRole('heading', { name: 'Launch prices' })
	});
	await expect(pricing.locator('[data-testid^="plan-"] h3.p-name')).toHaveText([
		'Solo',
		'Plus',
		'Pro'
	]);
	await expect(pricing.getByText('$39')).toBeVisible();
	// The founding price is the two numbers, the list price struck (I-772).
	const solo = pricing.getByTestId('plan-solo');
	await expect(solo.locator('.p-price .was')).toHaveText('$49');
	await expect(solo.locator('.p-price .n')).toHaveText('$39');
	await expect(solo.locator('.p-then')).toHaveText('First 12 months, first 30 subscribers');
	await expect(solo.locator('.p-run')).toHaveText('8 GB running at once');
	await expect(solo.locator('.p-specs')).toHaveText('100 GB disk · 250 GB egress');
	// The public cards carry no button of their own; the row under them does.
	await expect(pricing.locator('[data-testid^="plan-"] :is(a, button)')).toHaveCount(0);
	await expect(pricing.getByText('$89')).toBeVisible();
	await expect(pricing.getByText('$169')).toBeVisible();
	await expect(pricing.getByText('8 GB running at once')).toBeVisible();
	await expect(pricing.getByText('100 GB disk · 250 GB egress')).toBeVisible();
	await expect(pricing.getByText('16 GB running at once')).toBeVisible();
	await expect(pricing.getByText('250 GB disk · 500 GB egress')).toBeVisible();
	await expect(pricing.getByText('32 GB running at once')).toBeVisible();
	await expect(pricing.getByText('500 GB disk · 1 TB egress')).toBeVisible();
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left');
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	// The notice period and the 12-month hold left the landing with I-772.
	// They are kept in terms.md (the site footer's Terms), billing.md and
	// PRICING.md; "Launch prices" stays, as the section's heading.
	await expect(pricing.getByText('A change reaches you by email')).toHaveCount(0);
});

test('with no founding places left, Solo shows $49 and no founding line', async ({ page }) => {
	await setBilling({ founding_left: 0 });
	await page.goto('/');
	const pricing = page.locator('#pricing');
	const solo = pricing.getByTestId('plan-solo');
	await expect(solo.locator('.p-price .n')).toHaveText('$49');
	await expect(solo.locator('.p-then')).toHaveCount(0);
	// Nothing is struck once the list price is the price (I-772).
	await expect(solo.locator('.p-price .was')).toHaveCount(0);
	await expect(pricing.getByText('$39')).toHaveCount(0);
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
	// The waitlist and seat copy stays as it is (spec 2.1, owner's call).
	await expect(pricing.getByRole('link', { name: 'Start a free week' })).toBeVisible();
	await expect(page.getByTestId('seats-line')).toContainText('Full for now. 41 waiting;');
	await expect(page.getByTestId('seats-line')).toContainText(
		"join the list and you're emailed when a seat frees."
	);
});

// Guide B 1.10: the row shows what its Copy puts on the clipboard, and it
// runs on a visitor's laptop (no `cd` into the terminal's example folder).
test('the run step shows and copies repose run alone', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const step = page.locator('.steps:not(.refs) li', { hasText: 'Run in any checkout' });
	await expect(step.locator('.code-copy')).toContainText('repose run');
	await expect(step).not.toContainText('cd ~/code/your-project');
	await step.getByRole('button', { name: 'Copy repose run' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose run');
	await expect(page.getByText('job-alerts')).toHaveCount(0);
});

// The install command shows twice: under the hero's buttons, and as the first
// of the three steps (I-843, the owner's call over I-837's "each fact once").
// The hero's copy button copies exactly it.
test('the install command shows in the hero and the first step, and its button copies it', async ({
	page,
	context
}) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const install = 'curl -fsSL https://repose.run/install.sh | sh';
	await expect(page.locator('.hero .install')).toHaveText(install);
	await expect(page.locator('.steps:not(.refs) li').first().locator('.code-copy')).toContainText(
		install
	);
	expect(
		await page.locator('main').evaluate((m, i) => m.textContent!.split(i).length - 1, install)
	).toBe(2);
	const button = page.getByRole('button', { name: 'Copy install command' });
	const handle = await button.elementHandle();
	await button.click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(install);
	// "Copied" centres with its check as one group in the wider button.
	const gaps = await handle!.evaluate((el) => {
		const box = el.getBoundingClientRect();
		const layer = el.querySelector('.layer:not(.off)')!;
		const icon = layer.querySelector('.icon')!.getBoundingClientRect();
		const range = document.createRange();
		range.selectNodeContents(layer);
		const all = range.getBoundingClientRect();
		return {
			word: layer.textContent!.trim(),
			iconFirst: Math.abs(icon.left - all.left) < 0.5,
			off: Math.abs(all.left - box.left - (box.right - all.right))
		};
	});
	expect(gaps.word).toBe('Copied');
	expect(gaps.iconFirst).toBe(true);
	expect(gaps.off).toBeLessThan(1);
});

// Below 1024px the headline starts under the logo, on the frame's text
// line. From 1024px the hero's copy stands centred over the Orbit (owner,
// 2026-11-15), so the headline stands on the picture's centre line
// instead, clear of the logo's.
test('the headline starts under the logo, and stands centred over the picture from 1024px', async ({
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
		const orb = (await page.locator('.hero .orb').boundingBox())!;
		const mid = (b: { x: number; width: number }) => b.x + b.width / 2;
		expect(Math.abs(mid(headline) - mid(orb)), `at ${width}`).toBeLessThanOrEqual(1);
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

// The hero's install line is the command as copied, on one line at 12px
// (never shrunk to fit), and where the column is narrower it scrolls
// inside its own box, so the page never widens.
test('the hero install line stays readable and inside the page on a phone', async ({ page }) => {
	for (const width of [360, 390]) {
		await page.setViewportSize({ width, height: 800 });
		await page.goto('/');
		const install = page.locator('.hero .install');
		await expect(install).toHaveCSS('font-size', '12px');
		const box = await install.evaluate((e) => ({
			lines: Math.round(e.clientHeight / parseFloat(getComputedStyle(e).lineHeight)),
			right: e.getBoundingClientRect().right
		}));
		expect(box.lines, `at ${width}`).toBe(1);
		expect(box.right, `at ${width}`).toBeLessThanOrEqual(width);
		const over = await page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(over, `at ${width}`).toBeLessThanOrEqual(0);
	}
});

// I-961: a band that ends on a picture rests its stage on the next band's
// rule, with no strip between them.
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
		// The centrepiece, the wreck, the setup and the agents (I-1167).
		expect(gaps.length, `at ${width}`).toBe(4);
		for (const g of gaps) expect(g, `at ${width}`).toBe(0);
	}
});

// I-961 (option D, the owner's pick): the page has one numbering, the
// mono labels over each band's h2 and each cell's title, and the pictures
// no figure bars. The page's one motion control (owner, 2026-11-12) shows
// at rest and is the element under its own centre (a clip on a field once
// cut a button away while its box and opacity said it was there); it
// stands in the hero field's top right corner, its right edge on the
// text line.
test('the motion control shows, takes the pointer, and stands in the hero field', async ({
	page
}) => {
	for (const width of [1440, 1024, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		await expect(page.locator('main .fig-bar')).toHaveCount(0);
		const buttons = page.locator('main .fig .pic-pause');
		await expect(buttons).toHaveCount(1);
		await expect(page.locator('.hero .fig .pic-pause')).toHaveCount(1);
		for (let i = 0; i < 1; i++) {
			const b = buttons.nth(i);
			await b.scrollIntoViewIfNeeded();
			const m = await b.evaluate((el) => {
				const r = el.getBoundingClientRect();
				const x = r.left + r.width / 2;
				const y = r.top + r.height / 2;
				const hit = document.elementFromPoint(x, y);
				const fig = el.closest('.fig')!;
				const title = fig.querySelector('.cell-copy > .item-title');
				const stage = fig.querySelector(':scope > .stage');
				const t = title?.getBoundingClientRect();
				const s = stage?.getBoundingClientRect();
				return {
					hit: hit === el || el.contains(hit),
					opacity: getComputedStyle(el).opacity,
					// the title line's middle against the button's
					titleMid: t ? Math.abs(t.top + 14 - y) : null,
					// the field's top padding holds the button
					inField: s
						? r.top >= s.top && r.bottom <= s.top + parseFloat(getComputedStyle(stage!).paddingTop)
						: null
				};
			});
			expect(m.hit, `button ${i} at ${width}`).toBe(true);
			expect(m.opacity).toBe('1');
			if (m.titleMid !== null) expect(m.titleMid, `button ${i} at ${width}`).toBeLessThanOrEqual(1);
			if (m.inField !== null) expect(m.inField, `button ${i} at ${width}`).toBe(true);
		}
	}
});

// I-961, the owner's pick (alternative C's labels in D): one numbering
// on the page. Each band after the hero opens on its number and name
// over its h2, each cell on its band's number over its title (1.1 to
// 1.6 and 4.1 to 4.4), and the install rows count under theirs (6.1 to
// 6.3); the label sits on the text line, and its name starts at the
// number's fixed width, where a step's name does too. I-1167 put the
// layers and the wreck before the setup, and I-1212's bands run 01 to
// 08, the job agent's band folded away.
test('one numbering: the bands, the cells and the steps', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const heads = page.locator('main .band > .head');
	await expect(heads.locator(':scope > .num')).toHaveText([
		'01 Layers',
		'02 Permissions',
		'03 Setup',
		'04 Machine',
		'05 Agents',
		'06 Install',
		'07 Pricing',
		'08 Questions'
	]);
	await expect(page.locator('#parts .cols > li > .num')).toHaveText([
		'1.1 Machine',
		'1.2 Walls',
		'1.3 Your setup',
		'1.4 Secrets',
		'1.5 Triggers',
		'1.6 Agent'
	]);
	await expect(page.locator('#features .cell-copy > .num')).toHaveText([
		'4.1 Snapshots',
		'4.2 Ports',
		'4.3 Browser',
		'4.4 Editor'
	]);
	await expect(page.locator('.steps:not(.refs) .step-title .label')).toHaveText([
		'6.1',
		'6.2',
		'6.3'
	]);
	const m = await page.evaluate(() => {
		const box = (e: Element) => e.getBoundingClientRect();
		const head = document.querySelector('#install-h')!.closest('.head')!;
		const num = head.querySelector('.num')!;
		const n = num.querySelector('.n')!;
		const name = document.createRange();
		name.selectNodeContents(num.lastChild!);
		const step = document.querySelector('.steps:not(.refs) .step-title')!;
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

// The setup picture's rows in flight are measured against the panels; a
// window narrowed after load must not leave one parked past the page's
// edge (review round 1).
test('narrowing the window after load adds no sideways scroll', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await page.locator('.oc').scrollIntoViewIfNeeded();
	await page.waitForTimeout(2500);
	for (const width of [1024, 768, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.waitForTimeout(600);
		const over = await page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(over, `at ${width}`).toBeLessThanOrEqual(0);
	}
});

test('the footer links to the refund policy', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('contentinfo')).toHaveCount(1);
	await expect(
		page.getByRole('contentinfo').getByRole('link', { name: 'Refunds' })
	).toHaveAttribute('href', '/refunds');
});

// DECISIONS I-1169: the page sells a platform for running your own
// always-on agents, built in layers. The h1 names it, the lead names the
// machine and what you add to it, and six cells build it bottom to top.
// Nothing names a command or a class that does not exist, and the
// earlier headlines are gone. The job-application agent lost its band
// in the hero loop's round 8 (I-1212): it is a trigger in the Orbit and
// the triggers cell now.
test('the hero and the layers say what repose is now', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page).toHaveTitle('repose: a platform for running your own always-on agents');
	await expect(page.locator('meta[name="description"]')).toHaveAttribute(
		'content',
		/^Run your own always-on agents: pick a machine, bring any agent/
	);
	// The eyebrow is gone (owner, 2026-11-15).
	await expect(page.locator('.hero .eyebrow')).toHaveCount(0);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText(
		'A platform for running your own always-on agents'
	);
	await expect(page.locator('.hero .hk-lead')).toHaveText(
		'Give your agent a machine of its own, then add what it needs: your dev environment, secrets, a schedule or a webhook that wakes it. It keeps working with your laptop shut.'
	);
	await expect(page.locator('#parts .cols .item-title')).toHaveText([
		'Size it to the job',
		'Wall it off from your laptop',
		'Bring your dev environment',
		'Give it the keys you choose',
		'Start it on a schedule or a webhook',
		'Bring any agent'
	]);
	await expect(page.locator('#parts .cols .cmd')).toHaveText([
		'repose resize --size large',
		'repose run',
		'repose secrets set NAME',
		'repose notify set --ntfy URL'
	]);
	const text = (await page.locator('main').textContent())!;
	for (const gone of ['replicated in the cloud', 'For solo founders', 'A framework for running'])
		expect(text).not.toContain(gone);
	// Never on the page, and no command or class that does not exist.
	for (const claim of [
		/burst/i,
		/bare metal/i,
		/your own hardware/i,
		/repose trigger/i,
		/--size tiny/i,
		/\btiny class\b/i
	])
		expect(text).not.toMatch(claim);
});

// The hero's picture (LANDING.md "The hero's picture") is the Orbit:
// repose in the middle, the five agents as bubbles on a ring around it,
// and in plain words what wakes it and what it carries. Named for a
// screen reader, with no command or variable name in it (owner,
// 2026-11-12), and whole in the first screen at 1440. Under reduced
// motion it holds on its still frame: nothing is animating, and the
// controller has let it go. The three lanes it replaced (Compose, the
// hero of rounds 1 to 8) are not on the page.
test('the hero picture is the Orbit, named in plain words, and holds under reduced motion', async ({
	browser
}) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/');
	const orb = page.locator('.hero .orb');
	await expect(orb).toHaveAttribute('role', 'img');
	await expect(orb).toHaveAttribute('aria-label', /^repose in the middle: one always-on machine\./);
	await expect(orb).toHaveAttribute('aria-label', /Claude Code, Codex, opencode, Gemini CLI and pi/);
	// The five agents, each its bubble and its name; one of them lit, the
	// one the loop's alert wakes.
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
	// It asks you on your phone, in the words notifications.md uses.
	await expect(orb.locator('.phone .q')).toHaveText('Roll back the last deploy?');
	const text = (await orb.textContent())!;
	for (const jargon of [/repose run/, /[A-Z]+_[A-Z]+/]) expect(text).not.toMatch(jargon);
	// Whole in the first screen, under the copy, and nothing of the three
	// lanes left on the page.
	const box = (await orb.boundingBox())!;
	expect(box.height).toBeLessThanOrEqual(900 - 64);
	await expect(page.locator('.cmp')).toHaveCount(0);
	// Reduced motion: the still frame, with no loop behind it.
	expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	await expect(page.locator('.orb-frame')).not.toHaveAttribute('data-motion', 'play');
	await expect(page.locator('button.pic-pause')).toHaveCount(0);
	await context.close();
});
