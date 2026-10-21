// DECISIONS I-837 (Guide B 10.4): on the landing only the hero terminal
// moves, once, when it comes into view; nothing else animates on load or on
// scroll, and under reduced motion nothing animates at all.
import { test, expect, type Page } from '@playwright/test';

const running = (page: Page) =>
	page.evaluate(() =>
		document
			.getAnimations()
			.filter((a) => a.playState === 'running')
			.map((a) => {
				const t = (a.effect as KeyframeEffect | null)?.target as Element | null;
				return `${t?.tagName}.${t?.className}`;
			})
	);

async function scrollThrough(page: Page) {
	const h = await page.evaluate(() => document.documentElement.scrollHeight);
	for (let y = 0; y <= h; y += 400) {
		await page.evaluate((y) => window.scrollTo(0, y), y);
		await page.waitForTimeout(60);
	}
}

test('under reduced motion nothing animates on the landing', async ({ page }) => {
	await page.emulateMedia({ reducedMotion: 'reduce' });
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		await page.waitForLoadState('networkidle');
		expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
		await scrollThrough(page);
		expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	}
});

test('the terminal plays once and then stops', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page.locator('.term .ln.on').first()).toBeVisible();
	await page.waitForTimeout(6000);
	expect(await running(page)).toEqual([]);
	// Scrolling away and back does not start it again.
	await scrollThrough(page);
	await page.evaluate(() => window.scrollTo(0, 0));
	await page.waitForTimeout(300);
	expect(await running(page)).toEqual([]);
	await expect(page.locator('.term .ln').last()).toHaveCSS('opacity', '1');
});

test('below the fold the terminal waits until it is in view', async ({ page }) => {
	// A short phone: the pane starts under the fold.
	await page.setViewportSize({ width: 390, height: 640 });
	await page.goto('/');
	const term = page.locator('.term');
	await expect(term).toHaveClass(/playing/);
	await page.waitForTimeout(1500);
	// Not yet scrolled to: the lines wait, hidden, at full height.
	await expect(page.locator('.term .ln.on')).toHaveCount(0);
	await term.scrollIntoViewIfNeeded();
	await expect(page.locator('.term .ln').last()).toHaveCSS('opacity', '1', { timeout: 8000 });
});

test('nothing else on the page animates on scroll', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await page.waitForTimeout(6000);
	await page.evaluate(() => {
		(window as unknown as { seen: string[] }).seen = [];
		document.addEventListener('animationstart', (e) =>
			(window as unknown as { seen: string[] }).seen.push((e.target as Element).className)
		);
		document.addEventListener('transitionstart', (e) =>
			(window as unknown as { seen: string[] }).seen.push((e.target as Element).className)
		);
	});
	await scrollThrough(page);
	expect(await page.evaluate(() => (window as unknown as { seen: string[] }).seen)).toEqual([]);
});

// review-landing M1: a pane mounted by a client-side navigation back to `/`
// (the header logo, Pricing, Back) replays like a first load, rather than
// staying blank until the 5s CSS fallback.
test('after a client-side navigation back to the landing, the terminal plays', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs');
	await page.waitForTimeout(6000);
	await page.locator('header a[href="/"]').first().click();
	await expect(page).toHaveURL(/\/$/);
	await expect(page.locator('.term')).toHaveClass(/playing/, { timeout: 1000 });
	await expect(page.locator('.term .ln').first()).toHaveCSS('opacity', '1', { timeout: 1000 });
	await expect(page.locator('.term .ln').last()).toHaveCSS('opacity', '1', { timeout: 8000 });
});
