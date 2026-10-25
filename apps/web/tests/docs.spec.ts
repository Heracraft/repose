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

	await page.getByRole('button', { name: 'Search docs' }).click();
	await page.getByRole('combobox', { name: 'Search docs' }).fill('ntfy topic');
	await page.getByRole('listbox').getByRole('option').first().click();
	await expect(page).toHaveURL(/\/docs\/notifications/);
});

// The docs 404 keeps the docs shell and offers the pages closest to the
// slug asked for, or the Quickstart when none is close (spec 2.2).
test('an unknown docs page says so, with a 404 and the closest pages', async ({ page }) => {
	const response = await page.goto('/docs/no-such-page');
	expect(response?.status()).toBe(404);
	await expect(page).toHaveTitle('Not found · repose docs');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	await expect(page.getByRole('complementary', { name: 'Docs menu' })).toBeAttached();
	await expect(page.locator('main').getByRole('list').getByRole('link')).toHaveText(['Quickstart']);

	await page.goto('/docs/secret');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	await page.locator('main').getByRole('link', { name: 'Secrets and security' }).click();
	await expect(page).toHaveURL('/docs/secrets');
});

test('an experimental page says so under its title', async ({ page }) => {
	await page.goto('/docs/tutorial-t3-code');
	await expect(page.getByTestId('experimental')).toContainText('Experimental.');
	await page.goto('/docs/machine');
	await expect(page.getByTestId('experimental')).toHaveCount(0);
});

// A block with prompts copies its commands only, without the `$ ` or the
// output under them.
test('a code block copies its commands', async ({ page, context }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/docs/lifecycle');
	// Two commands with output between them; the stop example has one since
	// stop returns once the stop is under way (I-710).
	const block = page.locator('.doc .code').filter({ hasText: '$ repose rm -y' });
	await block.getByRole('button', { name: 'Copy' }).click();
	await expect(block.getByRole('button', { name: 'Copied' })).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
		'repose rm -y\nrepose run'
	);
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
// page's link: a link near the end of the list once put itself a third of
// the way down (/docs/cli at 390, I-400). Search is in the sub-bar, not
// the drawer.
test("the phone drawer shows the current page's link, and search is in the bar", async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/docs/cli');
	// The menu button is in the docs' sub-bar under the header (I-655),
	// beside the open page's title.
	const subbar = page.locator('[data-docs-subbar]');
	await expect(subbar).toContainText('CLI reference');
	await subbar.getByRole('button', { name: 'Open the docs menu' }).click();
	const drawer = page.getByRole('complementary', { name: 'Docs menu' });
	const link = drawer.getByRole('link', { name: 'CLI reference' });
	await expect(link).toBeInViewport({ ratio: 1 });
	// The link is not under the header and the sub-bar, which end at 101px.
	expect((await link.boundingBox())!.y).toBeGreaterThanOrEqual(101);
	await expect(drawer.getByRole('button', { name: 'Search docs' })).toBeHidden();
	await expect(subbar.getByRole('button', { name: 'Search docs' })).toBeVisible();
});

// Closing the phone drawer keeps the reader's place: focus goes back to
// the sticky menu button without scrolling the page to it (review r1 P1).
// While it is open, Tab stays in the drawer, since the article behind the
// scrim is inert (P2).
test('the phone drawer closes in place and holds Tab', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/docs/sync');
	await page.evaluate(() => window.scrollTo(0, 1500));
	await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(1500);
	const button = page.locator('[data-docs-subbar]').getByRole('button', { name: /docs menu/ });
	// A script click, so Playwright's own scroll-into-view does not move
	// the page before the close is measured.
	await button.evaluate((el: HTMLElement) => el.click());
	const article = page.locator('#docs-nav + div');
	await expect(article).toHaveJSProperty('inert', true);
	await expect(page.locator('.site-footer')).toHaveJSProperty('inert', true);
	await page.keyboard.press('Escape');
	await expect(button).toBeFocused();
	expect(await page.evaluate(() => window.scrollY)).toBe(1500);
	await expect(article).toHaveJSProperty('inert', false);
	await button.evaluate((el: HTMLElement) => el.click());
	await page.locator('.scrim').dispatchEvent('click');
	await expect(button).toBeFocused();
	expect(await page.evaluate(() => window.scrollY)).toBe(1500);
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

// Near the footer the sidebar gets shorter instead of riding up under the
// header, so the search stays in reach at the end of a page.
test('at the end of a page the docs search stays under the header', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs/billing');
	await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
	await expect
		.poll(() =>
			page.evaluate(() => {
				const nav = document.querySelector('#docs-nav')!.getBoundingClientRect();
				const search = document.querySelector('#docs-search')!.getBoundingClientRect();
				const footer = document.querySelector('.site-footer')!.getBoundingClientRect();
				return { top: nav.top, search: search.top >= 57, clear: nav.bottom <= footer.top + 1 };
			})
		)
		.toEqual({ top: 57, search: true, clear: true });
});

