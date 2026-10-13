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
	resetBilling,
	setAgents
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

test('a pending question shows in Waiting on you and on its row, and is answered in place', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'asking-app',
		remote_url: 'github.com/heracraft/asking-app'
	});
	const q = await addQuestion(p.id, {
		agent: 'claude',
		text: 'Ship the list page?',
		options: ['yes', 'no']
	});
	await page.goto('/projects');

	const card = page.getByRole('region', { name: 'Waiting on you' });
	await expect(card).toHaveAttribute('data-testid', 'pending-questions');
	// Other files' questions wait in the same account; past 3 rows the
	// rest are behind Show all.
	await expect(card.getByTestId('question').first()).toBeVisible();
	const all = card.getByRole('button', { name: /^Show all \d+$/ });
	if (await all.isVisible()) await all.click();
	const item = card.locator(`[data-qid="${q.id}"]`);
	await expect(item).toContainText('Ship the list page?');
	await expect(item).toBeVisible();
	await expect(item).toContainText('claude');
	// The project's name links to it, in the row or over its group.
	await expect(card.getByRole('link', { name: p.name, exact: true }).first()).toHaveAttribute(
		'href',
		`/projects/${p.id}`
	);
	await expect(row(page, p.name).getByText('question', { exact: true })).toBeVisible();

	await item.getByRole('button', { name: 'yes', exact: true }).click();
	await expect(item).toHaveText('Answered yes from the dashboard');
	const res = await fetch(`${apiURLFromEnv()}/projects/${p.id}/questions`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	const got = ((await res.json()).questions as { id: string }[]).find((x) => x.id === q.id);
	expect(got).toMatchObject({ state: 'answered', answer: 'yes', answered_via: 'dashboard' });
	await expect(row(page, p.name).getByText('question', { exact: true })).toHaveCount(0, {
		timeout: 15_000
	});
});

test('Waiting on you groups by project and shows 3 rows before Show all', async ({ page }) => {
	const now = Date.now();
	const mk = (
		i: number,
		project: string,
		agent: string,
		minutes: number,
		options: string[] = []
	) => ({
		id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
		project_id: `00000000-0000-4000-9000-${project === 'alpha' ? '000000000001' : '000000000002'}`,
		project,
		agent,
		window: agent,
		text: `Question ${i} from ${agent}?`,
		options,
		state: 'pending',
		answer: null,
		answered_via: null,
		created_at: new Date(now - minutes * 60_000).toISOString(),
		expires_at: new Date(now + 3600_000).toISOString(),
		answered_at: null
	});
	const questions = [
		mk(1, 'alpha', 'claude', 3, ['yes', 'no']),
		mk(2, 'alpha', 'codex', 20),
		mk(3, 'beta', 'pi', 50),
		mk(4, 'beta', 'opencode', 5),
		mk(5, 'alpha', 'gemini', 1)
	];
	await page.route(/\/questions\?state=pending$/, (route) =>
		route.fulfill({ json: { questions } })
	);
	await page.goto('/projects');
	const card = page.getByTestId('pending-questions');
	await expect(card.getByRole('heading', { name: 'Waiting on you' })).toBeVisible();
	await expect(card.locator('.badge--warn')).toHaveText('5');
	// beta has waited longest, so it leads; its newest row first.
	await expect(card.getByRole('link')).toHaveText(['beta', 'alpha']);
	await expect(card.getByTestId('question')).toHaveCount(3);
	await expect(card.getByTestId('question').nth(0)).toContainText('Question 4 from opencode?');
	await card.getByRole('button', { name: 'Show all 5' }).click();
	await expect(card.getByTestId('question')).toHaveCount(5);
	await card.getByRole('button', { name: 'Show fewer' }).click();
	await expect(card.getByTestId('question')).toHaveCount(3);
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

// The landing's stylesheet stays loaded after a client-side move to the
// dashboard, so a generic class there restyled the dashboard: its `.chip`
// turned the agent chip into a blue mono command chip with no gap
// (owner's screenshot, 2026-10-12). The landing's names are its own now.
test('an agent chip keeps its look after coming from the landing', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'chip-app',
		remote_url: 'github.com/heracraft/chip-app'
	});
	await setAgents(p.id, [{ agent: 'claude', window: '1', state: 'working' }]);
	await page.getByRole('link', { name: 'repose, home' }).click();
	await page.waitForURL(/\/$/);
	await page.getByRole('link', { name: 'Dashboard' }).first().click();
	await page.waitForURL(/\/projects$/);
	const chip = row(page, p.name).locator('.chip').first();
	await expect(chip).toBeVisible();
	const look = await chip.evaluate((el) => {
		const s = getComputedStyle(el);
		return { display: s.display, fontSize: s.fontSize };
	});
	expect(look).toEqual({ display: 'flex', fontSize: '11px' });
	await setAgents(p.id, []);
});
