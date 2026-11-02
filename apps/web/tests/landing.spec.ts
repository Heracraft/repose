// DECISIONS I-290: the landing page's pricing section shows the three plans
// and one live line from GET /public/seats, and stands without it. I-656:
// the page sits on the frame every page shares, under the site header and
// over the site footer. I-837: the plans are PlanCells' public cards. I-961
// (option D): the section's head is its label, "05 Pricing", over the h2
// "Launch prices".
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
	await expect(pricing.locator('.sec-label')).toHaveText('05Pricing');
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
	const step = page.locator('.steps li', { hasText: 'Run in any checkout' });
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
	const install = 'curl -fsSL https://repose.herakraft.co/install.sh | sh';
	await expect(page.locator('.hero .install')).toHaveText(install);
	await expect(page.locator('.steps li').first().locator('.code-copy')).toContainText(install);
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

test('the headline starts under the logo', async ({ page }) => {
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const logo = await page
			.getByRole('link', { name: 'repose, home' })
			.locator('svg')
			.filter({ visible: true })
			.first()
			.boundingBox();
		const headline = await page.locator('h1').boundingBox();
		expect(Math.abs(logo!.x - headline!.x), `at ${width}`).toBeLessThanOrEqual(1);
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
		expect(gaps.length, `at ${width}`).toBe(3);
		for (const g of gaps) expect(g, `at ${width}`).toBe(0);
	}
});

// I-961 (option D): every band after the hero opens on its numbered
// label, and every picture sits under a 40px figure bar holding its
// number; the pause button is centred in that bar at the content edge,
// never on a floor under the picture.
test('each band has its label and each picture its figure bar', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page.locator('main .sec-label')).toHaveText([
		'01Setup',
		'02Machine',
		'03Agents',
		'04Install',
		'05Pricing',
		'06Questions'
	]);
	await expect(page.locator('main .fig-bar')).toHaveText([
		'Fig. 0',
		'Fig. 1',
		'Fig. 2.1',
		'Fig. 2.2',
		'Fig. 2.3',
		'Fig. 2.4',
		'Fig. 3'
	]);
	const bars = await page
		.locator('main .fig-bar')
		.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().height));
	for (const h of bars) expect(h).toBe(40);
	await expect(page.locator('.fig .pic-pause')).toHaveCount(5);
	const m = await page.locator('.fig .pic-pause').evaluateAll((els) =>
		els.map((b) => {
			const r = b.getBoundingClientRect();
			const bar = b.closest('.fig')!.querySelector('.fig-bar')!.getBoundingClientRect();
			return { top: r.top - bar.top, bottom: bar.bottom - r.bottom, right: Math.round(r.right) };
		})
	);
	const content = await page.locator('main').evaluate((e) => {
		const r = e.getBoundingClientRect();
		return Math.round(r.right - parseFloat(getComputedStyle(e).paddingRight));
	});
	for (const b of m) {
		expect(Math.abs(b.top - b.bottom)).toBeLessThanOrEqual(1);
		expect(b.top).toBeGreaterThanOrEqual(0);
	}
	// The hero's, the setup's and the right cells' buttons end on the
	// content edge.
	expect(m.filter((b) => Math.abs(b.right - content) <= 1)).toHaveLength(3);
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
