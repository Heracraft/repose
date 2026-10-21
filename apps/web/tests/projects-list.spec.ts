// /projects as the dashboard's home (I-659): the plan strip, the questions
// waiting, rows that open their project from anywhere in them, and Start
// or Stop on a row.
import { test, expect, type Page } from '@playwright/test';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	addQuestion,
	closeQuestion,
	setBilling,
	resetBilling,
	setAgents,
	stopProject,
	destroyProject
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
	await expect(row(page, p.name).getByText('1 question', { exact: true })).toBeVisible();

	await item.getByRole('button', { name: 'yes', exact: true }).click();
	await expect(item).toHaveText('Answered yes from the dashboard');
	const res = await fetch(`${apiURLFromEnv()}/projects/${p.id}/questions`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	const got = ((await res.json()).questions as { id: string }[]).find((x) => x.id === q.id);
	expect(got).toMatchObject({ state: 'answered', answer: 'yes', answered_via: 'dashboard' });
	await expect(row(page, p.name).getByText('1 question', { exact: true })).toHaveCount(0, {
		timeout: 15_000
	});
});

test('a question answered in the terminal says so on /projects, then leaves', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'asked-term-app',
		remote_url: 'github.com/heracraft/asked-term-app'
	});
	const q = await addQuestion(p.id, { agent: 'claude', text: 'Which port?' });
	await page.clock.install();
	await page.goto('/projects');
	const card = page.getByRole('region', { name: 'Waiting on you' });
	await expect(card.getByTestId('question').first()).toBeVisible();
	const all = card.getByRole('button', { name: /^Show all \d+$/ });
	if (await all.isVisible()) await all.click();
	const item = card.locator(`[data-qid="${q.id}"]`);
	await expect(item).toContainText('Which port?');

	await closeQuestion(q.id, 'terminal');
	// The page's poll finds it gone; the card asks the account's full list how.
	await page.clock.runFor(10_500);
	await expect(item).toHaveText('Answered in the terminal');
	await page.clock.runFor(8_500);
	await expect(item).toHaveCount(0);
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
	await expect(card.locator('.hk-badge--warn')).toHaveText('5');
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
// An agent on a row is its name in mono at 13px with a dot (I-837).
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
		return { display: s.display, fontSize: s.fontSize, mono: s.fontFamily.includes('Geist Mono') };
	});
	expect(look).toEqual({ display: 'flex', fontSize: '13px', mono: true });
	await expect(chip.locator('.hk-dot--busy')).toHaveCount(1);
	await setAgents(p.id, []);
});

// The list's order and the toolbar. The api answers GET /projects in
// `order by created_at` (internal/api/store/store.go), so without these
// the machines a user is working on sit wherever they were created —
// with 100 projects to an account (I-569) that is the whole page.

// Every project these tests make is destroyed again: the suite shares one
// account with a 100-project cap (I-569), and a file that leaves fifty
// behind breaks the files that run after it.
const litter: string[] = [];

async function make(
	api: string,
	name: string,
	opts: { stopped?: boolean } = {}
): Promise<{ id: string; name: string }> {
	const p = await createProject(api, { name, remote_url: `github.com/h/${name}` });
	litter.push(p.id);
	if (opts.stopped) await stopProject(api, p.id);
	return p;
}

test.afterEach(async () => {
	const api = apiURLFromEnv();
	while (litter.length) await destroyProject(api, litter.pop()!);
});

/**
 * The project names in the table, in the order they are drawn.
 * allInnerTexts() does not wait for anything, so the first row is waited
 * for first: without that every read here races the first paint.
 */
async function names(page: Page): Promise<string[]> {
	const cells = page.getByRole('region', { name: 'Projects' }).locator('tbody tr .pname');
	await cells.first().waitFor({ state: 'attached' });
	return cells.allInnerTexts();
}

test('the list leads with what is running, whatever order they were made in', async ({ page }) => {
	const api = apiURLFromEnv();
	// Made oldest first; the middle one is the one left running, so
	// created_at order would bury it.
	const first = await make(api, 'ord-a', { stopped: true });
	const middle = await make(api, 'ord-b');
	const last = await make(api, 'ord-c', { stopped: true });

	await page.goto('/projects');
	const shown = await names(page);
	const seen = shown.filter((n) => [first.name, middle.name, last.name].includes(n));
	expect(seen[0]).toBe(middle.name);
});

