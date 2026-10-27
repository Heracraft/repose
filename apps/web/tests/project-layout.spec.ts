// A project is one frame and five tabs (DECISIONS I-841): the layout
// draws the name, the status line, Start or Stop and the tabs once, and
// keeps them mounted while the tabs switch. The Overview leads with what
// waits on you and Connect, then what runs now beside the newest events,
// then Details and Destroy. The snapshots, with their Restore… panel and
// the newest 8 behind "Show all N", are a tab of their own (I-680, I-837).
import { test, expect, type Locator } from '@playwright/test';
import {
	signIn,
	createProject,
	destroyProject,
	apiURLFromEnv,
	addEvents,
	addQuestion,
	setAgents
} from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };

async function snapshot(projectId: string, n: number) {
	for (let i = 0; i < n; i++) {
		const res = await fetch(`${apiURLFromEnv()}/projects/${projectId}/snapshots`, {
			method: 'POST',
			headers: auth,
			body: '{}'
		});
		if (!res.ok) throw new Error(`snapshot: ${res.status} ${await res.text()}`);
	}
}

async function stop(projectId: string) {
	const res = await fetch(`${apiURLFromEnv()}/projects/${projectId}/stop`, {
		method: 'POST',
		headers: auth,
		body: JSON.stringify({ snapshot: false })
	});
	if (!res.ok) throw new Error(`stop: ${res.status} ${await res.text()}`);
}

/** Every box lies within the card's left and right edges. */
async function insideCard(card: Locator, items: Locator[]) {
	const c = await card.boundingBox();
	expect(c).not.toBeNull();
	for (const item of items) {
		const b = await item.boundingBox();
		expect(b).not.toBeNull();
		expect(b!.x).toBeGreaterThanOrEqual(c!.x - 0.5);
		expect(b!.x + b!.width).toBeLessThanOrEqual(c!.x + c!.width + 0.5);
	}
}

async function project(name: string, snapshots: number) {
	const p = await createProject(apiURLFromEnv(), {
		name,
		remote_url: `github.com/heracraft/${name}`
	});
	await snapshot(p.id, snapshots);
	return p;
}

test('the snapshot list shows the newest 8, and Show all the rest', async ({ page }) => {
	const p = await project('many-snaps', 14);
	await page.goto(`/projects/${p.id}/snapshots`);
	const card = page.getByTestId('snapshots-card');
	const rows = card.getByTestId('snapshot-row');
	await expect(rows).toHaveCount(8);
	const toggle = card.getByRole('button', { name: 'Show all 14' });
	await expect(toggle).toHaveAttribute('aria-expanded', 'false');
	await toggle.click();
	await expect(rows).toHaveCount(14);
	await card.getByRole('button', { name: 'Show fewer' }).click();
	await expect(rows).toHaveCount(8);
});

