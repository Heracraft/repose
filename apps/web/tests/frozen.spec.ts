// A frozen machine (DECISIONS I-683, I-818): nobody is using it, so its
// cores are stopped and every process is held. The api sends
// `resting` for real now, so the look is no longer behind a flag and this
// suite, which runs the production build, guards that it shows.
import { test, expect, type Locator, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, destroyProject, setResting } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

/**
 * The viewport at 1440 and 390 in both schemes, at 1x, for the review
 * CLAUDE.md asks for ("Judge visuals at real size"). Written only with
 * REPOSE_SHOTS_DIR set, as multiplexer.spec.ts does.
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

/** How often the two words a frozen machine can carry appear in one container. */
async function marks(el: Locator): Promise<{ frozen: number; unused: number }> {
	const text = (await el.innerText()).toLowerCase();
	return {
		frozen: (text.match(/frozen/g) ?? []).length,
		unused: (text.match(/unused/g) ?? []).length
	};
}

test('a frozen machine is marked in the list and on its page', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'resting-app',
		remote_url: 'github.com/heracraft/resting-app'
	});
	try {
		await setResting(p.id, 30);
		await page.goto('/projects');
		const cell = page
			.getByRole('region', { name: 'Projects' })
			.locator('tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) })
			.locator('td')
			.nth(1);
		await expect(cell).toContainText('frozen');
		await expect(cell).toContainText('for 30m');
		await expect(page.locator('[data-frozen]')).not.toHaveCount(0);

		await page.goto(`/projects/${p.id}`);
		// The state stays running on the wire; the word a reader sees is
		// frozen (I-818 S2, S3).
		await expect(page.getByTestId('project-state')).toHaveText('frozen');
		const band = page.getByTestId('frozen');
		await expect(band).toHaveText('Frozen for 30m.');
		// The band says how long and stops: what wakes it is the docs'
		// answer, not the screen's (I-484, I-485).
		await expect(band).not.toContainText('connect');
		await expect(page.getByTestId('machine-card')).toContainText('frozen');
		await shots(page, 'frozen-project');

		// The marker goes with the field, with no reload of its own.
		await setResting(p.id, -1);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText('running');
		await expect(page.getByTestId('frozen')).toHaveCount(0);
	} finally {
		await destroyProject(api, p.id);
	}
});

// The table replaced `unused` with `frozen` while the stacked row printed
// both, so a machine that was frozen and unused said it twice on a phone
// and once on a desktop.
test('a frozen and unused machine says each thing once at 1440 and at 390', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		// A name with neither word in it: the row prints the name and the
		// remote, and this test counts the two words in the row.
		name: 'both-marks-app',
		remote_url: 'github.com/heracraft/both-marks-app'
	});
	try {
		await page.route(/\/v1\/projects$/, async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const list = (await res.json()) as Record<string, unknown>[];
			for (const q of list) {
				if (q.id !== p.id) continue;
				q.state = 'running';
				q.resting = { since: new Date(Date.now() - 42 * 60_000).toISOString() };
				q.idle = { since: new Date(Date.now() - 1565 * 60_000).toISOString(), hourly_cents: 0 };
			}
			await route.fulfill({ response: res, json: list });
		});

		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		const cell = page
			.getByRole('region', { name: 'Projects' })
			.locator('tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) })
			.locator('td')
			.nth(1);
		await expect(cell).toContainText('for 42m');
		await expect(cell).toContainText('unused 26h');
		expect(await marks(cell)).toEqual({ frozen: 1, unused: 1 });

		await page.setViewportSize({ width: 390, height: 844 });
		await page.goto('/projects');
		const stacked = page
			.getByRole('list', { name: 'Projects' })
			.locator('li')
			.filter({ has: page.getByRole('link', { name: new RegExp(p.name) }) })
			.first();
		await expect(stacked).toContainText('for 42m');
		await expect(stacked).toContainText('unused 26h');
		expect(await marks(stacked)).toEqual({ frozen: 1, unused: 1 });
		await shots(page, 'frozen-list');
	} finally {
		await destroyProject(api, p.id);
	}
});

