// The design rules that no other spec exercises: the keyboard path through
// a page, forced colours, reduced motion, and the phone column
// (DESIGN-LANGUAGE.md; DECISIONS I-377, I-391, I-392). Lighthouse (tests-a11y)
// scores the pages at rest; these drive them.
import { test, expect, type Locator, type Page } from '@playwright/test';
import {
	signIn,
	createProject,
	destroyProject,
	apiURLFromEnv,
	fail,
	unfail,
	addQuestion,
	closeQuestion,
	setBilling,
	resetBilling
} from './helpers';

/** Presses Tab until the target holds the focus (at most 80 stops). */
async function tabTo(page: Page, target: Locator): Promise<void> {
	const path: string[] = [];
	for (let i = 0; i < 80; i++) {
		await page.keyboard.press('Tab');
		if (await target.evaluate((el) => el === document.activeElement)) return;
		path.push(
			await page.evaluate(() => {
				const a = document.activeElement;
				return `${a?.tagName.toLowerCase()} ${(a?.getAttribute('aria-label') ?? a?.textContent ?? '').trim().slice(0, 20)}`;
			})
		);
	}
	throw new Error(`80 Tab stops without reaching the target: ${path.slice(0, 30).join(' | ')}`);
}

/** Tabs to the target and reports whether focus drew a ring on it, or on
 *  its ::after (a row link whose box covers the row): an outline of 2px
 *  or more, or a box-shadow it did not have at rest. */
async function ringOnTab(page: Page, target: Locator): Promise<boolean> {
	const rest = await target.evaluate((el) => getComputedStyle(el).boxShadow);
	await tabTo(page, target);
	return target.evaluate((el, rest) => {
		const drawn = (s: CSSStyleDeclaration) =>
			s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) >= 2;
		const s = getComputedStyle(el);
		return (
			drawn(s) ||
			drawn(getComputedStyle(el, '::after')) ||
			(s.boxShadow !== 'none' && s.boxShadow !== rest)
		);
	}, rest);
}

/** The projects this file makes, destroyed after each test, so the shared
 *  account stays under its 100-project cap for the files after it. */
const made: string[] = [];
async function project(body: { name: string; remote_url: string; class?: string }) {
	const p = await createProject(apiURLFromEnv(), body);
	made.push(p.id);
	return p;
}
test.afterEach(async () => {
	while (made.length) await destroyProject(apiURLFromEnv(), made.pop()!);
});