for (const width of [1024, 1440]) {
	test(`Restore… opens inside the snapshots section at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		const p = await project(`restore-room-${width}`, 2);
		// Stopped, so the panel offers both outcomes.
		await stop(p.id);
		await page.goto(`/projects/${p.id}/snapshots`);
		const card = page.getByTestId('snapshots-card');
		await card.getByRole('button', { name: 'Restore…' }).first().click();
		const panel = card.getByTestId('restore-confirm');
		await expect(panel).toBeVisible();
		// Focus moves into the panel, on the typed confirm.
		await expect(panel.getByLabel(/to confirm/)).toBeFocused();
		await insideCard(card, [
			panel.getByLabel(/to confirm/),
			panel.getByRole('button', { name: 'Restore', exact: true }),
			panel.getByLabel('Name for the restored project'),
			panel.getByRole('button', { name: 'Restore as new', exact: true }),
			panel.getByRole('button', { name: 'Cancel' })
		]);
	});
}

test('Restore as new keeps its field and buttons on one line inside the section', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await project('as-new-room', 2);
	await page.goto(`/projects/${p.id}/snapshots`);
	const card = page.getByTestId('snapshots-card');
	// One action a row: Restore… offers both outcomes.
	await expect(card.getByRole('button', { name: /Restore as new/ })).toHaveCount(0);
	await card.getByRole('button', { name: 'Restore…' }).first().click();
	const field = card.getByLabel('Name for the restored project');
	const submit = card.getByRole('button', { name: 'Restore as new', exact: true });
	const cancel = card.getByRole('button', { name: 'Cancel' });
	await insideCard(card, [field, submit, cancel]);
	// One line: the field and both buttons share a row.
	const [f, s, c] = await Promise.all([field, submit, cancel].map((l) => l.boundingBox()));
	expect(Math.abs(f!.y + f!.height / 2 - (s!.y + s!.height / 2))).toBeLessThan(4);
	expect(Math.abs(s!.y + s!.height / 2 - (c!.y + c!.height / 2))).toBeLessThan(4);
});

test('a stopped project has no Agents card', async ({ page }) => {
	const p = await project('stopped-layout', 1);
	await stop(p.id);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-state')).toContainText('stopped');
	await expect(page.getByTestId('details-card')).toBeVisible();
	await expect(page.getByTestId('agents-card')).toHaveCount(0);
});

// The current tab's 1px ink edge lies on the title rule: the scrolling
// tab row keeps it inside its box, so it is drawn and the row has no
// vertical scroll (spec section 2, Page tabs).
test("the current tab's edge lies on the rule", async ({ page }) => {
	const p = await project('tabs-edge', 0);
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto(`/projects/${p.id}`);
		const m = await page.locator('.hk-tabs').evaluate((nav) => {
			const a = nav.querySelector('[aria-current="page"]')!.getBoundingClientRect();
			const rule = nav.parentElement!.getBoundingClientRect();
			return {
				noScroll: nav.scrollHeight === nav.clientHeight,
				onRule: Math.abs(a.bottom - rule.bottom) < 0.5
			};
		});
		expect(m, `at ${width}`).toEqual({ noScroll: true, onRule: true });
	}
});

test('a project in error offers Restart', async ({ page }) => {
	const p = await project('error-layout', 0);
	await page.route(`**/v1/projects/${p.id}`, async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const json = await res.json();
		await route.fulfill({
			response: res,
			json: { ...json, state: 'error', last_error: 'boot_failed: the machine did not boot.' }
		});
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('last-error')).toContainText('The machine did not boot.');
	await expect(page.getByTestId('last-error').getByRole('link', { name: 'Errors' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Restart' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Start', exact: true })).toHaveCount(0);
});

test('no section is more than 40px taller than its content at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await project('snug-cards', 6);
	await addEvents(p.id, 12);
	for (const [path, rows, n] of [
		['', 'event-row', 5],
		['/snapshots', 'snapshot-row', 6],
		['/events', 'event-row', 10]
	] as const) {
		await page.goto(`/projects/${p.id}${path}`);
		await expect(page.getByTestId(rows)).toHaveCount(n);
		const empty = await page.evaluate(() => {
			const vis = (el: Element) => {
				const s = getComputedStyle(el);
				return (
					s.display !== 'none' && s.visibility !== 'hidden' && el.getBoundingClientRect().height > 0
				);
			};
			const out: string[] = [];
			for (const c of document.querySelectorAll('main section')) {
				if (!vis(c)) continue;
				const rc = c.getBoundingClientRect();
				const pb = parseFloat(getComputedStyle(c).paddingBottom) || 0;
				let bottom = rc.top;
				c.querySelectorAll('*').forEach((k) => {
					if (!vis(k) || k.children.length) return;
					const kr = k.getBoundingClientRect();
					if (kr.height && kr.bottom > bottom) bottom = kr.bottom;
				});
				const gap = rc.bottom - pb - bottom;
				if (gap > 40)
					out.push(`${c.querySelector('h2')?.textContent?.trim()}: ${Math.round(gap)}px`);
			}
			return out;
		});
		expect(empty, path || 'Overview').toEqual([]);
	}
});

/** Each named section's box, by its level-2 heading. */
async function boxes(page: import('@playwright/test').Page, names: string[]) {
	const out: Record<string, { x: number; y: number; w: number }> = {};
	for (const name of names) {
		const b = await page
			.locator('main section')
			.filter({ has: page.getByRole('heading', { level: 2, name, exact: true }) })
			.first()
			.boundingBox();
		if (!b) throw new Error(`no section ${name}`);
		out[name] = { x: Math.round(b.x), y: Math.round(b.y), w: Math.round(b.width) };
	}
	return out;
}

/** The Overview's sections in the DOM's order, which is the phone's: state before history. */
const ORDER = [
	'Waiting on you',
	'Connect',
	'Agents',
	'Machine',
	'Latest events',
	'Details',
	'Destroy'
];

async function busy(name: string) {
	const p = await project(name, 1);
	await setAgents(p.id, [{ agent: 'claude', window: 'claude', state: 'idle' }]);
	await addQuestion(p.id, { agent: 'claude', text: 'Ship it?', options: ['yes', 'no'] });
	return p;
}

// From a 760px column (1440: 872px) the newest events sit on the left and
// Agents and Machine in an 18rem column on the right; the rest spans it.
test('the Overview sits in two columns at 1440, questions and Connect first', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await busy('order-wide');
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('agents-card')).toBeVisible();
	await expect(page.getByTestId('questions')).toBeVisible();
	const b = await boxes(page, ORDER);
	const left = b.Connect.x;
	const near = (a: number, c: number) => expect(Math.abs(a - c)).toBeLessThanOrEqual(1);
	expect(b.Connect.w).toBeGreaterThan(850);
	for (const n of ['Waiting on you', 'Latest events', 'Details', 'Destroy']) near(b[n].x, left);
	for (const n of ['Agents', 'Machine']) {
		near(b[n].w, 288);
		near(b[n].x, left + b['Latest events'].w + 40);
	}
	expect(b['Waiting on you'].y).toBeLessThan(b.Connect.y);
	expect(b['Latest events'].y).toBe(b.Agents.y);
	expect(b.Machine.y).toBeGreaterThan(b.Agents.y);
	expect(b.Details.y).toBeGreaterThan(b.Machine.y);
	expect(b.Destroy.y).toBeGreaterThan(b.Details.y);
	expect(b.Details.w).toBe(b.Connect.w);
});

// Below a 760px column (1024: 700px, and every phone) one column, in the
// DOM order.
for (const width of [390, 1024]) {
	test(`the Overview stacks in one column at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		const p = await busy(`order-${width}`);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('agents-card')).toBeVisible();
		await expect(page.getByTestId('questions')).toBeVisible();
		const b = await boxes(page, ORDER);
		const ys = ORDER.map((n) => b[n].y);
		expect([...ys].sort((m, n) => m - n)).toEqual(ys);
		for (const n of ORDER) expect(Math.abs(b[n].x - b.Connect.x)).toBeLessThanOrEqual(1);
	});
}

