// DECISIONS I-1452: the hero is the Quickstart's real `repose run` terminal
// inside the 1200px sheet, with zero looping cartoons on `/`. Without JS the
// entire hero terminal, static Localhost/Browser captures, labeled agents, and
// pricing render complete; at 1440x900 and 390x844 the hero CTA, install bar,
// and terminal all land above the fold.
import { test, expect } from '@playwright/test';

test.describe('without scripts', () => {
	test.use({ javaScriptEnabled: false });

	test('the server-rendered landing shows the full hero terminal, captures and pricing', async ({
		page
	}) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/');
		const term = page.locator('.hero .term');
		await expect(term).toContainText('cd ~/code/your-project');
		await expect(term).toContainText('repose run -p');
		await expect(term).toContainText('Ready in 16s.');
		await expect(page.locator('#features .lh')).toBeVisible();
		await expect(page.locator('#features .bstage')).toBeVisible();
		await expect(page.locator('#agents .agents .aname')).toHaveCount(5);
		await expect(page.locator('#pricing [data-testid="plan-solo"]')).toContainText('$39');
		await expect(page.locator('#pricing [data-testid="plan-own"]')).toContainText('$10');
	});
});

for (const [width, height] of [
	[1440, 900],
	[390, 844]
] as const) {
	test(`the hero CTA, install command and terminal fit in the first screen at ${width}x${height}`, async ({
		page
	}) => {
		await page.setViewportSize({ width, height });
		await page.goto('/');
		const m = await page.evaluate(() => {
			const r = (sel: string) => document.querySelector(sel)!.getBoundingClientRect();
			return {
				cta: r('.hero .hk-btn'),
				install: r('.hero .hero-install .code-copy'),
				term: r('.hero .term')
			};
		});
		expect(m.cta.bottom, `cta at ${width}`).toBeLessThanOrEqual(height);
		expect(m.install.bottom, `install at ${width}`).toBeLessThanOrEqual(height);
		expect(m.term.bottom, `terminal at ${width}`).toBeLessThanOrEqual(height);
	});
}

test('total desktop page height stays compact under 5,000px', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	expect(h).toBeLessThan(5000);
});
