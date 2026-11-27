// Landing hero, SSR completeness (no-JS), above-the-fold framing at 1440x900
// and 390x844, and compact reduced-motion page height.
import { test, expect } from '@playwright/test';
import { cleanSlate } from './helpers';

cleanSlate();

test.describe('without scripts', () => {
	test.use({ javaScriptEnabled: false });

	test('the server-rendered landing shows the full HeroVisual, ScrollWheel, Safety and Pricing', async ({
		page
	}) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/');
		const hv = page.locator('.hero .hv');
		await expect(hv).toContainText('Your laptop');
		await expect(hv).toContainText('snapshots');
		await expect(page.locator('#agents')).toContainText('Pick where each sandbox runs');
		const wreck = page.locator('#safety .hero-pic');
		await expect(wreck).toHaveAttribute('role', 'img');
		await expect(wreck.locator('.side.laptop')).toBeVisible();
		await expect(wreck.locator('.side.right')).toBeVisible();
		await expect(page.locator('#pricing [data-testid="plan-solo"]')).toContainText('$39');
		await expect(page.locator('#pricing [data-testid="plan-own"]')).toContainText('$10');
	});
});

test('the hero headline, CTA and HeroVisual fit in the first screen at 1440x900', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const m = await page.evaluate(() => {
		const r = (sel: string) => document.querySelector(sel)!.getBoundingClientRect();
		return {
			cta: r('.hero .hk-btn'),
			hv: r('.hero .hv')
		};
	});
	expect(m.cta.bottom).toBeLessThanOrEqual(900);
	expect(m.hv.bottom).toBeLessThanOrEqual(900);
});

test('the hero CTA and top of HeroVisual land above the fold on a phone at 390x844', async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');
	const m = await page.evaluate(() => {
		const r = (sel: string) => document.querySelector(sel)!.getBoundingClientRect();
		return {
			cta: r('.hero .hk-btn'),
			hv: r('.hero .hv')
		};
	});
	expect(m.cta.bottom).toBeLessThanOrEqual(844);
	expect(m.hv.top).toBeLessThan(844);
});

test('under reduced motion the desktop page collapses the scroll track and stays compact', async ({
	browser
}) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/');
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	expect(h).toBeLessThan(4500);
	await context.close();
});
