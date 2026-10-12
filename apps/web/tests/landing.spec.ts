// DECISIONS I-290: the landing page's pricing section shows the three plans
// and one live line from GET /public/seats, and stands without it. I-656:
// the page sits on the frame every page shares, under the site header and
// over the site footer.
import { test, expect } from '@playwright/test';
import { setBilling, resetBilling } from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

test('the pricing section shows the three plans and the seats left', async ({ page }) => {
	await resetBilling();
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
	await expect(
		pricing.getByText(
			"Seven days free, card at checkout. Prices in USD, before tax. A plan's memory is what runs at the same time, around the clock, across up to 100 projects; a stopped project uses none. A plan's disk counts the data your projects hold."
		)
	).toBeVisible();
	await expect(pricing.locator('.tier h3')).toHaveText(['Solo', 'Plus', 'Pro']);
	await expect(pricing.getByText('$20')).toBeVisible();
	await expect(
		pricing.getByText('First 3 months for new subscribers, then $29 and 250 GB egress')
	).toBeVisible();
	await expect(pricing.getByText('$59')).toBeVisible();
	await expect(pricing.getByText('$99')).toBeVisible();
	await expect(pricing.getByText('8 GB running at once')).toBeVisible();
	await expect(pricing.getByText('100 GB disk · 100 GB egress')).toBeVisible();
	await expect(pricing.getByText('16 GB running at once')).toBeVisible();
	await expect(pricing.getByText('250 GB disk · 500 GB egress')).toBeVisible();
	await expect(pricing.getByText('32 GB running at once')).toBeVisible();
	await expect(pricing.getByText('500 GB disk · 1 TB egress')).toBeVisible();
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left');
	await expect(pricing.getByRole('button', { name: 'Start a free week' })).toBeVisible();
});

test('when full, the line says so with the number waiting', async ({ page }) => {
	await setBilling({ seats: { total: 30, held: 30, waiting: 41 } });
	await page.goto('/');
	await expect(page.getByTestId('seats-line')).toContainText('Full for now. 41 waiting;');
	await expect(page.getByTestId('seats-line')).toContainText(
		"join the list and you're emailed when a seat frees."
	);
});

test('the last step runs in your-project', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByText('cd ~/code/your-project && repose run')).toBeVisible();
	await expect(page.getByText('job-alerts')).toHaveCount(0);
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
