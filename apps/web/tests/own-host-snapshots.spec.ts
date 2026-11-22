// P0 from the Mac review (I-1546): a snapshot of a machine on the
// account's own host is on that host's disk. Removing the host asks with
// the count and deletes them; afterwards Recently destroyed says the
// snapshot went with the host and offers no Restore.
import { test, expect, type Page } from '@playwright/test';
import { signIn, apiURLFromEnv, ownedHost } from './helpers';

const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };

async function shots(page: Page, name: string, region: ReturnType<Page['locator']>) {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await region.evaluate((el) => el.scrollIntoView({ block: 'center' }));
			await page.waitForTimeout(250);
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await page.emulateMedia({ colorScheme: null });
}

test('removing an own host says it deletes its snapshots, and they show as gone', async ({
	page
}) => {
	await ownedHost({ limit: 1, set_install: false });
	await signIn(page);
	const api = apiURLFromEnv();
	const hostID = await ownedHost({ name: 'mac-mini' });
	const suffix = Math.random().toString(36).slice(2, 8);
	const p = await (
		await fetch(`${api}/projects`, {
			method: 'POST',
			headers: auth,
			body: JSON.stringify({ name: `onmac-${suffix}`, class: 'small' })
		})
	).json();
	await ownedHost({ project_id: p.id, host_id: hostID });
	await fetch(`${api}/projects/${p.id}`, { method: 'DELETE', headers: auth });

	await page.goto('/settings/hosts');
	await page.getByRole('button', { name: 'Remove mac-mini…' }).click();
	const confirm = page.getByTestId('confirm-remove-host');
	await expect(confirm).toContainText('This deletes the 1 snapshot kept on it; repose has no copy.');
	await shots(page, 'host-rm-snapshots', confirm);
	await page.getByRole('button', { name: 'Remove mac-mini now' }).click();
	await expect(confirm).toHaveCount(0);

	await page.goto('/projects');
	const row = page.getByTestId('destroyed-row').filter({ hasText: p.name });
	await expect(row).toContainText('gone with host mac-mini');
	await expect(row.getByRole('button', { name: 'Restore…' })).toHaveCount(0);
	await shots(page, 'destroyed-gone-with-host', row);
	await ownedHost({ limit: 1, set_install: false });
});

test('the Snapshots tab says a snapshot went with its host and offers no Restore', async ({
	page
}) => {
	await signIn(page);
	const api = apiURLFromEnv();
	const suffix = Math.random().toString(36).slice(2, 8);
	const p = await (
		await fetch(`${api}/projects`, {
			method: 'POST',
			headers: auth,
			body: JSON.stringify({ name: `snaps-${suffix}`, class: 'small' })
		})
	).json();
	await fetch(`${api}/projects/${p.id}/snapshots`, { method: 'POST', headers: auth });
	await page.route(
		(url) => url.pathname.endsWith(`/v1/projects/${p.id}/snapshots`),
		async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const list = (await res.json()) as object[];
			await route.fulfill({ response: res, json: list.map((s) => ({ ...s, gone_with_host: 'mac-mini' })) });
		}
	);
	await page.goto(`/projects/${p.id}/snapshots`);
	const row = page.getByTestId('snapshot-row').first();
	await expect(row).toContainText('gone with host mac-mini');
	await expect(row.getByRole('button', { name: 'Restore…' })).toHaveCount(0);
	await shots(page, 'snapshots-gone-with-host', row);
});
