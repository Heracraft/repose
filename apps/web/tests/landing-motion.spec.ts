// DECISIONS I-666: the landing's motion controller (landing/motion.svelte.ts).
// Each looping picture has a pause button (WCAG 2.2.2), only one picture in
// the "On every machine" grid plays at a time, and the hero rebuilds only on
// a change of its own width, never of the window's height alone.
import { test, expect, type Page } from '@playwright/test';

const frames = '.pic-frame';
const playingInGrid = '[data-motion-group="grid"][data-motion="play"]';

async function load(page: Page) {
	await page.goto('/');
	// Every picture registers once anime.js has loaded: five buttons.
	await expect(page.locator(`${frames} > button.pic-pause`)).toHaveCount(5);
}

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

test('every looping picture has a pause button the keyboard reaches', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	const seen = new Set<string>();
	for (let i = 0; i < 400 && seen.size < 5; i++) {
		await page.keyboard.press('Tab');
		const frame = await page.evaluate(() => {
			const a = document.activeElement;
			if (!a?.matches('button.pic-pause')) return null;
			const f = a.parentElement!;
			return Array.from(document.querySelectorAll('.pic-frame')).indexOf(f).toString();
		});
		if (frame === null || seen.has(frame)) continue;
		seen.add(frame);
		const button = page.locator('button.pic-pause:focus');
		await expect(button).toHaveAccessibleName('Pause animation');
		await expect(button).toHaveCSS('opacity', '1');
		await expect(button).toHaveAttribute('aria-pressed', 'false');
		await page.keyboard.press('Space');
		await expect(button).toHaveAttribute('aria-pressed', 'true');
		await page.keyboard.press('Enter');
		await expect(button).toHaveAttribute('aria-pressed', 'false');
	}
	expect(seen.size).toBe(5);
});

test('a paused picture stays paused through scrolling out and back', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await load(page);
	const frame = page.locator(frames).filter({ has: page.locator('.lh') });
	await frame.scrollIntoViewIfNeeded();
	const button = frame.locator('button.pic-pause');
	await button.focus();
	await page.keyboard.press('Space');
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await page.evaluate(() => (document.activeElement as HTMLElement).blur());
	await page.evaluate(() => window.scrollTo(0, 0));
	await expect(frame).toHaveAttribute('data-motion', 'hold');
	await frame.scrollIntoViewIfNeeded();
	// Past the 2s a pointer or focus would hold it for.
	await page.waitForTimeout(2500);
	await expect(frame).toHaveAttribute('data-motion', 'hold');
	await expect(button).toHaveAttribute('aria-pressed', 'true');
	await button.click();
	await expect(button).toHaveAttribute('aria-pressed', 'false');
	await expect(frame).toHaveAttribute('data-motion', /play|rest/);
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
		await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
		await load(page);
		const grid = page.locator('[data-motion-group="grid"]');
		await expect(grid).toHaveCount(3);
		const top = await grid.first().evaluate((e) => e.getBoundingClientRect().top + scrollY);
		const bottom = await grid.last().evaluate((e) => e.getBoundingClientRect().bottom + scrollY);
		let withOne = 0;
		for (let y = top - 900; y < bottom; y += 150) {
			await page.evaluate((y) => window.scrollTo(0, y), y);
			await page.waitForTimeout(250);
			const n = await page.locator(playingInGrid).count();
			expect(n).toBeLessThanOrEqual(1);
			if (n === 1) withOne++;
		}
		expect(withOne).toBeGreaterThan(0);
	});
}
