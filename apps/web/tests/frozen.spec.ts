// The frozen look (DECISIONS I-683; its frost, the I-841 amendment of
// 2026-10-21) is a mock behind a flag: the dev server shows it, a
// production build never does, even when an api sends `resting`. This
// suite runs the production build, so it guards that, then starts the same
// build with the flag on to check the frost a frozen row draws.
import { spawn, type ChildProcess } from 'node:child_process';
import path from 'node:path';
import { test, expect, type Locator, type Page } from '@playwright/test';
import { WEB_PORT } from './fixtures';
import { signIn, createProject, apiURLFromEnv, setResting } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('a production build shows no frozen machine, even when the api says resting', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'resting-app',
		remote_url: 'github.com/heracraft/resting-app'
	});
	await setResting(p.id, 30);
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: p.name, exact: true })).toBeVisible();
	await expect(page.locator('[data-frozen]')).toHaveCount(0);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-state')).toHaveText('running');
	await expect(page.getByTestId('frozen')).toHaveCount(0);
	// Nor its frost (Frost.svelte): not on the head, the Machine section or a row.
	await expect(page.locator('.hk-frost, [data-frost-on]')).toHaveCount(0);
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: p.name, exact: true })).toBeVisible();
	await expect(page.locator('.hk-frost, [data-frost-on]')).toHaveCount(0);
	await setResting(p.id, -1);
});

// The frost itself, on a second server of the same build with the flag on
// (PUBLIC_FLAG_FROZEN is read at run time), against the same fake api.
test.describe('a frozen row, with the flag on', () => {
	const port = WEB_PORT + 1;
	const origin = `http://127.0.0.1:${port}`;
	let web: ChildProcess | undefined;

	test.use({ baseURL: origin });

	test.beforeAll(async () => {
		web = spawn('node', ['build'], {
			cwd: path.resolve(import.meta.dirname, '..'),
			env: {
				...process.env,
				PORT: String(port),
				HOST: '127.0.0.1',
				PUBLIC_LOGTO_APP_ID: 'dashboard-test',
				PUBLIC_FLAG_FROZEN: '1'
			},
			stdio: 'ignore'
		});
		const deadline = Date.now() + 20_000;
		for (;;) {
			try {
				if ((await fetch(`${origin}/healthz`)).ok) break;
			} catch {
				// not up yet
			}
			if (Date.now() > deadline) throw new Error(`timed out waiting for ${origin}`);
			await new Promise((r) => setTimeout(r, 200));
		}
	});

	test.afterAll(() => {
		web?.kill('SIGTERM');
	});

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
		const row = page.locator('tr[data-frost-on="row"]', { hasText: p.name });
		await expect(row).toBeVisible();
		await expect(page.locator('tbody tr.prow')).toHaveCount(1);
		const foot = await footOf(row);
		expect(foot.below).toBe(1);
		expect(foot.rule).toContain('linear-gradient');
		// The leading edge comes down to meet it: one frosted L.
		await expect(row.locator('.hk-frost--side')).toHaveCount(1);

		await page.setViewportSize({ width: 390, height: 844 });
		const li = page.locator('li[data-frost-on="row"]', { hasText: p.name });
		await expect(li).toBeVisible();
		const phone = await footOf(li);
		expect(phone.below).toBe(1);
		expect(phone.rule).toContain('linear-gradient');
		await setResting(p.id, -1);
	});

	test('the frost grows in under the pointer, at once with reduced motion', async ({ page }) => {
		const p = await lastFrozenRow(page, 'frost-hover');
		const row = page.locator('tr[data-frost-on="row"]', { hasText: p.name });
		await expect(row).toBeVisible();
		const g = () => row.evaluate((r) => Number(getComputedStyle(r).getPropertyValue('--frost-g')));
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
		await setResting(p.id, -1);
	});
});
