// DECISIONS I-1452 & I-666: the default landing page has zero continuously
// looping pictures, so `PauseButton` is absent on `/`, `animejs` is not needed
// for `/` captures, and reduced motion holds zero running animations. Looping
// `?hero=` gallery candidates (such as `orbitv1`) still register with the
// motion controller and keep the WCAG 2.2.2 Pause motion control contract.
import { test, expect, type Page } from '@playwright/test';

const frames = '.pic-frame';

async function loadLoopingHero(page: Page) {
	await page.goto('/?hero=orbitv1');
	await expect(page.locator('button.pic-pause')).toHaveCount(1);
	await expect(page.locator(`${frames}[data-motion]`)).toHaveCount(1);
}

test('the default landing page runs no continuous loops and shows no pause button', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await page.waitForTimeout(1500);
	await expect(page.locator('button.pic-pause')).toHaveCount(0);
	await expect(page.locator(`${frames}[data-motion="play"]`)).toHaveCount(0);
});

test('the Orbit holds while the page moves and plays once it is still', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await loadLoopingHero(page);
	const orb = page.locator('.orb-frame');
	for (let i = 0; i < 14; i++) {
		await page.evaluate((d) => window.scrollBy(0, d), i % 2 ? -8 : 8);
		await page.waitForTimeout(150);
	}
	await expect(orb).toHaveAttribute('data-motion', 'hold');
	await expect(orb).toHaveAttribute('data-motion', 'play', { timeout: 3000 });
});

test('the motion control on a looping hero candidate is keyboard reachable and holds it', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await loadLoopingHero(page);
	const button = page.locator('button.pic-pause');
	await expect(button).toHaveText('Pause motion');
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	for (let i = 0; i < 60; i++) {
		await page.keyboard.press('Tab');
		if (await button.evaluate((b) => b === document.activeElement)) break;
	}
	await expect(button).toBeFocused();
	await page.keyboard.press('Space');
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await expect(page.locator(`${frames}[data-motion="play"]`)).toHaveCount(0);
	await page.keyboard.press('Enter');
	await expect(button).toHaveAttribute('aria-pressed', 'false');
});

test('paused motion on a looping candidate stays paused through scrolling out and back', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await loadLoopingHero(page);
	const button = page.locator('button.pic-pause');
	const orb = page.locator('.orb-frame');
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await page.evaluate(() => window.scrollTo(0, 1500));
	await page.waitForTimeout(300);
	await page.evaluate(() => window.scrollTo(0, 0));
	await page.waitForTimeout(1500);
	await expect(orb).toHaveAttribute('data-motion', 'hold');
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	await expect(orb).toHaveAttribute('data-motion', 'play', { timeout: 3000 });
});
