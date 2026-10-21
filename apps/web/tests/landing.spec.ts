// DECISIONS I-290: the landing page's pricing section shows the three plans
// and one live line from GET /public/seats, and stands without it. I-656:
// the page sits on the frame every page shares, under the site header and
// over the site footer. I-837: the plans are PlanCells' public cards.
import { test, expect } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

test('the pricing section shows the three plans and the seats left', async ({ page }) => {
	await resetBilling();
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
	await expect(pricing.getByText('Launch prices. Seven days free.')).toBeVisible();
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
	// A seat is 8 GB of running memory, a count apart from Solo's founding places.
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats of 8 GB left');
	await expect(pricing.getByRole('button', { name: 'Start a free week' })).toBeVisible();
	// The notice period and the 12-month hold left the landing with I-772.
	// They are kept in terms.md (the site footer's Terms), billing.md and
	// PRICING.md; "Launch prices" stays, in the section's head.
	await expect(pricing.getByText('A change reaches you by email')).toHaveCount(0);
});

test('with no founding places left, Solo shows $49 and no founding line', async ({ page }) => {
	await setBilling({ founding_left: 0 });
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
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
	await expect(page.getByTestId('seats-line')).toHaveText('1 seat of 8 GB left');
	await resetBilling();
});

test('when full, the line says so with the number waiting', async ({ page }) => {
	await setBilling({ seats: { total: 30, held: 30, waiting: 41 } });
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
	// Signing up then joins the waitlist and starts no week, so the button says that.
	await expect(pricing.getByRole('button', { name: 'Join the waitlist' })).toBeVisible();
	await expect(pricing.getByRole('button', { name: 'Start a free week' })).toHaveCount(0);
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

// I-837 "each fact once": the install command shows in the hero alone, as
// the line under the buttons, and the hero's copy button copies exactly it.
test('the install command shows once, and its button copies it', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/');
	const install = 'curl -fsSL https://repose.herakraft.co/install.sh | sh';
	await expect(page.getByText(install, { exact: true })).toHaveCount(1);
	await expect(page.locator('main')).toContainText(install);
	expect(
		await page.locator('main').evaluate((m, i) => m.textContent!.split(i).length - 1, install)
	).toBe(1);
	await page.getByRole('button', { name: 'Copy install command' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(install);
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

test('the footer links to the refund policy', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('contentinfo')).toHaveCount(1);
	await expect(
		page.getByRole('contentinfo').getByRole('link', { name: 'Refunds' })
	).toHaveAttribute('href', '/refunds');
});
