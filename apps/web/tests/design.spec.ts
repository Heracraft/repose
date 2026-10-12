// The design rules that no other spec exercises: the keyboard path through
// a page, forced colours, reduced motion, and the phone column
// (DESIGN-LANGUAGE.md; DECISIONS I-377, I-391, I-392). Lighthouse (tests-a11y)
// scores the pages at rest; these drive them.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, fail, unfail } from './helpers';

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
		const p = await createProject(apiURLFromEnv(), {
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
		const p = await createProject(apiURLFromEnv(), {
			name: 'forced-colours-app',
			remote_url: 'github.com/heracraft/forced-colours-app'
		});
		await page.emulateMedia({ forcedColors: 'active' });
		await page.goto(`/projects/${p.id}`);
		const dot = page.locator('.dot--good').first();
		await expect(dot).toBeVisible();
		const { fill, edge } = await dot.evaluate((el) => {
			const s = getComputedStyle(el);
			return { fill: s.backgroundColor, edge: s.borderTopColor };
		});
		expect(fill).not.toBe('rgba(0, 0, 0, 0)');
		expect(fill).toBe(edge);
	});

	test('forced colours mark the current config tab and no other', async ({ page }) => {
		const p = await createProject(apiURLFromEnv(), {
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
});

// DECISIONS I-655, I-656: one header on every kind of page, the same
// items in the same order, its logo at the same x and going home, and a
// Feedback link in the header and the footer of every page.
const FRAME_PAGES = ['/', '/docs/cli', '/terms', '/projects'];

for (const width of [320, 360, 390, 1440]) {
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
				// Signed in, the places sit beside the logo and Pricing goes
				// (I-679): Docs, Feedback, GitHub and Account from sm; below
				// it Account alone, which holds Docs and Feedback.
				expect(count, path).toBe(width >= 640 ? 4 : 1);
				const places = page.getByRole('navigation', { name: 'Dashboard' });
				await expect(places.getByRole('link', { name: /^Projects/ })).toBeInViewport({ ratio: 1 });
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

	test('monospace text draws "://" with no ligature gap', async ({ page }) => {
		await page.goto('/docs/install');
		const code = page.locator('.doc pre code').first();
		await expect(code).toBeVisible();
		expect(await code.evaluate((el) => getComputedStyle(el).fontVariantLigatures)).toBe(
			'no-contextual'
		);
	});
});
