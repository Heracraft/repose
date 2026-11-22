// Council L1: the landing page says what new machines run, the Ubuntu base
// and its package menu, and makes no Nix claim (I-1520).
import { test, expect } from '@playwright/test';

test('the landing page names Ubuntu and apt, not Nix', async ({ page }) => {
	await page.goto('/');
	const band = page.locator('#agents');
	await expect(band).toContainText('Every machine runs Ubuntu 24.04.');
	await expect(band).toContainText('any apt package');
	await expect(page.locator('main')).not.toContainText(/\bNix\b/);
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await band.locator('.fig').evaluate((el) => el.scrollIntoView({ block: 'center' }));
			await page.waitForTimeout(300);
			await page.screenshot({ path: `${dir}/landing-installed-${width}-${scheme}.png` });
		}
	}
});
