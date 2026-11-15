// DECISIONS I-666: the landing's motion controller (landing/motion.svelte.ts).
// The page has one motion control (WCAG 2.2.2; owner, 2026-11-12, in place
// of a pause button per picture), only one picture in
// the "On every machine" grid plays at a time, and the hero rebuilds only on
// a change of its own width, never of the window's height alone.
import { test, expect, type Page } from '@playwright/test';

const frames = '.pic-frame';
const playingInGrid = '[data-motion-group="grid"][data-motion="play"]';

async function load(page: Page) {
	await page.goto('/');
	// Every picture registers once anime.js has loaded; the page's one
	// control shows then.
	await expect(page.locator('button.pic-pause')).toHaveCount(1);
	await expect(page.locator(frames)).toHaveCount(6);
}

// Hero loop round 6: a loop starts only once the page has been still for
// QUIET_MS with its picture in view, so a reader scrolling past sees the
// still frame and never a move cut off mid-flight. The hero's Orbit is in
// view from the first paint, so it is the one that shows it.
test('the Orbit holds while the page moves and plays once it is still', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	const orb = page.locator('.orb-frame');
	// Nudging the page keeps it unsettled, with the Orbit in view throughout.
	for (let i = 0; i < 14; i++) {
		await page.evaluate((d) => window.scrollBy(0, d), i % 2 ? -8 : 8);
		await page.waitForTimeout(150);
	}
	await expect(orb).toHaveAttribute('data-motion', 'hold');
	await expect(orb).toHaveAttribute('data-motion', 'play', { timeout: 3000 });
});

test('a height-only resize does not restart the hero', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await load(page);
	await page.locator('.hero-pic .mwin').scrollIntoViewIfNeeded();
	await expect(page.locator('.hero-frame')).toHaveAttribute('data-motion', 'play');
	// 9s in, the project rows have landed (T.land is 1.3s) and the
	// loop is mid-story; a restart would set them back to 0.
	await page.waitForTimeout(9000);
	const kids = page.locator('.hero-pic .lw .m-proj');
	const before = await kids.evaluateAll((els) => els.map((e) => getComputedStyle(e).opacity));
	expect(before.length).toBeGreaterThan(0);
	expect(before.every((o) => o === '1')).toBe(true);
	// A phone's address bar collapsing: 60px less height, same width.
	await page.setViewportSize({ width: 390, height: 784 });
	await page.waitForTimeout(500);
	const after = await kids.evaluateAll((els) => els.map((e) => getComputedStyle(e).opacity));
	expect(after).toEqual(before);
});

test('the one motion control is reached by the keyboard and holds every picture', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
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

test('paused motion stays paused through scrolling out and back', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	const button = page.locator('button.pic-pause');
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	const frame = page.locator(frames).filter({ has: page.locator('.lh') });
	await frame.scrollIntoViewIfNeeded();
	// Past the 1s quiet and the 2s a pointer would hold it for.
	await page.waitForTimeout(3000);
	await expect(frame).toHaveAttribute('data-motion', 'hold');
	await page.evaluate(() => window.scrollTo(0, 0));
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	await frame.scrollIntoViewIfNeeded();
	await expect(frame).toHaveAttribute('data-motion', /play|rest/, { timeout: 3000 });
});

test('hovering a picture holds it, and it plays again after the pointer leaves', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	const frame = page.locator(frames).filter({ has: page.locator('.lh') });
	await frame.scrollIntoViewIfNeeded();
	// A pointer moving inside it: a resting pointer that the page scrolls
	// under does not hold it.
	const box = (await frame.boundingBox())!;
	await page.mouse.move(box.x + 40, box.y + 40);
	await page.mouse.move(box.x + 120, box.y + 80, { steps: 4 });
	await expect(frame).toHaveAttribute('data-motion', 'hold');
	await page.mouse.move(2, 2);
	await page.waitForTimeout(1000);
	await expect(frame).toHaveAttribute('data-motion', 'hold');
	// The hovered picture keeps the grid's turn while it is in view.
	await expect(frame).toHaveAttribute('data-motion', 'play', { timeout: 3000 });
});

test('scrolling a picture under a resting pointer does not hold it', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	await page.mouse.move(720, 450);
	const hero = page.locator('.hero-frame');
	// Wheel the hero's middle up to the pointer.
	const box = (await hero.boundingBox())!;
	const steps = Math.ceil((box.y + box.height / 2 - 450) / 100);
	for (let i = 0; i < steps; i++) {
		await page.mouse.wheel(0, 100);
		await page.waitForTimeout(100);
	}
	await page.waitForTimeout(300);
	await expect(hero).toHaveAttribute('data-motion', 'play');
	expect(await page.evaluate(() => document.querySelector('.hero-frame')!.matches(':hover'))).toBe(
		true
	);
});

for (const width of [1440, 390]) {
	test(`only one grid picture plays at a time, at ${width}`, async ({ page }) => {
		// A wait per scroll position, each past the quiet the controller
		// asks for: about 20 seconds of waiting at either width.
		test.setTimeout(120_000);
		await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
		await load(page);
		const grid = page.locator('[data-motion-group="grid"]');
		await expect(grid).toHaveCount(3);
		const top = await grid.first().evaluate((e) => e.getBoundingClientRect().top + scrollY);
		const bottom = await grid.last().evaluate((e) => e.getBoundingClientRect().bottom + scrollY);
		let withOne = 0;
		for (let y = top - 900; y < bottom; y += 150) {
			await page.evaluate((y) => window.scrollTo(0, y), y);
			// A loop starts only once the page has been still for QUIET_MS
			// (motion.svelte.ts, hero loop round 6): a reader scrolling past
			// sees the still frame. Waiting less than that is waiting for a
			// picture that is right to be holding.
			await page.waitForTimeout(1400);
			const n = await page.locator(playingInGrid).count();
			expect(n, `at ${y}`).toBeLessThanOrEqual(1);
			if (n === 1) withOne++;
		}
		expect(withOne).toBeGreaterThan(0);
	});
}
