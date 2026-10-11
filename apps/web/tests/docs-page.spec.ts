import { test, expect, type Page } from '@playwright/test';

// The page inside the docs frame (DocPage, I-660): the text and the rail at
// fixed x, one measure for prose and code, the rail's h2s and h3s, and the
// old slugs that now redirect.

/** The shared frame's content edges, read from the header (a classic
 *  scrollbar's gutter moves the whole frame 7.5px left). */
async function frameEdges(page: Page) {
	return page.evaluate(() => {
		const f = document.querySelector('header .frame')!;
		const b = f.getBoundingClientRect();
		const pad = parseFloat(getComputedStyle(f).paddingLeft);
		return { left: b.x + pad, right: b.right - pad };
	});
}

for (const path of ['/docs', '/docs/cli']) {
	test(`at 1440 ${path} puts the text at 428 and the rail at 1100, on one measure`, async ({
		page
	}) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto(path);
		await page.evaluate(() => document.fonts.ready.then(() => undefined));
		const edges = await frameEdges(page);
		const box = (sel: string) =>
			page
				.locator(sel)
				.first()
				.evaluate((el) => {
					const b = el.getBoundingClientRect();
					return { x: b.x, right: b.right };
				});
		const h1 = await box('main h1');
		const rail = await box('nav[aria-label="On this page"]');
		// 220px sidebar + 48px from the frame's left edge; 180px rail at its
		// right edge, 48px past the text.
		expect(Math.abs(h1.x - (edges.left + 268))).toBeLessThanOrEqual(2);
		expect(Math.abs(rail.x - (edges.right - 180))).toBeLessThanOrEqual(2);
		const p = await box('article > p');
		const pre = await box('article pre');
		expect(Math.abs(p.right - pre.right)).toBeLessThanOrEqual(2);
		expect(Math.abs(rail.x - pre.right - 48)).toBeLessThanOrEqual(2);
	});
}

test('old docs slugs land on their pages', async ({ page }) => {
	await page.goto('/docs/quickstart');
	await expect(page).toHaveURL('/docs');
	await expect(page.getByRole('heading', { level: 1, name: 'Quickstart' })).toBeVisible();
	await page.goto('/docs/pricing');
	await expect(page).toHaveURL('/docs/billing');
	await expect(page.getByRole('heading', { level: 1, name: 'Pricing' })).toBeVisible();
});

test('the CLI reference rail lists its commands', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs/cli');
	const rail = page.getByRole('navigation', { name: 'On this page' });
	// The h3s are a list inside their h2's item, by command name.
	const subs = rail.locator('ul ul a');
	expect(await subs.count()).toBeGreaterThanOrEqual(30);
	await expect(rail.getByRole('link', { name: 'repose run', exact: true })).toHaveAttribute(
		'href',
		'#repose-run-project'
	);
});

test('the rail marks its first section before you reach it', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs');
	const rail = page.getByRole('navigation', { name: 'On this page' });
	await expect(rail.getByRole('link').first()).toHaveAttribute('aria-current', 'true');
});

test("an experimental page's banner links to the feedback board", async ({ page }) => {
	await page.goto('/docs/tutorial-herdr');
	await expect(
		page.getByTestId('experimental').getByRole('link', { name: 'feedback board' })
	).toHaveAttribute('href', 'https://repose.fider.io');
});

// On a phone the Copy button sits in the code block's corner, not on a row
// of its own above the code.
test('on a phone the Copy button sits in the code block corner', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/docs');
	const m = await page
		.locator('article .code')
		.first()
		.evaluate((el) => {
			const pre = el.querySelector('pre')!.getBoundingClientRect();
			const copy = el.querySelector('.copy')!.getBoundingClientRect();
			const line = el.querySelector('.l1')!.getBoundingClientRect();
			return { top: copy.top - pre.top, right: pre.right - copy.right, line: line.top - pre.top };
		});
	expect(m.top).toBeLessThanOrEqual(8);
	expect(m.right).toBeLessThanOrEqual(8);
	expect(m.line).toBeLessThan(20);
});
