// Council L1: the landing page names Ubuntu for its sandboxes and makes no Nix
// claim anywhere in <main> (I-1520).
import { test, expect } from '@playwright/test';

test('the landing page names Ubuntu and makes no Nix claim', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('.hero .hv')).toContainText('Isolated Ubuntu sandbox');
	await expect(page.locator('#agents')).toContainText('Ubuntu');
	await expect(page.locator('main')).not.toContainText(/\bNix\b/);
});
