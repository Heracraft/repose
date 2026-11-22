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
	const prev = page.viewportSize();
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.emulateMedia({ colorScheme: null });
	if (prev) await page.setViewportSize(prev);
}

test("an offline host of the user's own is named in the list and on the machine's page", async ({
	page
}) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'own-host-app',
		remote_url: 'github.com/heracraft/own-host-app'
	});
	let hostID = '';
	try {
		hostID = await setOfflineOwnHost(p.id, 'mac-mini', 14);

		await page.goto('/projects');
		const row = page
			.locator('table.ptable tbody tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) });
		await expect(row).toContainText('your host mac-mini has not answered for 14m');
		// The old line, which told a Mac mini owner nothing, is gone.
		await expect(row).not.toContainText('server not answering');
		await shots(page, 'offline-own-host-list');

		// The same words on the machine's own page, in the banner.
		await page.goto(`/projects/${p.id}`);
		const banner = page.getByTestId('host-unreachable');
		await expect(banner).toContainText('your host mac-mini has not answered for 14m');
		await expect(banner).not.toContainText('The server running this machine');
		await shots(page, 'offline-own-host');

		// At 390 the stacked row carries it too.
		await page.setViewportSize({ width: 390, height: 844 });
		await page.goto('/projects');
		const stacked = page
			.locator('ul.mgroup li.mrow')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) })
			.first();
		await expect(stacked).toContainText('your host mac-mini has not answered for 14m');
	} finally {
		await destroyProject(api, p.id);
		// The quiet host would refuse every later file's creates, as the
		// api refuses a create while the account's host is offline
		// (I-1390); its machine is gone, so it can go too.
		if (hostID) {
			await fetch(`${api}/hosts/${hostID}`, {
				method: 'DELETE',
				headers: { Authorization: 'Bearer playwright' }
			});
		}
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
		await page.route(new RegExp(`/v1/projects(/${p.id})?$`), async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const body = await res.json();
			const rows = (Array.isArray(body) ? body : [body]) as Record<string, unknown>[];
			for (const q of rows) if (q.id === p.id) q.host_unreachable = true;
			await route.fulfill({ response: res, json: Array.isArray(body) ? rows : rows[0] });
		});
		// The page asks /hosts once per quiet machine and stops. An
		// account that owns none answers with an empty list, and a page
		// that treated that as "not read yet" would ask again for ever.
		let reads = 0;
		await page.route(/\/v1\/hosts$/, async (route) => {
			if (route.request().method() === 'GET') reads++;
			await route.fallback();
		});
		await page.goto('/projects');
		const row = page
			.locator('table.ptable tbody tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) });
		await expect(row).toContainText('server not answering');

		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('host-unreachable')).toContainText(
			'The server running this machine is not answering'
		);
		await page.waitForTimeout(2000);
		expect(reads, 'reads of /hosts').toBeLessThanOrEqual(3);
	} finally {
		await destroyProject(api, p.id);
	}
});

