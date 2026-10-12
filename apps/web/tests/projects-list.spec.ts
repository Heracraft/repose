// /projects as the dashboard's home (I-659): the plan strip, the questions
// waiting, rows that open their project from anywhere in them, and Start
// or Stop on a row.
import { test, expect, type Page } from '@playwright/test';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	addQuestion,
	setBilling,
	resetBilling
} from './helpers';

test.beforeEach(async ({ page }) => {
	await resetBilling();
	await signIn(page);
});

test.afterAll(async () => {
	await resetBilling();
});

/** The table row of a project, found by its name's link. */
function row(page: Page, name: string) {
	return page
		.getByRole('region', { name: 'Projects' })
		.locator('tr')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
}

test('the plan strip shows the memory running with a meter', async ({ page }) => {
	// The project first: a plan past its memory refuses a new one.
	await createProject(apiURLFromEnv(), {
		name: 'strip-app',
		remote_url: 'github.com/heracraft/strip-app'
	});
	await setBilling({ mode: 'active', plan: 'pro' });
	await page.goto('/projects');
	const strip = page.getByTestId('plan-strip');
	await expect(
		strip.getByRole('link', { name: /^Memory running, \d+ GB of 32 GB/ })
	).toHaveAttribute('href', '/billing');
	const meter = strip.getByRole('meter', { name: 'Memory running' });
	await expect(meter).toBeVisible();
	await expect(meter).toHaveAttribute('aria-valuetext', /GB of 32 GB/);
});

test("with billing off the strip is one row of the list's facts, with no meter", async ({
	page
}) => {
	await createProject(apiURLFromEnv(), {
		name: 'strip-off-app',
		remote_url: 'github.com/heracraft/strip-off-app'
	});
	await page.setViewportSize({ width: 1280, height: 900 });
	await page.goto('/projects');
	const strip = page.getByTestId('plan-strip');
	await expect(strip.getByRole('link', { name: /^Memory running, \d+ GB$/ })).toBeVisible();
	await expect(strip.getByRole('meter')).toHaveCount(0);
	// Equal cells side by side: every label on one line across the band.
	const labels = ['Memory running', 'Machines running', 'Agents needing input', 'Disk in use'];
	const tops: number[] = [];
	for (const l of labels) {
		const box = await strip.getByText(l, { exact: true }).boundingBox();
		if (!box) throw new Error(`no ${l}`);
		tops.push(Math.round(box.y));
	}
	expect(new Set(tops).size).toBe(1);
});

// An agent asking through repose-ask needs input as much as one whose
// window waits: the strip counts it, as the callout above it does.
test('Agents needing input counts a pending question', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'strip-ask-app',
		remote_url: 'github.com/heracraft/strip-ask-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'Count me?', options: ['yes', 'no'] });
	await page.goto('/projects');
	const cell = page
		.getByTestId('plan-strip')
		.locator('a, div')
		.filter({ has: page.getByText('Agents needing input', { exact: true }) })
		.last();
	await expect(cell).toContainText(p.name);
	await expect(cell).not.toContainText(/needing input\s*0/);
});

// The plan's 32 squares once ran past the Memory cell between 768 and
// about 860, where the cell beside the side column is under 413px.
test('with Pro and 15 projects at 768 the memory squares stay in their cell', async ({ page }) => {
	await createProject(apiURLFromEnv(), {
		name: 'strip-many-app',
		remote_url: 'github.com/heracraft/strip-many-app'
	});
	await setBilling({ mode: 'active', plan: 'pro' });
	await page.route(/\/v1\/projects$/, async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const [first] = (await res.json()) as Record<string, unknown>[];
		const json = Array.from({ length: 15 }, (_, i) => ({
			...first,
			id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
			name: `many-${i + 1}`,
			slug: `many-${i + 1}`,
			class: i < 3 ? 'large' : 'small',
			state: i < 5 ? 'running' : 'stopped'
		}));
		await route.fulfill({ response: res, json });
	});
	await page.setViewportSize({ width: 768, height: 900 });
	await page.goto('/projects');
	const strip = page.getByTestId('plan-strip');
	const meter = strip.getByRole('meter', { name: 'Memory running' });
	await expect(meter).toBeVisible();
	const cell = await strip.getByRole('link', { name: /^Memory running/ }).boundingBox();
	const squares = await meter.boundingBox();
	if (!cell || !squares) throw new Error('no memory cell');
	expect(squares.x + squares.width).toBeLessThanOrEqual(cell.x + cell.width);
});

test('a pending question shows in the callout and on its row, and links to the project', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'asking-app',
		remote_url: 'github.com/heracraft/asking-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'Ship the list page?', options: ['yes', 'no'] });
	await page.goto('/projects');

	const callout = page.getByRole('region', { name: 'Questions waiting' });
	const item = callout.getByRole('listitem').filter({ hasText: 'Ship the list page?' });
	await expect(item).toBeVisible();
	await expect(item).toContainText(p.name);
	await expect(item).toContainText('claude asks');
	await expect(item.getByRole('link', { name: /^Answer/ })).toHaveAttribute(
		'href',
		`/projects/${p.id}#questions`
	);
	await expect(row(page, p.name).getByText('question', { exact: true })).toBeVisible();
});

test("clicking a row's empty area opens the project", async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'row-click-app',
		remote_url: 'github.com/heracraft/row-click-app'
	});
	await page.goto('/projects');
	const r = row(page, p.name);
	await expect(r).toBeVisible();
	// The Agents cell holds no link of its own: the point is the name
	// link's ::after, which covers the row.
	const agents = r.locator('td').nth(3);
	await agents.scrollIntoViewIfNeeded();
	const box = await agents.boundingBox();
	if (!box) throw new Error('no agents cell');
	await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
	await expect(page).toHaveURL(`/projects/${p.id}`);
	await expect(page.getByRole('heading', { level: 1, name: p.name })).toBeVisible();
});

test('Stop on a row stops the project', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'row-stop-app',
		remote_url: 'github.com/heracraft/row-stop-app'
	});
	await page.goto('/projects');
	const r = row(page, p.name);
	await expect(r).toContainText('running');
	await r.hover();
	await r.getByRole('button', { name: `Stop ${p.name}` }).click();
	await expect(r).toContainText('stopped', { timeout: 15_000 });
	await expect(r.getByRole('button', { name: `Start ${p.name}` })).toBeVisible();
	// Still on the list: the button is above the row's link.
	await expect(page).toHaveURL('/projects');
});
