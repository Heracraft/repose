// A host the user owns that has stopped answering (DECISIONS I-1370). A
// Mac mini on a desk is asleep, rebooting or off the Wi-Fi most of the
// day, so this is the line its owner reads more than any other after the
// first day: it names the machine, says how long it has been quiet, and
// stops.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, destroyProject, setOfflineOwnHost } from './helpers';

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

test("an offline host of the user's own is named in the list and on the machine's page", async ({
	page
}) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'own-host-app',
		remote_url: 'github.com/heracraft/own-host-app'
	});
	try {
		await setOfflineOwnHost(p.id, 'mac-mini', 14);

		await page.goto('/projects');
		const row = page
			.locator('table.ptable tbody tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) });
		await expect(row).toContainText('mac-mini has not answered for 14m');
		// The old line, which told a Mac mini owner nothing, is gone.
		await expect(row).not.toContainText('server not answering');
		await shots(page, 'offline-own-host-list');

		// The same words on the machine's own page, in the banner.
		await page.goto(`/projects/${p.id}`);
		const banner = page.getByTestId('host-unreachable');
		await expect(banner).toContainText('mac-mini has not answered for 14m');
		await expect(banner).not.toContainText('The server running this machine');
		await shots(page, 'offline-own-host');

		// At 390 the stacked row carries it too.
		await page.setViewportSize({ width: 390, height: 844 });
		await page.goto('/projects');
		const stacked = page
			.locator('ul.mgroup li.mrow')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) })
			.first();
		await expect(stacked).toContainText('mac-mini has not answered for 14m');
	} finally {
		await destroyProject(api, p.id);
	}
});

test("a host of repose's that is quiet still says whose it is", async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'hosted-quiet-app',
		remote_url: 'github.com/heracraft/hosted-quiet-app'
	});
	try {
		// host_unreachable with no host of the user's behind it: the
		// account owns nothing, so there is no name to give.
		await page.route(/\/v1\/projects$/, async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const list = (await res.json()) as Record<string, unknown>[];
			for (const q of list) if (q.id === p.id) q.host_unreachable = true;
			await route.fulfill({ response: res, json: list });
		});
		await page.goto('/projects');
		const row = page
			.locator('table.ptable tbody tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) });
		await expect(row).toContainText('server not answering');
	} finally {
		await destroyProject(api, p.id);
	}
});
