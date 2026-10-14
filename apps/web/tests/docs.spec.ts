import { test, expect } from '@playwright/test';

// /docs is public: a signed-out visitor stays on it, can move between pages
// and can search.
test('docs are readable while signed out, with search and prev/next', async ({ page }) => {
	await page.goto('/docs');
	await expect(page).toHaveURL('/docs');
	await expect(page.getByRole('heading', { level: 1, name: 'Quickstart' })).toBeVisible();

	await page.goto('/docs/machine');
	await expect(page).toHaveURL('/docs/machine');
	await expect(page.getByRole('heading', { level: 1, name: 'The machine' })).toBeVisible();
	await page.getByRole('link', { name: /Next\s*Installing software/ }).click();
	await expect(page).toHaveURL('/docs/config');

	await page.getByLabel('Search the docs').fill('ntfy topic');
	const results = page.getByRole('list', { name: 'Search results' });
	await results.getByRole('link').first().click();
	await expect(page).toHaveURL(/\/docs\/notifications/);
});

test('an unknown docs page says so, with a 404', async ({ page }) => {
	const response = await page.goto('/docs/no-such-page');
	expect(response?.status()).toBe(404);
	await expect(page.getByRole('heading', { name: 'No such page' })).toBeVisible();
	await expect(page.getByText("There's no docs page called “no-such-page”.")).toBeVisible();
});

test('an experimental page says so under its title', async ({ page }) => {
	await page.goto('/docs/tutorial-t3-code');
	await expect(page.getByTestId('experimental')).toContainText('Experimental.');
	await page.goto('/docs/machine');
	await expect(page.getByTestId('experimental')).toHaveCount(0);
});

// A block with prompts copies its commands only, without the `$ ` or the
// output under them. lifecycle.md used to show the stop and the start in
// one block; I-710 split them, because a stop returns while it is still
// running and the start comes later, so each block now holds one command
// and both are checked here.
test('a code block copies its commands', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/docs/lifecycle');
	const stop = page.locator('.doc .code').filter({ hasText: '$ repose stop todo-app' });
	await stop.getByRole('button', { name: 'Copy' }).click();
	await expect(stop.getByRole('button', { name: 'Copied' })).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose stop todo-app');
	const start = page.locator('.doc .code').filter({ hasText: '$ repose start todo-app' });
	await start.getByRole('button', { name: 'Copy' }).click();
	await expect(start.getByRole('button', { name: 'Copied' })).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('repose start todo-app');
});