test('Keep this machine awake patches the project and sticks', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'keep-awake-app',
		remote_url: 'github.com/heracraft/keep-awake-app'
	});
	try {
		// keep_awake is the api's field; the fake api does not carry it, so
		// the route holds it for this test and answers the PATCH as the api
		// does, with the whole project.
		let keep = false;
		let last: Record<string, unknown> = {};
		const patched: boolean[] = [];
		await page.route(new RegExp(`/v1/projects/${p.id}$`), async (route) => {
			const req = route.request();
			if (req.method() === 'PATCH') {
				const body = req.postDataJSON() as { keep_awake?: boolean };
				if (body.keep_awake !== undefined) {
					keep = body.keep_awake;
					patched.push(keep);
				}
				return route.fulfill({
					status: 200,
					contentType: 'application/json',
					body: JSON.stringify({ ...last, keep_awake: keep })
				});
			}
			if (req.method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			last = { ...((await res.json()) as Record<string, unknown>), keep_awake: keep };
			return route.fulfill({ response: res, json: last });
		});

		await page.goto(`/projects/${p.id}/config`);
		const box = page.getByRole('checkbox', { name: 'Keep this machine awake' });
		await expect(box).not.toBeChecked();
		await box.check();
		await expect(box).toBeChecked();
		expect(patched).toEqual([true]);

		await page.reload();
		await expect(page.getByRole('checkbox', { name: 'Keep this machine awake' })).toBeChecked();
		await shots(page, 'keep-awake-config');

		await page.getByRole('checkbox', { name: 'Keep this machine awake' }).uncheck();
		await expect(page.getByRole('checkbox', { name: 'Keep this machine awake' })).not.toBeChecked();
		expect(patched).toEqual([true, false]);
	} finally {
		await destroyProject(api, p.id);
	}
});

// The frost itself (Frost.svelte, the I-841 amendment of 2026-10-21): the
// drawing a frozen row carries, where an edge meets a rule. While the look
// was a mock these ran on a second server started with PUBLIC_FLAG_FROZEN;
// `resting` is real and no flag gates it (I-818, I-826), so they run on the
// same build as the rest of the suite.
test.describe('the frost a frozen row draws', () => {
	/** A frozen project searched for alone, so its row is the last of its group. */
	async function lastFrozenRow(page: Page, name: string) {
		const p = await createProject(apiURLFromEnv(), {
			name,
			remote_url: `github.com/heracraft/${name}`
		});
		await setResting(p.id, 90);
		await page.goto(`/projects?q=${encodeURIComponent(p.name)}`);
		return p;
	}

	/** The foot's frost box against its row's: where its bottom sits, and
	 *  whether it paints the rule. */
	function footOf(row: Locator) {
		return row.evaluate((r) => {
			const foot = r.querySelector('.hk-frost--row') as HTMLElement;
			const rb = r.getBoundingClientRect();
			const fb = foot.getBoundingClientRect();
			return {
				below: Math.round(fb.bottom - rb.bottom),
				rule: getComputedStyle(foot).backgroundImage
			};
		});
	}

	test('the last frozen row of a group keeps a rule under its frost', async ({ page }) => {
		const p = await lastFrozenRow(page, 'frost-last');
		try {
			const row = page.locator('tr[data-frost-on="row"]', { hasText: p.name });
			await expect(row).toBeVisible();
			await expect(page.locator('tbody tr.prow')).toHaveCount(1);
			const foot = await footOf(row);
			expect(foot.below).toBe(1);
			expect(foot.rule).toContain('linear-gradient');
			// The foot is the whole of it: no strip up the leading edge.
			await expect(row.locator('.hk-frost')).toHaveCount(1);

			await page.setViewportSize({ width: 390, height: 844 });
			const li = page.locator('li[data-frost-on="row"]', { hasText: p.name });
			await expect(li).toBeVisible();
			const phone = await footOf(li);
			expect(phone.below).toBe(1);
			expect(phone.rule).toContain('linear-gradient');
			await shots(page, 'frozen-frost-row');
		} finally {
			await destroyProject(apiURLFromEnv(), p.id);
		}
	});

	test('the frost grows in under the pointer, at once with reduced motion', async ({ page }) => {
		const p = await lastFrozenRow(page, 'frost-hover');
		try {
			const row = page.locator('tr[data-frost-on="row"]', { hasText: p.name });
			await expect(row).toBeVisible();
			const g = () =>
				row.evaluate((r) => Number(getComputedStyle(r).getPropertyValue('--frost-g')));
			expect(await g()).toBe(0);
			await row.hover();
			await page.waitForTimeout(120);
			const mid = await g();
			expect(mid).toBeGreaterThan(0);
			expect(mid).toBeLessThan(1);
			await expect.poll(g).toBe(1);
			await page.mouse.move(0, 0);
			await expect.poll(g).toBe(0);

			await page.emulateMedia({ reducedMotion: 'reduce' });
			await row.hover();
			expect(await g()).toBe(1);
		} finally {
			await destroyProject(apiURLFromEnv(), p.id);
		}
	});
});