// Search is a palette (Guide B 12.14, I-841): Ctrl-K, Cmd-K or "/" opens
// it; one line per result, the query marked; arrows and Enter go to the
// heading, which takes focus; Escape gives focus back to the trigger.
test('the docs search palette opens on Ctrl-K, finds secrets and goes to the heading', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs/machine');
	const trigger = page.locator('#docs-search');
	await expect(trigger).toContainText(/Ctrl K|⌘K/);
	await page.keyboard.press('ControlOrMeta+k');
	const dialog = page.getByRole('dialog', { name: 'Search docs' });
	await expect(dialog).toBeVisible();
	const box = dialog.getByRole('combobox', { name: 'Search docs' });
	await expect(box).toBeFocused();
	await expect(box).toHaveAttribute('placeholder', 'Pages, commands, settings');
	// Empty, it lists every page with its section.
	const options = dialog.getByRole('option');
	await expect(options.first()).toContainText('Quickstart');
	await expect(options.first()).toContainText('Start here');
	await expect(options.first()).toHaveAttribute('aria-selected', 'true');

	await box.fill('secret');
	await expect(options.first()).toContainText('Secrets and security');
	await expect(options.first().locator('mark').first()).toHaveText(/secret/i);
	await expect(box).toHaveAttribute(
		'aria-activedescendant',
		(await options.first().getAttribute('id'))!
	);
	// Arrows move, wrapping at the ends.
	await page.keyboard.press('ArrowUp');
	await expect(options.last()).toHaveAttribute('aria-selected', 'true');
	await page.keyboard.press('ArrowDown');
	await page.keyboard.press('ArrowDown');
	const second = options.nth(1);
	await expect(second).toHaveAttribute('aria-selected', 'true');
	await expect(second).toContainText('Secrets for every project');
	await page.keyboard.press('Enter');
	await expect(dialog).toBeHidden();
	await expect(page).toHaveURL('/docs/secrets#secrets-for-every-project');
	await expect(page.locator('#secrets-for-every-project')).toBeFocused();
	await expect(page.locator('#secrets-for-every-project')).toBeInViewport();

	// "/" opens it outside a field; Escape closes it and focus goes back to the trigger.
	await page.keyboard.press('/');
	await expect(dialog).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(dialog).toBeHidden();
	await expect(trigger).toBeFocused();
});

test('the docs search finds CLI flags and says when nothing matches', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/docs');
	await page.locator('#docs-search').click();
	const dialog = page.getByRole('dialog', { name: 'Search docs' });
	const box = dialog.getByRole('combobox');
	await box.fill('--temp');
	const first = dialog.getByRole('option').first();
	await expect(first).toContainText('--temp [DURATION]');
	await expect(first).toContainText('repose run');
	await first.click();
	await expect(page).toHaveURL('/docs/cli#repose-run-project');

	await page.locator('#docs-search').click();
	await box.fill('zzqx');
	await expect(dialog.getByRole('option')).toHaveCount(0);
	await expect(dialog).toContainText('No match for “zzqx”.');
	await expect(dialog.getByRole('status')).toHaveText('No match for “zzqx”.');
	// A typo lists the close labels under the no-match line, and Enter opens the first.
	await box.fill('snapshto');
	await expect(dialog.getByRole('status')).toContainText('No match for “snapshto”.');
	await expect(dialog.getByRole('listbox', { name: 'Close matches' })).toBeVisible();
	await expect(dialog.getByRole('option').first()).toContainText('Snapshots');
	await box.press('Enter');
	await expect(page).toHaveURL(/\/docs\/lifecycle#snapshots$/);
	await page.locator('#docs-search').click();
	// Hovering a row makes it the active one.
	await box.fill('sync');
	await dialog.getByRole('option').nth(2).hover();
	await expect(dialog.getByRole('option').nth(2)).toHaveAttribute('aria-selected', 'true');
});

// On a phone the search is an icon in the docs' sub-bar, and the palette
// takes nearly the full width from the top, with 44px rows.
test('the docs search on a phone opens from the sub-bar, near full width', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/docs/cli');
	const button = page.locator('[data-docs-subbar]').getByRole('button', { name: 'Search docs' });
	const size = (await button.boundingBox())!;
	expect(Math.min(size.width, size.height)).toBeGreaterThanOrEqual(44);
	await button.click();
	const dialog = page.getByRole('dialog', { name: 'Search docs' });
	await expect(dialog).toBeVisible();
	// Measured once the opening scale has settled; a desktop Chromium's
	// scrollbar takes 15px of the 390, a phone's none.
	await expect
		.poll(async () => {
			const box = (await dialog.boundingBox())!;
			return box.x <= 12 && box.y <= 12 && box.width >= 390 - 15 - 24;
		})
		.toBe(true);
	await dialog.getByRole('combobox').fill('snapshot');
	await expect(dialog.getByRole('option').first().locator('mark').first()).toHaveText(/snapshot/i);
	await page.keyboard.press('Escape');
	await expect(button).toBeFocused();
});