test('a project an agent is waiting on comes above the rest', async ({ page }) => {
	const api = apiURLFromEnv();
	const busy = await make(api, 'ord-busy');
	const asked = await make(api, 'ord-asked');
	const q = await addQuestion(asked.id, { agent: 'claude', text: 'Run the migration?' });

	await page.goto('/projects');
	const shown = await names(page);
	const iAsked = shown.indexOf(asked.name);
	const iBusy = shown.indexOf(busy.name);
	expect(iAsked).toBeGreaterThanOrEqual(0);
	// The suite shares one account, so earlier tests' projects are in the
	// list too and the cap may hold `busy` back entirely — which is the
	// same claim, more strongly.
	expect(iBusy === -1 || iAsked < iBusy).toBe(true);
	await closeQuestion(q.id);
});

test('the search finds a project by name and by its remote', async ({ page }) => {
	const api = apiURLFromEnv();
	// The toolbar is drawn once the list is long enough to need it.
	const wanted = await createProject(api, {
		name: 'needle-app',
		remote_url: 'github.com/findme/needle-app'
	});
	litter.push(wanted.id);
	for (let i = 0; i < 9; i++) await make(api, `hay-${i}`, { stopped: true });
	await page.goto('/projects');
	const search = page.getByLabel('Search projects');
	await expect(search).toBeVisible();

	await search.fill('needle');
	await expect(page).toHaveURL(/[?&]q=needle/);
	expect(await names(page)).toEqual([wanted.name]);

	// The remote matches too, so an org finds its projects.
	await search.fill('findme');
	await expect(page).toHaveURL(/[?&]q=findme/);
	expect(await names(page)).toEqual([wanted.name]);

	// A reload keeps the list the user was reading.
	await page.reload();
	await expect(page.getByLabel('Search projects')).toHaveValue('findme');
	expect(await names(page)).toEqual([wanted.name]);

	await search.fill('nothing-matches-this');
	await expect(page.getByTestId('no-matches')).toBeVisible();
});

test('the state filter keeps its choice in the URL', async ({ page }) => {
	const api = apiURLFromEnv();
	const up = await make(api, 'filt-up');
	const down = await make(api, 'filt-off', { stopped: true });
	for (let i = 0; i < 8; i++) await make(api, `filt-${i}`, { stopped: true });

	await page.goto('/projects');
	// The suite shares one account, so this test's own projects are
	// narrowed to with the search; the cap would otherwise decide what is
	// on screen.
	await page.getByLabel('Search projects').fill('filt-');
	await expect(page).toHaveURL(/[?&]q=filt-/);
	const filters = page.getByRole('navigation', { name: 'Filter projects' });
	await expect(filters.getByRole('link', { name: 'All' })).toHaveAttribute('aria-current', 'page');

	await filters.getByRole('link', { name: 'Running' }).click();
	await expect(page).toHaveURL(/[?&]state=up/);
	const running = await names(page);
	expect(running).toContain(up.name);
	expect(running).not.toContain(down.name);

	await filters.getByRole('link', { name: 'Stopped' }).click();
	await expect(page).toHaveURL(/[?&]state=off/);
	const stopped = await names(page);
	expect(stopped).toContain(down.name);
	expect(stopped).not.toContain(up.name);
});

test('the list is capped, and "Show all" opens it', async ({ page }) => {
	const api = apiURLFromEnv();
	for (let i = 0; i < 28; i++) await make(api, `cap-${i}`, { stopped: true });
	await page.goto('/projects');
	await page.getByLabel('Search projects').fill('cap-');
	await expect(page).toHaveURL(/[?&]q=cap-/);
	const capped = await names(page);
	expect(capped.length).toBe(25);

	const all = page.getByRole('button', { name: 'Show all 28' });
	await expect(all).toBeVisible();
	await all.click();
	await expect(page.getByRole('button', { name: 'Show fewer' })).toBeVisible();
	expect((await names(page)).length).toBe(28);
	await page.getByRole('button', { name: 'Show fewer' }).click();
	await expect(all).toBeVisible();
	expect((await names(page)).length).toBe(25);
});