test.describe('signed in', () => {
	test.beforeEach(async ({ page }) => {
		await signIn(page);
	});

	test('the skip link is the first tab stop and moves focus to the page', async ({ page }) => {
		await page.goto('/projects');
		await expect(page.getByRole('heading', { level: 1, name: 'Projects' })).toBeVisible();
		await page.keyboard.press('Tab');
		const skip = page.getByRole('link', { name: 'Skip to content' });
		await expect(skip).toBeFocused();
		await expect(skip).toBeInViewport();
		await page.keyboard.press('Enter');
		await expect(page.locator('main')).toBeFocused();
	});

	test('the resize panel takes focus and Cancel gives it back', async ({ page }) => {
		const p = await project({
			name: 'focus-resize-app',
			remote_url: 'github.com/heracraft/focus-resize-app',
			class: 'small'
		});
		await page.goto(`/projects/${p.id}`);
		const open = page.getByRole('button', { name: 'Resize…' });
		await open.focus();
		await page.keyboard.press('Enter');
		await expect(page.getByLabel('Grow to')).toBeFocused();
		await page.getByRole('button', { name: 'Cancel' }).focus();
		await page.keyboard.press('Enter');
		await expect(open).toBeFocused();
	});

	test('Retry keeps its banner and the keyboard focus while it runs', async ({ page }) => {
		await fail('GET', '/me', 'internal');
		try {
			await page.goto('/settings');
			const banner = page.getByRole('alert');
			await expect(banner).toBeVisible();
			// Slow the retry's answer, so the state between the press and the
			// answer can be seen.
			await page.route('**/v1/me', async (route) => {
				await new Promise((r) => setTimeout(r, 600));
				await route.fallback();
			});
			await banner.getByRole('button', { name: 'Retry', exact: true }).focus();
			await page.keyboard.press('Enter');
			const retrying = banner.getByRole('button', { name: 'Retrying…' });
			await expect(retrying).toBeVisible();
			await expect(retrying).toBeFocused();
			await expect(page.getByText('Loading…')).toHaveCount(0);
			// It failed again: the same button, still focused, says Retry.
			await expect(banner.getByRole('button', { name: 'Retry', exact: true })).toBeFocused();
		} finally {
			await unfail('GET', '/me');
			await page.unroute('**/v1/me');
		}
		await page.getByRole('button', { name: 'Retry', exact: true }).click();
		await expect(page.getByRole('heading', { level: 2, name: 'Delete account' })).toBeVisible();
	});

	test('forced colours keep the state dot filled in a system colour', async ({ page }) => {
		const p = await project({
			name: 'forced-colours-app',
			remote_url: 'github.com/heracraft/forced-colours-app'
		});
		await page.emulateMedia({ forcedColors: 'active' });
		await page.goto(`/projects/${p.id}`);
		const dot = page.locator('.hk-dot--on').first();
		await expect(dot).toBeVisible();
		const { fill, edge } = await dot.evaluate((el) => {
			const s = getComputedStyle(el);
			return { fill: s.backgroundColor, edge: s.borderTopColor };
		});
		expect(fill).not.toBe('rgba(0, 0, 0, 0)');
		expect(fill).toBe(edge);
	});

	test('forced colours mark the current config tab and no other', async ({ page }) => {
		const p = await project({
			name: 'forced-tabs-app',
			remote_url: 'github.com/heracraft/forced-tabs-app'
		});
		await page.emulateMedia({ forcedColors: 'active' });
		await page.goto(`/projects/${p.id}/config`);
		const tabs = page.getByRole('tab');
		await expect(tabs).toHaveCount(2);
		const edges = await tabs.evaluateAll((els) =>
			els.map((el) => {
				const s = getComputedStyle(el);
				return {
					selected: el.getAttribute('aria-selected') === 'true',
					edge: `${s.borderBottomWidth} ${s.borderBottomColor}`,
					page: getComputedStyle(document.body).backgroundColor
				};
			})
		);
		const current = edges.find((e) => e.selected)!;
		const other = edges.find((e) => !e.selected)!;
		expect(current.edge).not.toBe(other.edge);
		expect(current.edge.startsWith('3px')).toBe(true);
		// The other tab's edge is the page's own colour: no mark.
		expect(other.edge.endsWith(other.page)).toBe(true);
	});

	// The places a container could clip a ring or a custom ring replaces
	// the outline (critique-guide 3): a table row's link, a filter link, a
	// config tab and the focused Nix editor each show one on the Tab path.
	test('the Tab path shows a ring on a table link, a filter link, a tab and the Nix editor', async ({
		page
	}) => {
		const p = await project({
			name: 'tab-walk-app',
			remote_url: 'github.com/heracraft/tab-walk-app'
		});
		// The filter links show from nine projects: pad the list with copies.
		await page.route('**/v1/projects', async (route) => {
			const res = await route.fetch();
			const list = (await res.json()) as { id: string; name: string; slug: string }[];
			const padded = [...list];
			for (let i = 0; padded.length < 10; i++) {
				padded.push({ ...list[0], id: `${list[0].id}-${i}`, name: `pad-${i}`, slug: `pad-${i}` });
			}
			await route.fulfill({ response: res, json: padded });
		});
		await page.goto('/projects');
		await expect(page.getByRole('navigation', { name: 'Filter projects' })).toBeVisible();
		const filter = page
			.getByRole('navigation', { name: 'Filter projects' })
			.getByRole('link')
			.nth(1);
		expect(await ringOnTab(page, filter), 'filter link').toBe(true);
		const rowLink = page.locator('.ptable').getByRole('link', { name: p.name, exact: true });
		await expect(rowLink).toBeVisible();
		expect(await ringOnTab(page, rowLink), 'table row link').toBe(true);
		await page.unroute('**/v1/projects');

		await page.goto(`/projects/${p.id}/config`);
		// The tablist is one tab stop, on the selected tab (a project the
		// earlier specs applied Nix to opens on Nix).
		const current = page.getByRole('tab', { selected: true });
		await expect(current).toBeVisible();
		expect(await ringOnTab(page, current), 'config tab').toBe(true);
		const nixTab = page.getByRole('tab', { name: 'Nix' });
		if ((await nixTab.getAttribute('aria-selected')) !== 'true') {
			await page.keyboard.press('ArrowRight');
			await expect(nixTab).toBeFocused();
			await page.keyboard.press('Enter');
		}
		await expect(nixTab).toHaveAttribute('aria-selected', 'true');
		const frame = page.locator('.nix-frame');
		const rest = await frame.evaluate((el) => getComputedStyle(el).boxShadow);
		await tabTo(page, page.getByRole('textbox', { name: 'Nix fragment' }));
		// The ring eases in over --hk-dur-fast: a 2px inset once it has.
		expect(rest).not.toMatch(/0px 0px 0px 2px inset/);
		await expect
			.poll(() => frame.evaluate((el) => getComputedStyle(el).boxShadow), { message: 'Nix editor' })
			.toMatch(/0px 0px 0px 2px inset/);
	});
});

