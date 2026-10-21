// The project page's sections are as tall as their content, its growing
// lists are capped, and its forms open with room: the snapshot list holds
// the newest 8 behind "Show all N", Restore… opens one panel inside the
// snapshots section with both outcomes, Agents shows only while the
// machine runs, a machine in error offers Restart, and the sections come
// in the order a visit needs them (DECISIONS I-680, I-837).
import { test, expect, type Locator } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addEvents, setAgents } from './helpers';

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
	await page.goto(`/projects/${p.id}`);
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
		await page.goto(`/projects/${p.id}`);
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
	await page.goto(`/projects/${p.id}`);
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
	await expect(page.getByTestId('last-error')).toHaveText('the machine did not boot.');
	await expect(page.getByRole('button', { name: 'Restart' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Start', exact: true })).toHaveCount(0);
});

test('no section is more than 40px taller than its content at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await project('snug-cards', 6);
	await addEvents(p.id, 12);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('snapshot-row')).toHaveCount(6);
	await expect(page.getByTestId('event-row')).toHaveCount(10);
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
			if (gap > 40) out.push(`${c.querySelector('h2')?.textContent?.trim()}: ${Math.round(gap)}px`);
		}
		return out;
	});
	expect(empty).toEqual([]);
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

const ORDER = [
	'Connect',
	'Events',
	'Agents',
	'Machine',
	'Snapshots',
	'Details',
	'Usage',
	'Destroy'
];

// I-837: the page leads with getting in. At 1440 Connect spans the frame,
// then Events beside Agents and Machine, Snapshots beside Details, then
// Usage and Destroy across the frame.
test('the sections sit in two columns at 1440, Connect first', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await project('order-wide', 2);
	await setAgents(p.id, [{ agent: 'claude', window: 'claude', state: 'working' }]);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('agents-card')).toBeVisible();
	const b = await boxes(page, ORDER);
	const left = b.Connect.x;
	expect(b.Connect.w).toBeGreaterThan(1000);
	const near = (a: number, b: number) => expect(Math.abs(a - b)).toBeLessThanOrEqual(1);
	for (const n of ['Events', 'Snapshots', 'Usage', 'Destroy']) near(b[n].x, left);
	// The side column: 21rem, 3rem past the wide one.
	for (const n of ['Agents', 'Machine', 'Details']) {
		near(b[n].w, 336);
		near(b[n].x, left + b.Events.w + 48);
	}
	expect(b.Events.y).toBe(b.Agents.y);
	expect(b.Machine.y).toBeGreaterThan(b.Agents.y);
	expect(b.Snapshots.y).toBe(b.Details.y);
	expect(b.Connect.y).toBeLessThan(b.Events.y);
	expect(b.Usage.y).toBeGreaterThan(b.Snapshots.y);
	expect(b.Destroy.y).toBeGreaterThan(b.Usage.y);
	expect(b.Usage.w).toBe(b.Connect.w);
});

// Below 1024px one column, in the DOM order, which is the phone's.
for (const width of [390, 768]) {
	test(`the sections stack in one column at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		const p = await project(`order-${width}`, 1);
		await setAgents(p.id, [{ agent: 'claude', window: 'claude', state: 'idle' }]);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('agents-card')).toBeVisible();
		const b = await boxes(page, ORDER);
		const ys = ORDER.map((n) => b[n].y);
		expect([...ys].sort((m, n) => m - n)).toEqual(ys);
		for (const n of ORDER) expect(Math.abs(b[n].x - b.Connect.x)).toBeLessThanOrEqual(1);
	});
}

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

// On a phone the header's action is one row under the status line in
// every state, so the remote and the state stay under the title.
test('on a phone the header action sits under the status line', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	const p = await project('phone-head', 0);
	for (const [state, name] of [
		['running', 'Stop'],
		['stopped', 'Start']
	] as const) {
		if (state === 'stopped') await stop(p.id);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText(state);
		const status = await page.getByTestId('project-state').boundingBox();
		const action = await page.getByRole('button', { name, exact: true }).boundingBox();
		const tabs = await page.getByRole('navigation', { name: 'Project' }).boundingBox();
		expect(action!.y).toBeGreaterThan(status!.y + status!.height);
		expect(action!.y + action!.height).toBeLessThan(tabs!.y);
	}
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