// ENG-68 & ENG-116 (DECISIONS I-1392): once any listed project runs on a
// host the account owns, /projects shows a Host column (host name, `repose`,
// or `-`) and each project's detail header shows where it runs — even while
// the host is healthy, and with zero GET /hosts requests.
test('healthy owned hosts show where each machine runs on /projects and project detail without reading /hosts', async ({
	page
}) => {
	const api = apiURLFromEnv();
	const own = await createProject(api, {
		name: 'own-readout-app',
		remote_url: 'github.com/heracraft/own-readout-app'
	});
	const fleet = await createProject(api, {
		name: 'fleet-readout-app',
		remote_url: 'github.com/heracraft/fleet-readout-app'
	});
	const unplaced = await createProject(api, {
		name: 'unplaced-readout-app',
		remote_url: 'github.com/heracraft/unplaced-readout-app'
	});
	// A stopped machine is the widest state cell: one line, the state and
	// "last run" side by side. Whether one is left on the list depends on
	// what ran before, so this spec brings its own.
	const stopped = await createProject(api, {
		name: 'stopped-readout-app',
		remote_url: 'github.com/heracraft/stopped-readout-app'
	});
	try {
		const patch = (q: Record<string, unknown>) => {
			if (q.id === own.id) {
				q.host_id = '00000000-0000-4000-8000-000000000001';
				q.host_name = 'mac-mini';
			} else if (q.id === fleet.id) {
				q.host_id = 'host-02';
				delete q.host_name;
			} else if (q.id === unplaced.id) {
				delete q.host_id;
				delete q.host_name;
			} else if (q.id === stopped.id) {
				q.host_id = 'host-02';
				delete q.host_name;
				q.state = 'stopped';
				q.created_at = new Date(Date.now() - 3 * 3600_000).toISOString();
				q.started_at = new Date(Date.now() - 42 * 60_000).toISOString();
				delete q.last_snapshot_at;
				delete q.idle;
				delete q.resting;
			}
		};
		await page.route(/\/v1\/projects(\/[^/]+)?$/, async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const body = await res.json();
			if (Array.isArray(body)) {
				for (const q of body as Record<string, unknown>[]) patch(q);
				await route.fulfill({ response: res, json: body });
			} else {
				patch(body as Record<string, unknown>);
				await route.fulfill({ response: res, json: body });
			}
		});
		let hostReads = 0;
		await page.route(/\/v1\/hosts$/, async (route) => {
			if (route.request().method() === 'GET') hostReads++;
			await route.fallback();
		});

		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		await expect(page.locator('.ptable thead th')).toContainText(['Host']);

		const rowOf = (name: string) =>
			page
				.locator('table.ptable tbody tr')
				.filter({ has: page.getByRole('link', { name, exact: true }) });
		await expect(rowOf(own.name).getByTestId('row-host')).toHaveText('mac-mini');
		await expect(rowOf(fleet.name).getByTestId('row-host')).toHaveText('repose');
		await expect(rowOf(unplaced.name).getByTestId('row-host')).toHaveText('-');

		// No cell clips at desktop/tablet widths with the Host column present:
		// the list as it opens, then this spec's machines under All, where
		// the stopped row is. A stopped row is one line, so its state cell
		// is the widest; the search keeps it on a list that caps at 20.
		const clippedCells = () =>
			page.locator('.ptable tbody td').evaluateAll((cells) =>
				cells
					.filter((c) => getComputedStyle(c).overflow === 'hidden')
					.filter((c) => c.scrollWidth > c.clientWidth + 1)
					.map(
						(c) =>
							`${c.className}: ${c.clientWidth} of ${c.scrollWidth} for ${c.textContent?.trim()}`
					)
			);
		for (const url of ['/projects', '/projects?state=all&q=readout-app']) {
			await page.setViewportSize({ width: 1440, height: 900 });
			await page.goto(url);
			await expect(page.locator('.ptable thead th')).toContainText(['Host']);
			if (url !== '/projects') {
				await expect(rowOf(stopped.name).locator('td.c-state')).toContainText('last run 42m ago');
			}
			for (const width of [1440, 1024, 800]) {
				await page.setViewportSize({ width, height: 900 });
				expect(await clippedCells(), `clipped at ${width} on ${url}`).toEqual([]);
			}
		}
		await shots(page, 'own-host-list');
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');

		// Stacked phone rows carry the host readout too.
		await page.setViewportSize({ width: 390, height: 844 });
		const mrowOf = (name: string) =>
			page
				.locator('ul.mgroup li.mrow')
				.filter({ has: page.getByRole('link', { name, exact: true }) })
				.first();
		await expect(mrowOf(own.name).getByTestId('row-host')).toHaveText('mac-mini');
		await expect(mrowOf(fleet.name).getByTestId('row-host')).toHaveText('repose');
		await expect(mrowOf(unplaced.name).getByTestId('row-host')).toHaveText('-');

		// Clicking into the owned-host machine shows its host in the header on every tab.
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		await rowOf(own.name).getByRole('link', { name: own.name, exact: true }).click();
		await expect(page.getByTestId('project-host')).toHaveText('on mac-mini');
		await shots(page, 'own-host-detail');
		await page
			.getByRole('navigation', { name: 'Project' })
			.getByRole('link', { name: 'Events', exact: true })
			.click();
		await expect(page.getByTestId('project-host')).toHaveText('on mac-mini');

		// A fleet machine and an unplaced machine also show where they run
		// once the list has a machine on an owned host.
		await page.goto('/projects');
		await rowOf(fleet.name).getByRole('link', { name: fleet.name, exact: true }).click();
		await expect(page.getByTestId('project-host')).toHaveText('on repose');

		await page.goto('/projects');
		await rowOf(unplaced.name).getByRole('link', { name: unplaced.name, exact: true }).click();
		await expect(page.getByTestId('project-host')).toHaveText('host -');

		expect(hostReads, 'GET /hosts calls while all hosts are healthy').toBe(0);
	} finally {
		await destroyProject(api, own.id);
		await destroyProject(api, fleet.id);
		await destroyProject(api, stopped.id);
		await destroyProject(api, unplaced.id);
	}
});

// ENG-65: clicking into a broken project never tells the user less than the
// /projects row; the error banner shows across every tab, and Restart
// surfaces own-host capacity refusals instead of "No capacity right now."
test('a broken project shows its error reason on every tab and Restart surfaces own-host refusals', async ({
	page
}) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'broken-host-app',
		remote_url: 'github.com/heracraft/broken-host-app'
	});
	try {
		await page.route(`**/v1/projects/${p.id}`, async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const body = await res.json();
			await route.fulfill({
				response: res,
				json: {
					...body,
					state: 'error',
					last_error: 'capacity: your host stalehost has not registered yet'
				}
			});
		});
		await page.route(`**/v1/projects/${p.id}/start`, async (route) => {
			await route.fulfill({
				status: 503,
				contentType: 'application/json',
				json: {
					error: {
						code: 'capacity',
						message: 'your host stalehost has not answered for 14m',
						detail: { reason: 'no_own_host', host: 'stalehost', host_state: 'unreachable' }
					}
				}
			});
		});

		await page.goto(`/projects/${p.id}`);
		const banner = page.getByTestId('last-error');
		await expect(banner).toContainText('Your host stalehost has not registered yet.');
		await expect(banner.getByRole('link', { name: 'Errors' })).toBeVisible();
		await shots(page, 'project-error-detail');

		// Error reason remains visible when switching tabs.
		await page
			.getByRole('navigation', { name: 'Project' })
			.getByRole('link', { name: 'Config', exact: true })
			.click();
		await expect(page.getByTestId('last-error')).toContainText(
			'Your host stalehost has not registered yet.'
		);

		// Restart shows the real host refusal rather than "No capacity right now."
		await page.getByRole('button', { name: 'Restart', exact: true }).click();
		const refusal = page.getByTestId('capacity-refusal');
		await expect(refusal).toHaveText('Your host stalehost has not answered for 14m.');
		await expect(page.getByText('No capacity right now.')).toHaveCount(0);
	} finally {
		await destroyProject(api, p.id);
	}
});