// DECISIONS I-655, I-656: one header on every kind of page, the same
// items in the same order, its logo at the same x and going home, and a
// Feedback link in the header and the footer of every page.
const FRAME_PAGES = ['/', '/docs/cli', '/terms', '/projects'];

for (const width of [320, 360, 390, 1024, 1440]) {
	test(`the header row fits at ${width} on every kind of page`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await signIn(page);
		for (const path of FRAME_PAGES) {
			await page.goto(path);
			const nav = page.getByRole('navigation', { name: 'Site' });
			if (path !== '/projects' || width >= 640) {
				await expect(nav.getByRole('link', { name: 'Docs' })).toBeVisible();
			}
			const items = nav.locator(':scope > a:visible, :scope > div > :visible');
			const count = await items.count();
			if (path === '/projects') {
				// Signed in, Pricing goes (I-679): Docs, Feedback, GitHub and
				// Account from sm; below it Account alone, which holds Docs
				// and Feedback. The places are in the rail or strip (I-841).
				expect(count, path).toBe(width >= 640 ? 4 : 1);
				const places = page.getByRole('navigation', { name: 'Dashboard' });
				await expect(places.getByRole('link', { name: 'Projects', exact: true })).toBeInViewport({
					ratio: 1
				});
			} else {
				// Docs, Feedback and the account slot at every width; Pricing
				// from 400px and GitHub from sm.
				expect(count, path).toBe(width >= 640 ? 5 : width >= 400 ? 4 : 3);
			}
			for (let i = 0; i < count; i++) {
				await expect(items.nth(i), `${path} item ${i}`).toBeInViewport({ ratio: 1 });
			}
			await expect(page.getByRole('link', { name: 'repose, home' })).toBeInViewport({
				ratio: 1
			});
		}
	});
}

for (const width of [1440, 390]) {
	test(`the logo stands at the frame's edge on every page at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await signIn(page);
		const xs: number[] = [];
		for (const path of FRAME_PAGES) {
			await page.goto(path);
			const logo = page
				.getByRole('link', { name: 'repose, home' })
				.locator('svg')
				.filter({ visible: true });
			await expect(logo).toBeVisible();
			const { x, cw } = await logo.evaluate((el) => ({
				x: el.getBoundingClientRect().x,
				// The header is full-bleed: its width is the page's.
				cw: document.querySelector('header')!.getBoundingClientRect().width
			}));
			// The 1200px frame, centred in the page's width (less a classic
			// scrollbar's gutter, which an overlay scrollbar does not take),
			// and its 20 to 40px gutter: x=160 at 1440 and x=20 at 390.
			const gutter = Math.min(40, Math.max(20, 0.03 * width));
			expect(x, path).toBeCloseTo((cw - Math.min(cw, 1200)) / 2 + gutter, 0);
			xs.push(Math.round(x));
		}
		expect(new Set(xs).size, xs.join(',')).toBe(1);
	});
}

test('the logo goes home from the dashboard', async ({ page }) => {
	await signIn(page);
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: 'repose, home' })).toHaveAttribute('href', '/');
});

test('every page links to the feedback board in its header and its footer', async ({ page }) => {
	await signIn(page);
	for (const path of FRAME_PAGES) {
		await page.goto(path);
		for (const where of [
			page.getByRole('navigation', { name: 'Site' }),
			page.getByRole('navigation', { name: 'Footer' })
		]) {
			await expect(where.getByRole('link', { name: 'Feedback' }), path).toHaveAttribute(
				'href',
				'https://repose.fider.io'
			);
		}
	}
});

test('the header stays at the top of a long page', async ({ page }) => {
	await page.goto('/terms');
	await page.evaluate(() => scrollTo(0, 1500));
	await expect
		.poll(() =>
			page
				.locator('header')
				.first()
				.evaluate((el) => el.getBoundingClientRect().top)
		)
		.toBe(0);
});

for (const width of [768, 1024, 1120]) {
	test(`the landing does not scroll sideways at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const wide = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
		expect(wide).toBeLessThanOrEqual(0);
	});
}