// The sidebar lists pages only, and from xl up the page's sections are in a
// rail whose column is drawn on every page, so following a link between
// pages moves neither the sidebar, the text nor the rail (I-396).
test('the docs columns hold still between pages, and the rail marks the section', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const boxes = () =>
		page.evaluate(() => {
			const r = (el: Element | null | undefined) => {
				const b = el!.getBoundingClientRect();
				return [b.x, b.y + scrollY, b.width].map(Math.round).join(',');
			};
			const main = document.querySelector('main');
			return {
				sidebar: r(document.getElementById('docs-nav')),
				main: r(main),
				rail: r(main?.nextElementSibling)
			};
		});

	await page.goto('/docs');
	await expect(page.getByRole('heading', { level: 1, name: 'Quickstart' })).toBeVisible();
	await page.evaluate(() => document.fonts.ready.then(() => undefined));
	// The shared frame (I-656, I-660): the 220px sidebar at the frame's
	// left edge, 48px to the text, and the 180px rail ending at the
	// frame's right edge. At 1440 with an overlay scrollbar that is x=160,
	// 428 and 1100; a classic scrollbar's gutter moves the frame 7.5px
	// left, so the edges are read from the header's frame.
	const edges = await page.evaluate(() => {
		const f = document.querySelector('header .frame')!;
		const b = f.getBoundingClientRect();
		const pad = parseFloat(getComputedStyle(f).paddingLeft);
		return { left: Math.round(b.x + pad), right: Math.round(b.right - pad) };
	});
	await expect.poll(boxes).toMatchObject({
		sidebar: `${edges.left},57,220`,
		rail: `${edges.right - 180},57,180`
	});
	expect((await boxes()).main).toMatch(new RegExp(`^${edges.left + 268},57,\\d+$`));
	const first = await boxes();
	// Layout shifts during the client navigations. The links are clicked
	// from script: a shift within 500ms of real input is left out of the
	// entries' sum (hadRecentInput), and the point is to see every shift.
	await page.evaluate(() => {
		const w = window as unknown as { shifts: number[] };
		w.shifts = [];
		new PerformanceObserver((list) => {
			for (const e of list.getEntries()) w.shifts.push((e as unknown as { value: number }).value);
		}).observe({ type: 'layout-shift' });
	});
	for (const [slug, h1] of [
		['cli', 'CLI reference'],
		['secrets', 'Secrets and security']
	]) {
		await page.evaluate(
			(slug) =>
				document.querySelector<HTMLAnchorElement>(`#docs-nav a[href$="/docs/${slug}"]`)!.click(),
			slug
		);
		await expect(page.getByRole('heading', { level: 1, name: h1 })).toBeVisible();
		expect(await boxes(), slug).toEqual(first);
		// The sidebar holds no section links of the open page.
		await expect(
			page
				.getByRole('complementary', { name: 'Docs menu' })
				.getByRole('list', { name: 'On this page' })
		).toHaveCount(0);
	}

	expect(await page.evaluate(() => (window as unknown as { shifts: number[] }).shifts)).toEqual([]);

	const rail = page.getByRole('navigation', { name: 'On this page' });
	await expect(rail).toBeVisible();
	await rail.getByRole('link', { name: 'What repose stores' }).click();
	await expect(page).toHaveURL('/docs/secrets#what-repose-stores');
	await expect(rail.locator('[aria-current="true"]')).toHaveText('What repose stores');
	await page.evaluate(() => scrollTo(0, 0));
	await expect(rail.locator('[aria-current="true"]')).toHaveText('Store an API key');

	// One jump that carries a heading from below the reading band to above
	// it, never inside (a wheel fling, a scrollbar drag): the observer alone
	// left the rail empty here.
	await page.goto('/docs/cli');
	await page.evaluate(() => {
		const h = document.getElementById('projects')!;
		scrollTo({ top: h.getBoundingClientRect().top + scrollY + 500, behavior: 'instant' });
	});
	await expect(rail.locator('[aria-current="true"]')).toHaveText('Projects');
});

// On a phone the drawer opens scrolled the least that shows the current
// page's link, with the search held at its top: a link near the end of the
// list once put itself a third of the way down and scrolled the search away
// (/docs/cli at 390, I-400).
test("the phone drawer shows the search and the current page's link", async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/docs/cli');
	// The menu button is in the docs' sub-bar under the header (I-655),
	// beside the open page's title.
	const subbar = page.locator('[data-docs-subbar]');
	await expect(subbar).toContainText('CLI reference');
	await subbar.getByRole('button', { name: 'Open the docs menu' }).click();
	const drawer = page.getByRole('complementary', { name: 'Docs menu' });
	await expect(drawer.getByRole('link', { name: 'CLI reference' })).toBeInViewport({ ratio: 1 });
	const search = drawer.getByLabel('Search the docs');
	await expect(search).toBeInViewport({ ratio: 1 });
	// The search is not under the header and the sub-bar, which end at 101px.
	expect((await search.boundingBox())!.y).toBeGreaterThanOrEqual(101);
});

// The owner saw a scrollbar flash over the sidebar on the first visit to
// the docs, gone at the first move (I-660): from lg up the sidebar draws
// none, and at 1440x900 its list fits whole with Tutorials folded.
test('the desktop sidebar fits 1440x900 and draws no scrollbar', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await page.getByRole('navigation', { name: 'Site' }).getByRole('link', { name: 'Docs' }).click();
	await expect(page).toHaveURL('/docs');
	const aside = page.locator('#docs-nav');
	await expect(aside).toBeVisible();
	const m = await aside.evaluate((el) => ({
		fits: el.scrollHeight <= el.clientHeight,
		scrollbar: getComputedStyle(el).scrollbarWidth,
		top: el.scrollTop
	}));
	expect(m).toEqual({ fits: true, scrollbar: 'none', top: 0 });
	// Tutorials is folded away from a tutorial and open on one.
	const fold = aside.locator('details');
	await expect(fold).not.toHaveAttribute('open');
	await page.goto('/docs/tutorial-herdr');
	await expect(fold).toHaveAttribute('open');
	await expect(aside.getByRole('link', { name: 'herdr on your machine' })).toBeInViewport({
		ratio: 1
	});
});
