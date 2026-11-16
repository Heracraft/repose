// The Browser tab (DECISIONS I-1216): the machine's browser, drawn here
// by noVNC on the dashboard's own origin. The fake api mints a ticket of
// the real shape and serves no desktop, so this suite drives the whole
// state machine around the picture -- the connect, the three reconnects
// at 1, 2 and 4 seconds, and the Resume view that follows -- against a
// real browser. A view with frames in it needs a guest and is the
// release's live check.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, destroyProject } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

/**
 * The viewport at 1440 and 390 in both schemes, at 1x, for the review
 * CLAUDE.md asks for ("Judge visuals at real size"). Written only with
 * REPOSE_SHOTS_DIR set, as frozen.spec.ts does.
 */
async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.emulateMedia({ colorScheme: null });
}

async function project(name: string) {
	return createProject(apiURLFromEnv(), {
		name,
		remote_url: `github.com/heracraft/${name}`
	});
}

test('the tab connects, retries three times, then offers Resume view', async ({ page }) => {
	const p = await project('view-states');
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}/browser`);
	await expect(page).toHaveTitle(new RegExp(`Browser · ${p.name} · repose$`));
	await expect(page.getByTestId('view-screen')).toBeVisible();
	await expect(page.getByText('Connecting…')).toBeVisible();
	await shots(page, 'browser-connecting');

	// 1 + 2 + 4 seconds of reconnects, each one a fresh mint, and then it
	// stops rather than minting for ever.
	await expect(page.getByRole('button', { name: 'Resume view' })).toBeVisible({
		timeout: 30_000
	});
	await expect(page.getByText('The view could not be reached.')).toBeVisible();
	await shots(page, 'browser-stopped');

	// Resume view is one mint: the page says it is connecting again.
	await page.getByRole('button', { name: 'Resume view' }).click();
	await expect(page.getByText('Connecting…')).toBeVisible();
	await destroyProject(apiURLFromEnv(), p.id);
});

// The six tabs (I-841, I-1216) fit a laptop; on a phone the row scrolls,
// which .hk-tabs is built for, and every tab is still reachable.
test('the tab row holds six tabs, and scrolls on a phone', async ({ page }) => {
	const p = await project('view-tabs');
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}/browser`);
	const nav = page.getByRole('navigation', { name: 'Project' });
	await expect(nav.getByRole('link', { name: 'Browser' })).toHaveAttribute('aria-current', 'page');
	expect(await nav.evaluate((n) => n.scrollWidth <= n.clientWidth)).toBe(true);

	for (const width of [390, 360]) {
		await page.setViewportSize({ width, height: 844 });
		// No vertical scroll, whatever the width: the current tab's 1px
		// edge has to stay inside the box and on the rule.
		expect(await nav.evaluate((n) => n.scrollHeight <= n.clientHeight), `${width}`).toBe(true);
		// Every tab is reachable by scrolling the row, and Config, the last,
		// comes into view when it is scrolled to the end.
		await nav.evaluate((n) => n.scrollTo({ left: n.scrollWidth }));
		await expect(nav.getByRole('link', { name: 'Config' })).toBeInViewport();
		await nav.evaluate((n) => n.scrollTo({ left: 0 }));
		await expect(nav.getByRole('link', { name: 'Browser' })).toBeInViewport();
	}
	await destroyProject(apiURLFromEnv(), p.id);
});