test('Latest events shows the newest 5 and links to the Events tab', async ({ page }) => {
	const p = await project('latest-five', 0);
	await addEvents(p.id, 12);
	await page.goto(`/projects/${p.id}`);
	const latest = page.getByTestId('latest-events');
	await expect(latest.getByTestId('event-row')).toHaveCount(5);
	await expect(latest.getByTestId('event-row').first()).toContainText('e011');
	await latest.getByRole('link', { name: 'All events' }).click();
	await expect(page).toHaveURL(`/projects/${p.id}/events`);
	await expect(page.getByTestId('events-card').getByTestId('event-row')).toHaveCount(10);
});

// Destroy no longer carries an open field on every visit: its button
// names the project and opens the typed confirm, and Keep it closes it.
test('Destroy opens its typed confirm and Keep it gives focus back', async ({ page }) => {
	const p = await project('destroy-open', 0);
	await page.goto(`/projects/${p.id}`);
	const section = page.getByTestId('destroy-card');
	await expect(section.getByLabel(/to confirm/)).toHaveCount(0);
	const open = section.getByRole('button', { name: `Destroy ${p.slug}…` });
	await open.click();
	await expect(section.getByLabel(/to confirm/)).toBeFocused();
	await section.getByRole('button', { name: 'Keep it' }).click();
	await expect(section.getByLabel(/to confirm/)).toHaveCount(0);
	await expect(open).toBeFocused();
});

