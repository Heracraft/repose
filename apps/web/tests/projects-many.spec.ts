// An account with many projects, most of them stopped (I-841, amended
// 2026-10-21): /projects defaults to Running, which draws what is up or
// wants you and the five stopped machines that ran last, with a link to
// the rest; the rail lists what is up and folds the stopped ones into one
// row. The list is the api's answer, shaped here by page.route, since
// the fake holds 100 projects at most and the suite's account is shared.
import { test, expect, type Page } from '@playwright/test';
import { signIn } from './helpers';

const DAY = 86_400_000;

/** n projects: one asking, one failed, two running, the rest stopped a day apart. */
function account(n: number) {
	const now = Date.now();
	return Array.from({ length: n }, (_, i) => {
		const state = i < 3 ? 'running' : i === 3 ? 'error' : 'stopped';
		const at = new Date(now - (i + 1) * DAY).toISOString();
		const name = i === n - 1 ? 'needle-oldest' : `many-${String(i).padStart(3, '0')}`;
		return {
			id: `01900000-0000-7000-8000-${String(700000 + i).padStart(12, '0')}`,
			name,
			slug: name,
			remote_url: `https://github.com/many/${name}`,
			class: 'small',
			state,
			agent_default: 'claude',
			hold_base_updates: false,
			base_version: '2026.09.15',
			config_revision_id: 'r1',
			volume_bytes: 0,
			created_at: new Date(now - 400 * DAY).toISOString(),
			started_at: at,
			last_snapshot_at: state === 'stopped' ? at : null,
			last_error: state === 'error' ? 'boot_failed: the new system did not boot' : null,
			cost_today_cents: 0,
			cost_month_cents: 0
		};
	});
}

async function mock(page: Page, n: number) {
	const list = account(n);
	await page.route(/\/v1\/projects(\?.*)?$/, (route) =>
		route.request().method() === 'GET' ? route.fulfill({ json: list }) : route.fallback()
	);
	await page.route(/\/v1\/projects\/[^/]+\/events/, (route) => route.fulfill({ json: [] }));
	await page.route(/\/v1\/projects\/destroyed/, (route) => route.fulfill({ json: [] }));
	// many-000 has an agent waiting on an answer.
	await page.route(/\/v1\/questions(\?.*)?$/, (route) =>
		route.fulfill({
			json: {
				questions: [
					{
						id: 'q-many',
						project_id: list[0].id,
						project: list[0].slug,
						agent: 'claude',
						text: 'Ship it?',
						options: ['yes', 'no'],
						state: 'pending',
						answer: null,
						answered_via: null,
						created_at: new Date().toISOString(),
						expires_at: new Date(Date.now() + 1800_000).toISOString(),
						answered_at: null
					}
				]
			}
		})
	);
	return list;
}

async function names(page: Page): Promise<string[]> {
	const cells = page.locator('.hk-table-wrap').locator('tbody tr .pname');
	await cells.first().waitFor({ state: 'attached' });
	return cells.allInnerTexts();
}

test.beforeEach(async ({ page }) => {
	await signIn(page);
	await page.setViewportSize({ width: 1440, height: 900 });
});

for (const n of [40, 150]) {
	test(`${n} projects: Running is the default, with five stopped and a link to the rest`, async ({
		page
	}) => {
		await mock(page, n);
		await page.goto('/projects');
		await expect(page.getByText(`${n} projects`)).toBeVisible();
		const filters = page.getByRole('navigation', { name: 'Filter projects' });
		await expect(filters.getByRole('link', { name: 'Running' })).toHaveAttribute(
			'aria-current',
			'page'
		);
		const table = page.locator('.hk-table-wrap');
		await expect(table.locator('tbody[data-band=waiting] .pname')).toHaveText([
			'many-000',
			'many-003'
		]);
		await expect(table.locator('tbody[data-band=up] .pname')).toHaveText(['many-001', 'many-002']);
		// The five that ran last, newest first.
		await expect(table.locator('tbody[data-band=off] .pname')).toHaveText([
			'many-004',
			'many-005',
			'many-006',
			'many-007',
			'many-008'
		]);
		const rest = page.getByTestId('all-stopped');
		await expect(rest).toHaveText(`All ${n - 4} stopped`);
		await rest.click();
		await expect(page).toHaveURL(/[?&]state=off/);
		// Stopped: every stopped one, 25 at a time, in the count the link
		// gave; the failed machine leads Running and is not counted again.
		await expect(page.getByRole('button', { name: `Show all ${n - 4}` })).toBeVisible();
		expect(await names(page)).toHaveLength(25);
	});

	test(`${n} projects: a search looks through the stopped ones too`, async ({ page }) => {
		await mock(page, n);
		await page.goto('/projects');
		await page.getByLabel('Search projects').fill('needle');
		await expect(page).toHaveURL(/[?&]q=needle/);
		await expect.poll(() => names(page)).toEqual(['needle-oldest']);
		await expect(
			page.getByRole('navigation', { name: 'Filter projects' }).getByRole('link', { name: 'All' })
		).toHaveAttribute('aria-current', 'page');
	});

	test(`${n} projects: the rail lists what is up and folds the stopped`, async ({ page }) => {
		const list = await mock(page, n);
		await page.goto('/projects');
		const rail = page.getByRole('navigation', { name: 'Dashboard' });
		await expect(rail.getByText('Projects').last()).toBeVisible();
		await expect(
			rail.getByRole('link', { name: /^many-000, running, waiting on you$/ })
		).toBeVisible();
		await expect(rail.getByRole('link', { name: /^many-003, error$/ })).toBeVisible();
		await expect(rail.getByRole('link', { name: /^many-001, running$/ })).toBeVisible();
		await expect(rail.getByRole('link', { name: /^many-004, / })).toHaveCount(0);
		const fold = rail.getByTestId('rail-stopped');
		await expect(fold).toHaveText(`${n - 4} stopped`);
		await expect(fold).toHaveAttribute('href', '/projects?state=off');
		// A stopped project's own page draws its row, current, above the fold.
		await page.route(/\/v1\/projects\/[^/?]+$/, (route) => route.fulfill({ json: list[10] }));
		await page.goto(`/projects/${list[10].id}`);
		await expect(rail.getByRole('link', { name: /^many-010, stopped$/ })).toHaveAttribute(
			'aria-current',
			'page'
		);
		await expect(fold).toHaveText(`${n - 5} stopped`);
	});
}

test('8 projects or fewer: no filter, every project listed', async ({ page }) => {
	await mock(page, 8);
	await page.goto('/projects');
	await expect(page.getByRole('navigation', { name: 'Filter projects' })).toHaveCount(0);
	expect(await names(page)).toHaveLength(8);
	await expect(page.getByTestId('rail-stopped')).toHaveCount(0);
});