test('a stopped row says when it last ran, and shows no agent column filler', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await make(api, 'rest-app', { stopped: true });
	await page.goto('/projects');
	// Narrowed to this test's project: the suite shares one account and
	// the list is capped at 25.
	await page.getByLabel('Search projects').fill('rest-app');
	await expect(page).toHaveURL(/[?&]q=rest-app/);
	const r = row(page, p.name);
	await expect(r).toContainText(/last run|made/);
	// "none" belongs to a machine that is up and running nothing; a
	// stopped one can run nothing, so the word said nothing. (The Last
	// snapshot cell keeps its "none": a stopped machine can have one.)
	await expect(r.locator('td').nth(3)).toHaveText('');
});

// A temporary machine is destroyed with no snapshot (I-347), so its
// countdown is a loss the reader cannot undo: the stacked phone row has
// to carry it, not only the table's state cell.
test('the phone row carries a temporary machine’s countdown', async ({ page }) => {
	const res = await fetch(`${apiURLFromEnv()}/projects`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({
			name: `phone-temp-${Math.random().toString(36).slice(2, 8)}`,
			remote_url: '',
			class: 'small',
			expires_in_s: 7200
		})
	});
	expect(res.ok).toBe(true);
	const p = await res.json();
	litter.push(p.id);

	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/projects');
	const li = page.getByRole('list', { name: 'Projects' }).locator('li').filter({ hasText: p.name });
	await expect(li).toContainText('temporary');
	await expect(li).toContainText(/destroyed in|left/);
});

// Stopping a machine must not slide its row away from the pointer that
// pressed Stop: with the list capped at 25 the row left the page
// entirely. The order is taken on arrival and held while the page is open.
test('a row the user stops keeps its place in the list', async ({ page }) => {
	const api = apiURLFromEnv();
	const up = await make(api, 'hold-up');
	for (let i = 0; i < 6; i++) await make(api, `hold-${i}`, { stopped: true });

	await page.goto('/projects');
	await page.getByLabel('Search projects').fill('hold-');
	await expect(page).toHaveURL(/[?&]q=hold-/);
	const before = await names(page);
	expect(before[0]).toBe(up.name);

	const r = row(page, up.name);
	await r.hover();
	await r.getByRole('button', { name: `Stop ${up.name}` }).click();
	await expect(r).toContainText('stopped', { timeout: 15_000 });
	// Same place, not sorted down among the stopped ones.
	expect((await names(page))[0]).toBe(up.name);
});

// A host going unreachable takes every machine on it, and the list drew
// a green dot and a climbing uptime for a machine that cannot be started.
test('a machine whose host is not answering says so on its row', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await make(api, 'unreachable-app');
	await page.route('**/v1/projects', async (route) => {
		const res = await route.fetch();
		const list = (await res.json()) as { id: string; host_unreachable?: boolean }[];
		for (const q of list) if (q.id === p.id) q.host_unreachable = true;
		await route.fulfill({ response: res, json: list });
	});
	await page.goto('/projects');
	await expect(row(page, p.name)).toContainText('server not answering');
});

// A machine in error was visible only as a red line inside its own row.
test('the lede counts a machine in error', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await make(api, 'lede-error');
	await page.route('**/v1/projects', async (route) => {
		const res = await route.fetch();
		const list = (await res.json()) as { id: string; state: string; last_error?: string }[];
		for (const q of list) {
			if (q.id === p.id) {
				q.state = 'error';
				q.last_error = 'start_failed: the guest did not boot';
			}
		}
		await route.fulfill({ response: res, json: list });
	});
	await page.goto('/projects');
	await expect(page.getByText(/\d+ in error/)).toBeVisible();
});