test.describe('public pages', () => {
	test('the docs fold chevron turns only when motion is allowed', async ({ page }) => {
		await page.setViewportSize({ width: 390, height: 844 });
		const chevron = page.locator('main details summary svg');
		await page.goto('/docs/cli');
		await expect(chevron).toBeAttached();
		expect(await chevron.evaluate((el) => getComputedStyle(el).transitionProperty)).toContain(
			'transform'
		);
		await page.emulateMedia({ reducedMotion: 'reduce' });
		expect(await chevron.evaluate((el) => getComputedStyle(el).transitionProperty)).toBe('none');
	});

	// Inline code with no space in it never breaks, so a long one could push
	// the page wider than a phone (a 44-character ntfy URL did).
	for (const width of [390, 360]) {
		test(`no docs page scrolls sideways at ${width}`, async ({ page }) => {
			await page.setViewportSize({ width, height: 800 });
			for (const slug of ['notifications', 'cli', 'secrets', 'troubleshooting', 'your-chrome']) {
				await page.goto(`/docs/${slug}`);
				await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
				const wide = await page.evaluate(() => document.documentElement.scrollWidth);
				expect(wide, slug).toBeLessThanOrEqual(width);
			}
		});
	}

	// A reference table fits its column: from sm the term column stays on
	// one line and description code wraps; on a phone a two- or three-column
	// table stacks each row (/docs/cli Secrets ran 690px in 624, config.toml
	// 403 in 335).
	for (const width of [1440, 390]) {
		test(`no /docs/cli table scrolls sideways at ${width}`, async ({ page }) => {
			await page.setViewportSize({ width, height: 900 });
			await page.goto('/docs/cli');
			await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
			const wide = await page
				.locator('.doc .table-wrap')
				.evaluateAll((els) =>
					els
						.filter((el) => el.scrollWidth > el.clientWidth)
						.map((el) => `${el.scrollWidth}/${el.clientWidth}`)
				);
			expect(wide).toEqual([]);
		});
	}

	test('monospace text draws "://" with no ligature gap', async ({ page }) => {
		await page.goto('/docs/install');
		const code = page.locator('.doc pre code').first();
		await expect(code).toBeVisible();
		expect(await code.evaluate((el) => getComputedStyle(el).fontVariantLigatures)).toBe(
			'no-contextual'
		);
	});
});

// The theme switch (DECISIONS I-837): System, Light and Dark in the footer
// of every page. A choice is stored per browser, painted before the first
// frame, and System hands the page back to the computer's setting.
test.describe('the theme switch', () => {
	const theme = (page: Page) =>
		page.evaluate(() => ({
			attr: document.documentElement.dataset.theme ?? null,
			stored: localStorage.getItem('hk-theme'),
			bg: getComputedStyle(document.body).backgroundColor
		}));

	test('sets data-theme, survives a reload, and System removes it', async ({ page }) => {
		await page.emulateMedia({ colorScheme: 'light' });
		await page.goto('/docs/cli');
		const group = page.getByRole('radiogroup', { name: 'Theme' });
		await expect(group.getByRole('radio', { name: 'System' })).toBeChecked();
		await group.getByText('Dark', { exact: true }).click();
		await expect(group.getByRole('radio', { name: 'Dark' })).toBeChecked();
		expect(await theme(page)).toEqual({ attr: 'dark', stored: 'dark', bg: 'rgb(9, 9, 11)' });
		await page.reload();
		await expect(group.getByRole('radio', { name: 'Dark' })).toBeChecked();
		expect(await theme(page)).toEqual({ attr: 'dark', stored: 'dark', bg: 'rgb(9, 9, 11)' });
		await group.getByText('System', { exact: true }).click();
		expect(await theme(page)).toEqual({ attr: null, stored: null, bg: 'rgb(250, 250, 250)' });
		// Light holds on a dark computer.
		await page.emulateMedia({ colorScheme: 'dark' });
		await expect.poll(async () => (await theme(page)).bg).toBe('rgb(9, 9, 11)');
		await group.getByText('Light', { exact: true }).click();
		expect(await theme(page)).toEqual({
			attr: 'light',
			stored: 'light',
			bg: 'rgb(250, 250, 250)'
		});
	});

	// With every stylesheet held back, the page shows what app.html paints
	// on its own: the stored Dark, on a light computer.
	for (const path of ['/', '/projects']) {
		test(`a stored Dark paints ${path} dark before any CSS`, async ({ page }) => {
			await page.emulateMedia({ colorScheme: 'light' });
			await page.addInitScript(() => localStorage.setItem('hk-theme', 'dark'));
			await page.route('**/*.css', (route) => route.abort());
			await page.goto(path, { waitUntil: 'domcontentloaded' });
			const paint = await page.evaluate(() => ({
				bg: getComputedStyle(document.documentElement).backgroundColor,
				scheme: getComputedStyle(document.documentElement).colorScheme,
				chrome: [...document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')].map(
					(m) => m.content
				)
			}));
			expect(paint).toEqual({
				bg: 'rgb(9, 9, 11)',
				scheme: 'dark',
				chrome: ['#09090b', '#09090b']
			});
		});
	}
});