// On a phone Start or Stop is a row of its own under the status line,
// above the tabs, in every state.
test('on a phone the header action sits under the status line, above the tabs', async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	const p = await project('phone-head', 0);
	for (const [state, name] of [
		['running', 'Stop'],
		['stopped', 'Start']
	] as const) {
		if (state === 'stopped') await stop(p.id);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText(state);
		const action = await page.getByRole('button', { name, exact: true }).boundingBox();
		const status = await page.getByTestId('project-state').boundingBox();
		const tabs = await page.getByRole('navigation', { name: 'Project' }).boundingBox();
		expect(action!.y).toBeGreaterThan(status!.y + status!.height);
		expect(action!.y + action!.height).toBeLessThanOrEqual(tabs!.y);
	}
});

const TABS = ['Overview', 'Events', 'Usage', 'Snapshots', 'Config'];

// One frame for the five tabs: the strip does not move under the
// pointer, the state shows on each, and Stop is the same element after
// every switch (the header and an op under way stay mounted).
test('the tabs switch under one header that stays mounted', async ({ page }) => {
	const p = await project('tabs-y', 0);
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText('running');
		const stopButton = await page
			.getByRole('button', { name: 'Stop', exact: true })
			.elementHandle();
		const nav = page.getByRole('navigation', { name: 'Project' });
		const ys: number[] = [];
		for (const tab of TABS) {
			await nav.getByRole('link', { name: tab, exact: true }).click();
			await expect(nav.getByRole('link', { name: tab, exact: true })).toHaveAttribute(
				'aria-current',
				'page'
			);
			await expect(page.getByRole('heading', { level: 1, name: p.name })).toBeVisible();
			await expect(page.getByTestId('project-state')).toHaveText('running');
			ys.push((await nav.boundingBox())!.y);
			expect(await stopButton!.evaluate((el) => el.isConnected), `${tab} at ${width}`).toBe(true);
		}
		expect(Math.max(...ys) - Math.min(...ys), `at ${width}: ${ys.join(', ')}`).toBeLessThan(0.5);
	}
	await destroyProject(apiURLFromEnv(), p.id);
});

for (const width of [360, 390]) {
	test(`all five tabs fit at ${width} without sideways scroll`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		const p = await project(`tabs-fit-${width}`, 0);
		await page.goto(`/projects/${p.id}/config`);
		const nav = page.getByRole('navigation', { name: 'Project' });
		await expect(nav.getByRole('link')).toHaveText(TABS);
		expect(await nav.evaluate((n) => n.scrollWidth <= n.clientWidth)).toBe(true);
		for (const tab of TABS) await expect(nav.getByRole('link', { name: tab })).toBeInViewport();
	});
}

// Moving from one project to another (a rail row) starts the other's
// frame: its name, its tabs and its poll, not the first one's.
test('the rail moves to another project, tab and all', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const a = await project('rail-from', 0);
	const b = await project('rail-to', 0);
	await page.goto(`/projects/${a.id}/usage`);
	await expect(page.getByRole('heading', { level: 1, name: a.name })).toBeVisible();
	// A link inside the app, as the rail's row is (the rail lists only 8,
	// and this suite's account holds many more).
	await page.evaluate((href) => {
		const a = document.createElement('a');
		a.href = href;
		a.textContent = 'to b';
		a.id = 'to-b';
		document.querySelector('main')!.prepend(a);
	}, `/projects/${b.id}`);
	await page.locator('#to-b').click();
	await expect(page).toHaveURL(`/projects/${b.id}`);
	await expect(page.getByRole('heading', { level: 1, name: b.name })).toBeVisible();
	await expect(
		page.getByRole('navigation', { name: 'Project' }).getByRole('link', { name: 'Overview' })
	).toHaveAttribute('aria-current', 'page');
	await expect(page).toHaveTitle(new RegExp(`(^|\\) )${b.name} · repose$`));
});

// Machine shows one disk size: the meter's. The volume behind it is read
// out, not drawn beside a second figure.
test('Machine draws one disk size', async ({ page }) => {
	const p = await project('one-disk-size', 0);
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('machine-card');
	await expect(card.getByRole('meter', { name: 'Disk' })).toBeVisible();
	await expect(card.getByTestId('disk-volume')).toHaveText(/^\d+ GB disk$/);
	const volume = await card.getByTestId('disk-volume').boundingBox();
	expect(volume!.width).toBeLessThanOrEqual(1);
});
