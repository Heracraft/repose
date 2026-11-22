// Council L1: the landing page makes no Nix claim anywhere in <main> (I-1520).
// The owner cut the Ubuntu spec from the page (direction.md A4), so the hero
// no longer names it.
import { test, expect } from '@playwright/test';

test('the landing page makes no Nix claim and shows no spec label in the hero', async ({
	page
}) => {
	await page.goto('/');
	await expect(page.locator('.hero .hv')).not.toContainText('Ubuntu 24.04');
	await expect(page.locator('main')).not.toContainText(/\bNix\b/);
});