// The root error page (Guide B 12.22): a path nothing serves keeps the
// header and the footer, says Not found, and offers the way back.
test('an unknown path signed in keeps the dashboard header and offers Projects', async ({
	page
}) => {
	await signIn(page);
	await page.goto('/no-such-place');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	await expect(page).toHaveTitle('Not found · repose');
	await expect(page.locator('main#main')).toHaveCount(1);
	// The rail stays beside the error (Guide B 12.22, I-841), with no
	// place current.
	const places = page.getByRole('navigation', { name: 'Dashboard' });
	await expect(places.getByRole('link', { name: 'Projects', exact: true })).toBeVisible();
	await expect(places.locator('[aria-current="page"]')).toHaveCount(0);
	await page.getByRole('link', { name: 'Back to Projects' }).click();
	await expect(page).toHaveURL('/projects');
});

test('a project that does not exist marks no place current', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await signIn(page);
	await page.goto('/projects/01900000-0000-7000-8000-0000000000ff');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	// The project layout draws this 404 itself, so page.error is unset; the
	// rail still marks nothing current (ia-final 1.2, I-841).
	const places = page.getByRole('navigation', { name: 'Dashboard' });
	await expect(places.locator('[aria-current="page"]')).toHaveCount(0);
	// Leaving for a real page brings the current place back.
	await places.getByRole('link', { name: 'Projects', exact: true }).click();
	await expect(places.getByRole('link', { name: 'Projects', exact: true })).toHaveAttribute(
		'aria-current',
		'page'
	);
});

