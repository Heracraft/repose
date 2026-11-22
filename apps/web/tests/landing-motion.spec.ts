// WCAG 2.2.2 motion contract (DECISIONS I-666): `Wreck` in `#safety` registers
// with the motion controller, mounts one `PauseButton` ("Pause motion") when
// motion is allowed, holds while scrolling and plays when still, respects
// keyboard toggling and scroll persistence, and stops completely under
// `prefers-reduced-motion: reduce`. `?hero=orbitv1` keeps its gallery contract.
import { test, expect } from '@playwright/test';

const frames = '.pic-frame';

test('the Safety Wreck animation registers Pause motion, holds while scrolling, and plays once still', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const safety = page.locator('#safety');
	await safety.scrollIntoViewIfNeeded();
	await expect(page.locator('button.pic-pause')).toHaveCount(1);
	const wreckFrame = safety.locator(frames);
	for (let i = 0; i < 10; i++) {
		await page.evaluate((d) => window.scrollBy(0, d), i % 2 ? -8 : 8);
		await page.waitForTimeout(150);
	}
	await expect(wreckFrame).toHaveAttribute('data-motion', 'hold');
	await expect(wreckFrame).toHaveAttribute('data-motion', 'play', { timeout: 3500 });
});

test('the Pause motion control is keyboard reachable and pauses and resumes cleanly', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const button = page.locator('button.pic-pause');
	await expect(button).toHaveText('Pause motion');
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	await button.focus();
	await expect(button).toBeFocused();
	await page.keyboard.press('Space');
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await expect(page.locator(`${frames}[data-motion="play"]`)).toHaveCount(0);
	await page.keyboard.press('Enter');
	await expect(button).toHaveAttribute('aria-pressed', 'false');
});

test('paused motion stays paused through scrolling out and back', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const safety = page.locator('#safety');
	await safety.scrollIntoViewIfNeeded();
	const button = page.locator('button.pic-pause');
	const wreckFrame = safety.locator(frames);
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await page.evaluate(() => window.scrollTo(0, 0));
	await page.waitForTimeout(300);
	await safety.scrollIntoViewIfNeeded();
	await page.waitForTimeout(1500);
	await expect(wreckFrame).toHaveAttribute('data-motion', 'hold');
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	await expect(wreckFrame).toHaveAttribute('data-motion', 'play', { timeout: 3500 });
});

test('reduced motion hides Pause motion and leaves zero running animations', async ({
	browser
}) => {
	const context = await browser.newContext({
		reducedMotion: 'reduce',
		viewport: { width: 1440, height: 900 }
	});
	const page = await context.newPage();
	await page.goto('/');
	await page.locator('#safety').scrollIntoViewIfNeeded();
	await page.waitForTimeout(600);
	await expect(page.locator('button.pic-pause')).toHaveCount(0);
	expect(await page.evaluate(() => document.getAnimations().length)).toBe(0);
	await context.close();
});

test('the Orbit gallery candidate holds while scrolling and plays once still', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/?hero=orbitv1');
	const orb = page.locator('.orb-frame');
	await expect(orb).toBeVisible();
	for (let i = 0; i < 10; i++) {
		await page.evaluate((d) => window.scrollBy(0, d), i % 2 ? -8 : 8);
		await page.waitForTimeout(150);
	}
	await expect(orb).toHaveAttribute('data-motion', 'hold');
	await expect(orb).toHaveAttribute('data-motion', 'play', { timeout: 3500 });
});
