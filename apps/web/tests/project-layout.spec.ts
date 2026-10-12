// The project page's cards are as tall as their content, its growing lists
// are capped, and its forms open with room: the snapshot list holds the
// newest 8 behind "Show all N", Restore… and Restore as new… stay inside
// the snapshots card, Agents shows only while the machine runs, and a
// machine in error offers Restart (DECISIONS I-680, DESIGN-LANGUAGE.md
// "Card layout").
import { test, expect, type Locator } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addEvents } from './helpers';

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
	test(`Restore… opens inside the snapshots card at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		const p = await project(`restore-room-${width}`, 2);
		await page.goto(`/projects/${p.id}`);
		const card = page.getByTestId('snapshots-card');
		await card.getByRole('button', { name: 'Restore…' }).first().click();
		const panel = card.getByTestId('restore-confirm');
		await expect(panel).toBeVisible();
		await insideCard(card, [
			panel.getByRole('textbox'),
			panel.getByRole('button', { name: 'Restore', exact: true }),
			panel.getByRole('button', { name: 'Cancel' })
		]);
	});
}

test('Restore as new… keeps its field and buttons inside the card', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await project('as-new-room', 2);
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('snapshots-card');
	await card.getByRole('button', { name: 'Restore as new…' }).first().click();
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

test('no card is more than 40px taller than its content at 1440', async ({ page }) => {
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
		for (const c of document.querySelectorAll('main .card, main section')) {
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