test('an unknown path signed out renders Not found and stays put', async ({ page }) => {
	await page.goto('/no-such-place');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	await expect(page.getByRole('navigation', { name: 'Site' })).toBeVisible();
	await expect(page.getByRole('navigation', { name: 'Footer' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Docs', exact: true }).last()).toBeVisible();
	await page.waitForTimeout(500);
	await expect(page).toHaveURL('/no-such-place');
});

// Touch targets (Guide B 1.7): at 390 on a touch screen every control a
// thumb hits measures 44px or more, in the header and in each page's main
// (every page is on the kit since Phase 3, I-837).
const TOUCH_PAGES = [
	'/',
	'/docs/cli',
	'/projects',
	'/questions',
	'project',
	'project/events',
	'project/usage',
	'project/snapshots',
	'project/config',
	'/billing',
	'/settings',
	'/settings/notifications',
	'/settings/secrets',
	'/settings/machine-nix',
	'/no-such-place'
];

async function shortTargets(page: Page, scope: string): Promise<string[]> {
	return page.locator(scope).evaluateAll((roots) => {
		const out: string[] = [];
		for (const root of roots) {
			const els = root.querySelectorAll<HTMLElement>(
				'button, a[role=button], input, select, [role=tab], label:has(input)'
			);
			for (const el of els) {
				const r = el.getBoundingClientRect();
				if (!r.width || !r.height || !el.checkVisibility()) continue;
				// A box or a radio inside its label: the label is the target.
				if (el instanceof HTMLInputElement && el.closest('label')) continue;
				if (el instanceof HTMLInputElement && /^(hidden|radio|checkbox)$/.test(el.type)) continue;
				// A hidden file picker a labelled button opens (secrets' "Choose a file").
				if (el instanceof HTMLInputElement && el.type === 'file' && el.tabIndex < 0) continue;
				if (r.height < 43.5) {
					const name = (el.getAttribute('aria-label') ?? el.textContent ?? '').trim();
					out.push(`${el.tagName.toLowerCase()} "${name.slice(0, 30)}" ${Math.round(r.height)}px`);
				}
			}
			// A link that stands alone on its line (the only text of its
			// parent, or an hk-tap or hk-more link) is a control too; one
			// inside running text is exempt, and a row link whose ::after
			// covers its row is as tall as the row.
			for (const a of root.querySelectorAll<HTMLAnchorElement>('a[href]')) {
				const r = a.getBoundingClientRect();
				if (!r.width || !r.height || !a.checkVisibility()) continue;
				if (a.closest('nav, [role=tablist], .hk-tabs, .hk-seg--links')) continue;
				if (getComputedStyle(a, '::after').position === 'absolute') continue;
				const text = a.textContent?.trim() ?? '';
				const alone =
					a.matches('.hk-tap, .hk-more') || a.parentElement?.textContent?.trim() === text;
				if (alone && r.height < 43.5) {
					out.push(`a "${text.slice(0, 30)}" ${Math.round(r.height)}px`);
				}
			}
		}
		return out;
	});
}

test.describe('touch targets', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('every control in the header and in the main is 44px tall', async ({ page }) => {
		const p = await project({
			name: 'touch-app',
			remote_url: 'github.com/heracraft/touch-app'
		});
		await signIn(page);
		for (const path of TOUCH_PAGES) {
			const url = path.startsWith('project') ? path.replace('project', `/projects/${p.id}`) : path;
			await page.goto(url);
			await expect(page.locator('main#main')).toBeVisible();
			await page.waitForLoadState('networkidle');
			expect(await shortTargets(page, 'header'), `${url} header`).toEqual([]);
			expect(await shortTargets(page, 'main'), `${url} main`).toEqual([]);
			// The strip's places are links: each is a 44px row (I-841).
			const strip = page.getByRole('navigation', { name: 'Dashboard' }).getByRole('link');
			for (const h of await strip.evaluateAll((els) =>
				els.filter((el) => el.checkVisibility()).map((el) => el.getBoundingClientRect().height)
			)) {
				expect(h, `${url} strip`).toBeGreaterThanOrEqual(43.5);
			}
		}
	});
});

// The dashboard's rail (DECISIONS I-841): Projects, Questions, Billing and
// Settings beside the page from 1024px, a sticky strip under the header
// below it, the projects by name and the plan at the rail's foot.
test.describe('the dashboard rail', () => {
	test.beforeEach(async ({ page }) => {
		await signIn(page);
	});

	/** The frame's left edge: the 1200px frame centred, plus its gutter. */
	const frameLeft = (page: Page) =>
		page.evaluate(() => {
			const cw = document.querySelector('header')!.getBoundingClientRect().width;
			const gutter = Math.min(40, Math.max(20, 0.03 * innerWidth));
			return (cw - Math.min(cw, 1200)) / 2 + gutter;
		});

	for (const width of [1440, 1024]) {
		test(`the rail is 200px at the frame's edge and the page 48px past it at ${width}`, async ({
			page
		}) => {
			await page.setViewportSize({ width, height: 900 });
			await page.goto('/projects');
			await expect(page.getByRole('heading', { level: 1, name: 'Projects' })).toBeVisible();
			const left = await frameLeft(page);
			const rail = (await page.locator('[data-dash-strip]').boundingBox())!;
			const main = (await page.locator('main#main').boundingBox())!;
			expect(rail.x).toBeCloseTo(left, 0);
			expect(rail.width).toBeCloseTo(200, 0);
			expect(main.x).toBeCloseTo(left + 248, 0);
			// The rail is the window under the header less the compact
			// footer's 71px row, its plan at the foot.
			expect(rail.y).toBeCloseTo(57, 0);
			expect(rail.y + rail.height).toBeCloseTo(900 - 71, 0);
		});
	}

	// The rail never slides under the header at a page's foot, and its
	// plan sits at one height on a long page and a short one (review IA).
	test('the rail holds still at the foot of a long page and a short one', async ({ page }) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		const tops: number[] = [];
		for (const path of ['/projects', '/questions', '/settings/secrets', '/no-such-place']) {
			await page.goto(path);
			await expect(page.locator('main#main')).toBeVisible();
			await page.waitForLoadState('networkidle');
			await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
			const first = page.getByRole('navigation', { name: 'Dashboard' }).getByRole('link').first();
			const box = (await first.boundingBox())!;
			expect(box.y, `${path} first place`).toBeGreaterThanOrEqual(57);
			tops.push((await page.locator('[data-dash-strip]').boundingBox())!.y);
		}
		for (const y of tops) expect(y).toBeCloseTo(57, 0);
	});

	for (const width of [390, 360]) {
		test(`the strip under the header holds the four places at ${width}`, async ({ page }) => {
			await page.setViewportSize({ width, height: 844 });
			await page.goto('/projects');
			const nav = page.getByRole('navigation', { name: 'Dashboard' });
			for (const name of ['Projects', 'Questions', 'Billing', 'Settings']) {
				await expect(
					nav
						.getByRole('link', { name, exact: true })
						.or(nav.getByRole('link', { name: new RegExp(`^${name},`) }))
				).toBeInViewport({ ratio: 1 });
			}
			// They fit: the strip cannot be dragged sideways.
			const fits = await nav
				.locator('ul')
				.first()
				.evaluate((ul) => ul.scrollWidth <= ul.clientWidth);
			expect(fits).toBe(true);
			const strip = (await page.locator('[data-dash-strip]').boundingBox())!;
			expect(strip.y + strip.height).toBeCloseTo(101, 0);
			// It sticks under the header.
			await page.evaluate(() => scrollTo(0, 600));
			await expect
				.poll(async () => (await page.locator('[data-dash-strip]').boundingBox())!.y)
				.toBeCloseTo(57, 0);
		});
	}

	test('no dashboard page scrolls sideways', async ({ page }) => {
		const p = await project({ name: 'wide-app', remote_url: 'github.com/heracraft/wide-app' });
		for (const width of [320, 390, 768, 1024, 1280]) {
			await page.setViewportSize({ width, height: 900 });
			for (const path of [
				'/projects',
				'/questions',
				`/projects/${p.id}`,
				'/billing',
				'/settings'
			]) {
				await page.goto(path);
				await expect(page.locator('main#main')).toBeVisible();
				await page.waitForLoadState('networkidle');
				const wide = await page.evaluate(() => document.documentElement.scrollWidth - innerWidth);
				expect(wide, `${path} at ${width}`).toBeLessThanOrEqual(0);
			}
		}
	});

	test('one place is current on every dashboard page', async ({ page }) => {
		const p = await project({
			name: 'current-app',
			remote_url: 'github.com/heracraft/current-app'
		});
		await page.setViewportSize({ width: 1440, height: 900 });
		// A project page's current item is its rail row, or Projects when
		// the row is past the eight the rail lists.
		const row = new RegExp(`${p.name}|^Projects$`);
		const routes: [string, string | RegExp][] = [
			['/projects', 'Projects'],
			['/questions', 'Questions'],
			['/billing', 'Billing'],
			['/settings', 'Settings'],
			['/settings/notifications', 'Settings'],
			['/settings/secrets', 'Settings'],
			['/settings/machine-nix', 'Settings'],
			[`/projects/${p.id}`, row],
			[`/projects/${p.id}/config`, row],
			[`/projects/${p.id}/events`, row]
		];
		const nav = page.getByRole('navigation', { name: 'Dashboard' });
		for (const [path, name] of routes) {
			await page.goto(path);
			await expect(page.locator('main#main')).toBeVisible();
			const current = nav.locator('[aria-current="page"]');
			await expect(current, path).toHaveCount(1);
			await expect(current, path).toContainText(name);
		}
		// The strip draws no project rows, so on a project's tabs Projects
		// is the current place, shown and named (ia-final 1.2's fallback).
		for (const width of [390, 768]) {
			await page.setViewportSize({ width, height: 844 });
			for (const path of [`/projects/${p.id}`, `/projects/${p.id}/config`]) {
				await page.goto(path);
				await expect(page.locator('main#main')).toBeVisible();
				const current = nav.locator('[aria-current="page"]');
				await expect(current, `${path} at ${width}`).toHaveCount(1);
				await expect(current, `${path} at ${width}`).toHaveText('Projects');
				await expect(current, `${path} at ${width}`).toBeVisible();
			}
		}
	});

	// One primary at most per page (ia-final 2.3, Guide B 1.4).
	test('each page has at most its one primary button', async ({ page }) => {
		const p = await project({
			name: 'primary-app',
			remote_url: 'github.com/heracraft/primary-app'
		});
		await page.setViewportSize({ width: 1440, height: 900 });
		const pages: [string, number][] = [
			['/projects', 0],
			['/questions', 0],
			['/settings', 0],
			['/settings/notifications', 0],
			['/settings/secrets', 0],
			['/settings/machine-nix', 1],
			['/billing', 1],
			[`/projects/${p.id}`, 1],
			[`/projects/${p.id}/events`, 1],
			[`/projects/${p.id}/config`, 1]
		];
		for (const [path, most] of pages) {
			await page.goto(path);
			await expect(page.locator('main#main')).toBeVisible();
			await page.waitForLoadState('networkidle');
			const n = await page.locator('main .hk-btn').filter({ visible: true }).count();
			expect(n, path).toBeLessThanOrEqual(most);
		}
	});

	test('the title counts the questions waiting', async ({ page }) => {
		const p = await project({ name: 'title-app', remote_url: 'github.com/heracraft/title-app' });
		const asked = [
			await addQuestion(p.id, { agent: 'claude', text: 'First?', options: ['yes', 'no'] }),
			await addQuestion(p.id, { agent: 'codex', text: 'Second?' })
		];
		try {
			await page.goto('/questions');
			const badge = page.getByTestId('pending-questions');
			await expect(badge).toBeVisible();
			const n = Number(await badge.textContent());
			expect(n).toBeGreaterThanOrEqual(2);
			await expect(page).toHaveTitle(`(${n}) Questions · repose`);
			await expect(
				page.getByRole('navigation', { name: 'Dashboard' }).getByRole('link', {
					name: `Questions, ${n} waiting`
				})
			).toBeVisible();
			// The project's rail row turns amber and says so.
			await expect(
				page.getByRole('link', { name: new RegExp(`^${p.name}, \\w+, waiting on you$`) })
			).toBeVisible();
		} finally {
			for (const q of asked) await closeQuestion(q.id);
		}
	});

	test('the rail foot shows the plan, and leaves it off Billing', async ({ page }) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		try {
			await setBilling({ mode: 'active', plan: 'pro' });
			await page.goto('/projects');
			const foot = page.getByTestId('plan-strip');
			await expect(foot).toContainText('Plan');
			await expect(foot).toContainText('Pro');
			await expect(foot).toContainText(/\d+ GB of 32 GB running/);
			await page.goto('/billing');
			await expect(page.getByRole('heading', { level: 1, name: 'Billing' })).toBeVisible();
			await expect(page.getByTestId('plan-strip')).toHaveCount(0);

			await setBilling({ mode: 'past_due', plan: 'pro' });
			await page.goto('/settings');
			const failed = page.getByTestId('plan-strip').getByRole('link', { name: 'Payment failed' });
			await expect(failed).toHaveAttribute('href', '/billing');
			// Below 1024px the trouble shows as one line under the strip.
			await page.setViewportSize({ width: 390, height: 844 });
			await expect(page.getByRole('link', { name: 'Update card' }).first()).toBeVisible();
		} finally {
			await resetBilling();
		}
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		await expect(page.getByRole('heading', { level: 1, name: 'Projects' })).toBeVisible();
		// Billing switched off: no plan to show.
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('plan-strip')).toHaveCount(0);
	});

	test('a project row in the rail opens the project and is current there', async ({ page }) => {
		const p = await project({
			name: 'rail-row-app',
			remote_url: 'github.com/heracraft/rail-row-app'
		});
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		const row = page
			.getByRole('navigation', { name: 'Dashboard' })
			.getByRole('link', { name: new RegExp(`^${p.name}, `) });
		await row.click();
		await expect(page).toHaveURL(`/projects/${p.id}`);
		await expect(row).toHaveAttribute('aria-current', 'page');
	});
});