// Start and Stop are always on the row, not only under the pointer (I-837):
// a phone has no hover, and a keyboard user saw nothing until focus.
test('a row shows its Start or Stop without hovering', async ({ page }) => {
	const p = await make(apiURLFromEnv(), 'shown-act');
	await page.goto('/projects');
	await page.mouse.move(0, 0);
	const button = row(page, p.name).getByRole('button', { name: `Stop ${p.name}` });
	await expect(button).toBeVisible();
	expect(await button.evaluate((el) => getComputedStyle(el).opacity)).toBe('1');
});

// Below 640px each project is a stacked row: the name and its Start or
// Stop on the first line, the action at the frame's right edge; the state
// and the figures on the second.
test('the phone row puts the action beside the name and the state under it', async ({ page }) => {
	const p = await make(apiURLFromEnv(), 'phone-row');
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/projects');
	const li = page.getByRole('list', { name: 'Projects' }).locator('li').filter({ hasText: p.name });
	const name = await li.getByRole('link', { name: p.name, exact: true }).boundingBox();
	const action = await li.getByRole('button', { name: `Stop ${p.name}` }).boundingBox();
	const state = await li.getByText('running', { exact: true }).boundingBox();
	if (!name || !action || !state) throw new Error('no phone row parts');
	// One line: the button's middle within the name's line box.
	expect(Math.abs(action.y + action.height / 2 - (name.y + name.height / 2))).toBeLessThan(6);
	// Flush with the frame's right edge (20px gutter), its touch padding
	// pulled back over the gutter. The frame's own box, since a classic
	// scrollbar's gutter is kept on the page.
	const edge = await page.evaluate(() => {
		const main = document.querySelector('main')!;
		return main.getBoundingClientRect().right - parseFloat(getComputedStyle(main).paddingRight);
	});
	expect(action.x + action.width).toBeGreaterThan(edge + 2);
	expect(action.x + action.width).toBeLessThanOrEqual(edge + 12);
	expect(action.height).toBeGreaterThanOrEqual(44);
	expect(state.y).toBeGreaterThan(name.y + name.height - 2);
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

// The account's payment state is one banner over the list; the full rule
// is on /billing (I-837).
test('past due and suspended each put one banner over the list', async ({ page }) => {
	await setBilling({ mode: 'past_due', plan: 'solo' });
	await page.goto('/projects');
	const due = page.getByTestId('projects-past-due');
	await expect(due).toHaveText('Your last payment failed. Update card');
	await expect(due.getByRole('link', { name: 'Update card' })).toHaveAttribute('href', '/billing');

	await setBilling({ mode: 'suspended', plan: 'solo' });
	await page.reload();
	const off = page.getByRole('alert').filter({ hasText: 'suspended' });
	await expect(off).toHaveText('Your account is suspended. Update card and pay');
	await expect(page.getByTestId('projects-past-due')).toHaveCount(0);
});

// An account with no project yet: the plan to choose first, then one line
// and the three commands that make a project, each with its own Copy.
test('the empty list offers a plan, then the install, login and run commands', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await page.route(/\/v1\/projects$/, (route) =>
		route.request().method() === 'GET' ? route.fulfill({ json: [] }) : route.fallback()
	);
	await page.route(/\/v1\/projects\/destroyed/, (route) => route.fulfill({ json: [] }));
	await page.goto('/projects');
	const plan = page.getByTestId('no-plan');
	await expect(plan).toContainText('Choose a plan before your first machine can start.');
	await expect(plan.getByRole('link', { name: 'Choose a plan' })).toHaveAttribute(
		'href',
		'/billing'
	);
	await expect(page.getByText('No projects yet.')).toBeVisible();
	await expect(page.getByRole('link', { name: /^Quickstart/ })).toHaveAttribute('href', '/docs');
	const commands = page.getByRole('list', { name: 'Commands' });
	await expect(commands.locator('code')).toHaveText([
		/curl -fsSL https:\/\/repose\.herakraft\.co\/install\.sh \| sh$/,
		/repose login$/,
		/repose run$/
	]);
	for (const what of ['the install command', 'repose login', 'repose run']) {
		await expect(commands.getByRole('button', { name: `Copy ${what}` })).toBeVisible();
	}
	await expect(page.getByTestId('plan-strip')).toHaveCount(0);
});
