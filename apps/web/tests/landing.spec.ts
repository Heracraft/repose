// DECISIONS I-290, I-656, I-663: the landing's pricing draws the three
// plans from lib/plans.ts with a button each and one live reading from
// GET /public/seats, and stands without it; the page sits on the frame
// every page shares.
import { test, expect, type Page } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

const pricingOf = (page: Page) =>
	page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });

test('the pricing section shows the three plans, a button each and the seats left', async ({
	page
}) => {
	await resetBilling();
	await page.goto('/');
	const pricing = pricingOf(page);
	await expect(
		pricing.getByText('Seven days free, card at checkout. Prices in USD, before tax.', {
			exact: true
		})
	).toBeVisible();
	const plans = pricing.getByRole('list', { name: 'Plans' });
	await expect(plans.locator('h3')).toHaveText(['Solo', 'Plus', 'Pro']);
	const solo = page.getByTestId('plan-solo');
	await expect(solo.getByText('8 GB running at once')).toBeVisible();
	await expect(solo.getByTestId('intro-solo')).toHaveText('$20 for 3 months');
	await expect(solo.getByText('$20', { exact: true })).toBeVisible();
	await expect(solo.getByText('then $29 and 250 GB egress from month 4')).toBeVisible();
	const plus = page.getByTestId('plan-plus');
	await expect(plus.getByText('16 GB running at once')).toBeVisible();
	await expect(plus.getByText('$59', { exact: true })).toBeVisible();
	await expect(plus.getByText('250 GB', { exact: true })).toBeVisible();
	const pro = page.getByTestId('plan-pro');
	await expect(pro.getByText('32 GB running at once')).toBeVisible();
	await expect(pro.getByText('$99', { exact: true })).toBeVisible();
	await expect(pro.getByText('1 TB', { exact: true })).toBeVisible();
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left');
	for (const name of ['Start Solo', 'Start Plus', 'Start Pro']) {
		await expect(pricing.getByRole('button', { name })).toBeVisible();
	}
	// One large button on the page, the hero's (docs/LANDING.md "Pricing").
	await expect(pricing.locator('.btn--lg')).toHaveCount(0);
	await expect(pricing.getByText('Snapshots kept 7 days')).toBeVisible();
});

test('the three cells end on one line, their rows level', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const tops = async (sel: string) =>
		page
			.locator(`[data-testid^="plan-"] ${sel}`)
			.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)));
	for (const sel of ['.p-name', '.p-price', '.p-act']) {
		const t = await tops(sel);
		expect(t, sel).toHaveLength(3);
		expect(new Set(t).size, `${sel} at ${t}`).toBe(1);
	}
	const bottoms = await page
		.locator('[data-testid^="plan-"]')
		.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().bottom)));
	expect(new Set(bottoms).size).toBe(1);
});

test('when full, the buttons join the waitlist and the reading counts who waits', async ({
	page
}) => {
	await setBilling({ seats: { total: 30, held: 30, waiting: 41 } });
	await page.goto('/');
	await expect(page.getByTestId('seats-line')).toHaveText(
		"41 waiting. You're emailed when a seat frees."
	);
	await expect(pricingOf(page).getByRole('button', { name: 'Join the waitlist' })).toHaveCount(3);
	await expect(pricingOf(page).getByRole('button', { name: /^Start / })).toHaveCount(0);
	await resetBilling();
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
		const headline = await page.locator('.hero-h').boundingBox();
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
