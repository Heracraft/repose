// DECISIONS I-669: the feature grid's four pictures share one frame (height,
// inset, head, bottom line; ComesBack's closed window inset alike at the
// bottom, I-675), the Editor shows all 78 columns at card width,
// the Browser's log sits under its viewport, and Localhost starts on
// Chromium's refused page with localhost:5173 already in the bar. I-676:
// Localhost's two pages never show at once.
import { test, expect, type Page } from '@playwright/test';

const cells = '.land-cells > .land-cell';

async function grid(page: Page, width: number) {
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/');
	await page.locator('#features').scrollIntoViewIfNeeded();
}

for (const width of [1440, 390]) {
	test(`the four grid pictures share one frame at ${width}`, async ({ page }) => {
		await grid(page, width);
		const shots = page.locator(`${cells} .shot`);
		await expect(shots).toHaveCount(4);
		const boxes = await shots.evaluateAll((els) =>
			els.map((shot) => {
				const s = shot.getBoundingClientRect();
				const win = shot.querySelector('.shot-win')!.getBoundingClientRect();
				const head = shot.querySelector('.shot-head')!.getBoundingClientRect();
				const wins = [...shot.querySelectorAll('.shot-win.open')].map((w) =>
					Math.round(w.getBoundingClientRect().bottom - s.bottom)
				);
				return {
					h: Math.round(s.height),
					top: Math.round(win.top - s.top),
					left: Math.round(win.left - s.left),
					// the picture's right edge: its rightmost window or tile
					right: Math.round(
						s.right -
							Math.max(
								...[...shot.querySelectorAll('.shot-win, .snap')].map(
									(w) => w.getBoundingClientRect().right
								)
							)
					),
					head: Math.round(head.height),
					open: wins,
					// a closed picture: its lowest window or tile above the
					// frame's inner bottom edge (a folded tile's box runs on
					// under its clip, so only the newest counts)
					bottom: Math.round(
						s.bottom -
							1 -
							Math.max(
								...[...shot.querySelectorAll('.shot-win, .snap:not(.old)')].map(
									(w) => w.getBoundingClientRect().bottom
								)
							)
					)
				};
			})
		);
		const [first] = boxes;
		for (const b of boxes) {
			expect(b.h).toBe(first.h);
			expect(b.top).toBe(first.top);
			expect(b.left).toBe(first.left);
			expect(b.right).toBe(first.right);
			expect(b.head).toBe(28);
			// one window in each runs off the frame's bottom edge (the
			// frame's 1px border is the line it meets), or, closed
			// (ComesBack), stands the top's inset above it
			expect(b.open.length).toBeLessThanOrEqual(1);
			if (b.open.length) expect(b.open[0]).toBeGreaterThanOrEqual(-1);
			else expect(Math.abs(b.bottom - (b.top - 1))).toBeLessThanOrEqual(1);
		}
	});
}

for (const width of [1440, 1024, 820, 390, 360]) {
	test(`ComesBack shows every row's label whole at ${width}`, async ({ page }) => {
		await grid(page, width);
		const cut = await page
			.locator('.land-cells .m-row .nm, .land-cells .m-row .val')
			.evaluateAll((els) =>
				els.filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent)
			);
		expect(cut).toEqual([]);
		await expect(page.locator('.land-cells .m-row')).toHaveCount(6);
	});
}

test('the Editor shows every column of the screen at card width', async ({ page }) => {
	await grid(page, 1440);
	const over = await page
		.locator('.land-cells .screen .ln')
		.evaluateAll((els) => els.filter((e) => e.scrollWidth > e.clientWidth + 1).length);
	expect(over).toBe(0);
	await expect(page.locator('.land-cells .screen .ln')).toHaveCount(20);
});

test("the Browser's log sits under its viewport, not over the page", async ({ page }) => {
	await grid(page, 1440);
	const [vp, term] = await Promise.all([
		page.locator('.land-cells .vp').boundingBox(),
		page.locator('.land-cells .bw .term').boundingBox()
	]);
	expect(term!.y).toBeGreaterThanOrEqual(vp!.y + vp!.height - 1);
});

test('Localhost leaves its still through the seam, and never shows two pages at once', async ({
	page
}) => {
	await grid(page, 1440);
	const frame = page.locator('.shot.shot-box', { has: page.locator('.lh') });
	await frame.scrollIntoViewIfNeeded();
	// Take the grid's turn: point at it, then leave; it plays 2s later,
	// from its still frame, so its first cycle starts at the seam (I-676).
	const box = (await frame.boundingBox())!;
	await page.mouse.move(box.x + 30, box.y + 40);
	await page.mouse.move(box.x + 40, box.y + 50);
	await page.mouse.move(2, 2);
	await expect(frame).toHaveAttribute('data-motion', 'play', { timeout: 5000 });
	// Sample through the seam's exit, the first frame and the reload: the
	// refused page and the app are never both over 0.2 (the app's headline
	// over "This site can't be reached"), and the Local line stays.
	const samples: { home: number; err: number; tr: number; url: string | null }[] = [];
	for (let i = 0; i < 40; i++) {
		samples.push(
			await frame.evaluate((f) => ({
				home: Number(getComputedStyle(f.querySelector('.home')!).opacity),
				err: Number(getComputedStyle(f.querySelector('.err')!).opacity),
				tr: Number(getComputedStyle(f.querySelector('.tr')!).opacity),
				url: f.querySelector('.url')!.textContent
			}))
		);
		await page.waitForTimeout(100);
	}
	for (const s of samples) {
		expect(Math.min(s.home, s.err)).toBeLessThanOrEqual(0.2);
		expect(s.tr).toBe(1);
		expect(s.url).toBe('localhost:5173');
	}
	// The loop's first frame: the refused page, dimmed, and no app.
	expect(samples.some((s) => s.home === 0 && s.err === 0.6)).toBe(true);
	// The forward lands and the page reloads to the app.
	await expect
		.poll(async () => frame.locator('.home').evaluate((e) => getComputedStyle(e).opacity), {
			timeout: 6000
		})
		.toBe('1');
});