// The I-664 check on the dashboard's main pages (ia-final 1.6): a first
// load moves nothing that is on screen, at 1440 and at 390, and a
// payment failure seen first on a fresh browser paints its plan line
// with the page instead of pushing the page down.
test.describe('first loads hold still', () => {
	test.beforeEach(async ({ page }) => {
		await signIn(page);
		await page.addInitScript(() => {
			const w = window as unknown as { __cls: number };
			w.__cls = 0;
			new PerformanceObserver((list) => {
				for (const e of list.getEntries() as unknown as {
					value: number;
					hadRecentInput: boolean;
				}[]) {
					if (!e.hadRecentInput) w.__cls += e.value;
				}
			}).observe({ type: 'layout-shift', buffered: true });
		});
	});

	async function cls(page: Page, path: string): Promise<number> {
		await page.goto(path);
		await expect(page.locator('main#main')).toBeVisible();
		await page.waitForLoadState('networkidle');
		await page.waitForTimeout(800);
		return page.evaluate(() => (window as unknown as { __cls: number }).__cls);
	}

	test('six pages at 1440 and 390', async ({ page }) => {
		const p = await project({ name: 'still-app', remote_url: 'github.com/heracraft/still-app' });
		const paths = [
			'/projects',
			'/questions',
			`/projects/${p.id}`,
			`/projects/${p.id}/events`,
			'/billing',
			'/settings'
		];
		try {
			await setBilling({ mode: 'active', plan: 'pro' });
			for (const [width, height] of [
				[1440, 900],
				[390, 844]
			]) {
				await page.setViewportSize({ width, height });
				for (const path of paths) {
					expect(await cls(page, path), `${path} at ${width}`).toBeLessThanOrEqual(0.05);
				}
			}
		} finally {
			await resetBilling();
		}
	});

	test('a payment failure on a fresh browser at 390', async ({ page }) => {
		await page.addInitScript(() => {
			try {
				localStorage.removeItem('repose-billing-mode');
			} catch {
				// Storage blocked: the same cold start.
			}
		});
		await page.setViewportSize({ width: 390, height: 844 });
		try {
			await setBilling({ mode: 'past_due', plan: 'pro' });
			for (const path of ['/projects', '/questions', '/settings']) {
				expect(await cls(page, path), path).toBeLessThanOrEqual(0.05);
				await expect(page.locator('.plan-line')).toContainText('Payment failed');
			}
		} finally {
			await resetBilling();
		}
	});
});
