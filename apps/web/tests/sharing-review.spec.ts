// What the customer review of sharing on owned hosts found missing on
// the dashboard (DECISIONS I-1652, I-1654): a machine that stopped
// answering read `running` with a Stop, and one destroyed off a host of
// the account's own still showed a size.
import { test, expect, type Page } from '@playwright/test';
import {
	addEvents,
	signIn,
	createProject,
	apiURLFromEnv,
	destroyProject,
	ownedHost,
	removeOwnedHosts
} from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

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

/** The fake's guestd silence (cmd/fakeapi POST /not-answering, I-1652). */
async function setNotAnswering(projectID: string, minutes: number): Promise<void> {
	const res = await fetch(`${process.env.FAKEAPI_ADMIN_URL}/not-answering`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ project_id: projectID, minutes })
	});
	if (!res.ok) throw new Error(`/not-answering: ${res.status}`);
}

test('a machine that stopped answering says so, with Restart as the way back', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'hung-app',
		remote_url: 'github.com/heracraft/hung-app'
	});
	try {
		await setNotAnswering(p.id, 4);
		await page.goto('/projects');
		const row = page
			.locator('table.ptable tbody tr')
			.filter({ has: page.getByRole('link', { name: p.name, exact: true }) });
		await expect(row.getByTestId('row-not-answering')).toHaveText('Not answering for 4m');
		await expect(row).toContainText('running, not answering');
		// The rail's dot is red, and its name says so (pass2-dash N5).
		await expect(
			page.getByRole('link', { name: `${p.name}, running, not answering` })
		).toBeVisible();
		await expect(row.getByRole('button', { name: `Restart ${p.name}` })).toBeVisible();
		await shots(page, 'not-answering-list');

		await page.goto(`/projects/${p.id}`);
		const banner = page.getByTestId('not-answering');
		await expect(banner).toContainText(
			'Not answering for 4m. Restart starts it again; what was in its memory is lost.'
		);
		await shots(page, 'not-answering-page');
		// The header says so too, and offers Restart in place of Stop (I-1658).
		await expect(page.getByTestId('project-state')).toContainText('running, not answering');
		await expect(page.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0);
		await page.getByRole('button', { name: 'Restart', exact: true }).first().click();
		await expect(banner).toHaveCount(0);
	} finally {
		await destroyProject(api, p.id);
	}
});

test('Recently destroyed names no size for a machine from a host of your own', async ({ page }) => {
	const api = apiURLFromEnv();
	await removeOwnedHosts(api);
	await ownedHost({ limit: -1 });
	await ownedHost({ name: 'mac-mini' });
	try {
		const p = await createProject(api, {
			name: 'gone-own',
			remote_url: 'github.com/heracraft/gone-own'
		});
		await destroyProject(api, p.id);
		await page.goto('/projects');
		const row = page.getByTestId('destroyed-row').filter({ hasText: 'gone-own' }).first();
		await expect(row.getByTestId('destroyed-size')).toHaveText('-');
		await expect(row).not.toContainText('large');
	} finally {
		await removeOwnedHosts(api).catch(() => {});
	}
});

// pass2-dash N6: the memory stop reads as the CLI says it, amber, not
// "failed" in red; and repose's own sentence draws its command as code,
// not in backticks (N5).
test('a memory stop reads as the CLI says it, and its command is code', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'oom-app',
		remote_url: 'github.com/heracraft/oom-app'
	});
	try {
		await addEvents(p.id, 1, {
			kinds: ['oom_stopped'],
			summaries: [
				'stopped: your host mac-mini ran out of memory, so repose stopped oom-app, the machine started last, to keep the others running. `repose start oom-app` starts it again'
			]
		});
		await page.goto(`/projects/${p.id}/events`);
		const row = page
			.getByTestId('events-card')
			.locator('li')
			.filter({ hasText: 'ran out of memory' })
			.first();
		await expect(row).toContainText('stopped, host out of memory');
		await expect(row).not.toContainText('failed');
		await expect(row).not.toContainText('`');
		await expect(row.locator('code')).toHaveText('repose start oom-app');
	} finally {
		await destroyProject(api, p.id);
	}
});
