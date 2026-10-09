// Checklist: "Start, Stop, Destroy, Resize call the right routes and show
// op progress."
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('stop and start round trip', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'lifecycle-app',
		remote_url: 'github.com/heracraft/lifecycle-app'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-state')).toHaveText('running');

	await page.getByRole('button', { name: 'Stop' }).click();
	await expect(page.getByTestId('project-state')).toHaveText('stopped', { timeout: 10_000 });

	await page.getByRole('button', { name: 'Start', exact: true }).click();
	await expect(page.getByTestId('project-state')).toHaveText('running', { timeout: 10_000 });
});

test('resize grows the volume', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'resize-app',
		remote_url: 'github.com/heracraft/resize-app',
		class: 'small'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByText('of 20 GB')).toBeVisible();

	await page.getByRole('button', { name: 'Resize…' }).click();
	await page.getByRole('combobox').selectOption('40');
	await page.getByRole('button', { name: 'Grow' }).click();
	await expect(page.getByText('of 40 GB')).toBeVisible({ timeout: 10_000 });
});

// The select once opened blank on a value it did not offer (20 GB), and
// Grow untouched asked the api to shrink a 40 GB disk to 20. It must open
// on the smallest size larger than the disk and offer only grows.
test('resize opens on the next size up and offers only grows', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'resize-default-app',
		remote_url: 'github.com/heracraft/resize-default-app',
		class: 'small'
	});
	const asked: number[] = [];
	page.on('request', (r) => {
		if (r.method() === 'POST' && r.url().endsWith('/resize')) {
			asked.push((r.postDataJSON() as { volume_bytes: number }).volume_bytes);
		}
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByText('of 20 GB')).toBeVisible();

	await page.getByRole('button', { name: 'Resize…' }).click();
	const select = page.getByLabel('Grow to');
	await expect(select).toHaveValue('40');
	await expect(select.locator('option')).toHaveText(['40 GB', '80 GB', '160 GB', '320 GB']);
	await page.getByRole('button', { name: 'Grow' }).click();
	await expect(page.getByText('of 40 GB')).toBeVisible({ timeout: 10_000 });
	expect(asked).toEqual([40 * 2 ** 30]);
});

test('a disk at the largest size has no Grow to offer', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'resize-max-app',
		remote_url: 'github.com/heracraft/resize-max-app'
	});
	await page.route(`**/v1/projects/${p.id}`, async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const json = await res.json();
		await route.fulfill({ response: res, json: { ...json, volume_bytes: 320 * 2 ** 30 } });
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByText('of 320 GB')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Resize…' })).toHaveCount(0);
	await expect(page.getByText('320 GB is the largest size.')).toBeVisible();
});

// The Disk card's used figure is the guest's root filesystem, and a disk
// 90 percent full or more says so beside Resize (I-567). The allocated
// disk_used_bytes, near full on kanali with 6 GB free, is not shown.
test('a nearly full disk says how full it is', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'disk-full-app',
		remote_url: 'github.com/heracraft/disk-full-app'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByText(/^1 GB of \d+ GB$/)).toBeVisible();
	await expect(page.getByTestId('disk-full')).toHaveCount(0);

	await page.route(`**/v1/projects/${p.id}`, async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const json = await res.json();
		await route.fulfill({
			response: res,
			json: {
				...json,
				volume_bytes: 40 * 2 ** 30,
				disk_used_bytes: 39.5 * 2 ** 30,
				root_used_bytes: 37 * 2 ** 30,
				root_size_bytes: 39 * 2 ** 30
			}
		});
	});
	await page.reload();
	await expect(page.getByText(/^37 GB of 40 GB$/)).toBeVisible();
	await expect(page.getByTestId('disk-full')).toHaveText('94 percent full');
	await expect(page.getByText('39.5 GB')).toHaveCount(0);
});

/** Serves one snapshot for the project, so the list has a row to act on. */
async function oneSnapshot(page: import('@playwright/test').Page, projectId: string) {
	await page.route(`**/v1/projects/${projectId}/snapshots`, (route) =>
		route.request().method() === 'GET'
			? route.fulfill({
					json: [
						{
							id: '0199a1c2-3f40-7b8e-9d21-4c5e6f7a8b90',
							created_at: new Date(Date.now() - 3_600_000).toISOString(),
							bytes: 2 * 2 ** 30,
							reason: 'stop'
						}
					]
				})
			: route.fallback()
	);
}

// Restoring over the disk is as final as Destroy, so it takes the same
// typed confirm instead of the browser's confirm(), and a running project
// has to be stopped first (features/snapshots.md).
test('restoring a snapshot over the disk asks for the slug, like Destroy', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'snap-restore-app',
		remote_url: 'github.com/heracraft/snap-restore-app'
	});
	await oneSnapshot(page, p.id);
	let dialogs = 0;
	page.on('dialog', (d) => {
		dialogs++;
		void d.dismiss();
	});
	let restores = 0;
	page.on('request', (r) => {
		if (r.url().endsWith('/restore')) restores++;
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-state')).toHaveText('running');

	const row = page.getByTestId('snapshot-row');
	await row.getByRole('button', { name: 'Restore…' }).click();
	const panel = row.getByTestId('restore-confirm');
	await expect(panel).toContainText('Anything written since');
	const restore = panel.getByRole('button', { name: 'Restore', exact: true });
	await expect(restore).toBeDisabled();
	await panel.getByLabel(/to confirm/).fill(p.slug);
	await expect(panel).toContainText(`Stop ${p.slug} first`);
	await expect(restore).toBeDisabled();

	await panel.getByRole('button', { name: 'Cancel' }).click();
	await expect(panel).toHaveCount(0);
	// Cancel gives focus back to the button that opened the panel (I-391).
	await expect(row.getByRole('button', { name: 'Restore…' })).toBeFocused();
	expect(dialogs).toBe(0);
	expect(restores).toBe(0);
});

test('restore as new has a labelled name field and fits a phone screen', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	const p = await createProject(apiURLFromEnv(), {
		name: 'snap-new-app',
		remote_url: 'github.com/heracraft/snap-new-app'
	});
	await oneSnapshot(page, p.id);
	await page.goto(`/projects/${p.id}`);

	const row = page.getByTestId('snapshot-row');
	await row.getByRole('button', { name: 'Restore as new…' }).click();
	const name = row.getByLabel('Name for the restored project');
	await expect(name).toBeVisible();
	await expect(row.getByRole('button', { name: 'Restore as new' })).toBeDisabled();
	await name.fill('snap-new-copy');
	await expect(row.getByRole('button', { name: 'Restore as new' })).toBeEnabled();
	const overflow = await page.evaluate(
		() => document.documentElement.scrollWidth - document.documentElement.clientWidth
	);
	expect(overflow).toBeLessThanOrEqual(0);
});

test('destroy requires the exact slug and redirects to the projects list', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'destroy-app',
		remote_url: 'github.com/heracraft/destroy-app'
	});
	await page.goto(`/projects/${p.id}`);

	const destroyBtn = page.getByRole('button', { name: 'Destroy', exact: true });
	await expect(destroyBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill('wrong-slug');
	await expect(destroyBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill(p.slug);
	await expect(destroyBtn).toBeEnabled();
	await destroyBtn.click();

	await expect(page).toHaveURL('/projects', { timeout: 10_000 });
});

// DECISIONS I-167: a destroyed project is listed under "Recently destroyed"
// with its snapshot and expiry, and Restore brings it back as a new
// project under the same name.
test('a destroyed project can be restored from the projects list', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'restore-app',
		remote_url: 'github.com/heracraft/restore-app'
	});
	await page.goto(`/projects/${p.id}`);
	await page.getByLabel(/to confirm/).fill(p.slug);
	await page.getByRole('button', { name: 'Destroy', exact: true }).click();
	await expect(page).toHaveURL('/projects', { timeout: 10_000 });

	const section = page.getByRole('region', { name: 'Recently destroyed' });
	await expect(section).toBeVisible({ timeout: 15_000 });
	const row = section.getByTestId('destroyed-row').filter({ hasText: p.name });
	await expect(row).toContainText('restorable until');
	await expect(row).toContainText('days left');

	await row.getByRole('button', { name: 'Restore…' }).click();
	await expect(row.getByLabel('Name for the restored project')).toHaveValue(p.name);
	await row.getByRole('button', { name: 'Restore', exact: true }).click();

	await expect(page).toHaveURL(/\/projects\/[0-9a-f-]+$/, { timeout: 10_000 });
	await expect(page).not.toHaveURL(`/projects/${p.id}`);
	await expect(page.getByRole('heading', { name: p.name })).toBeVisible();
});

// DECISIONS I-333: the api sends up to 100 destroyed rows at once; the page
// shows the newest 10 and reveals the rest 20 at a time.
test('recently destroyed shows ten rows and reveals the rest on request', async ({ page }) => {
	const now = Date.now();
	const rows = Array.from({ length: 35 }, (_, i) => ({
		id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
		name: `gone-${String(i + 1).padStart(2, '0')}`,
		slug: `gone-${String(i + 1).padStart(2, '0')}`,
		class: 'small',
		volume_bytes: 20 * 2 ** 30,
		destroyed_at: new Date(now - (i + 1) * 3_600_000).toISOString(),
		name_free: true,
		restorable_until: new Date(now + 29 * 86_400_000).toISOString(),
		snapshot: {
			id: `s${i}`,
			created_at: new Date(now - (i + 1) * 3_600_000).toISOString(),
			bytes: 300 * 2 ** 20,
			reason: 'destroy'
		}
	}));
	await page.route('**/v1/projects/destroyed', (route) => route.fulfill({ json: rows }));
	await page.goto('/projects');

	const section = page.getByRole('region', { name: 'Recently destroyed' });
	await expect(section.getByTestId('destroyed-row')).toHaveCount(10);
	await expect(section.getByTestId('destroyed-row').first()).toContainText('gone-01');
	await expect(section.getByText('10 of 35 shown')).toBeVisible();

	await section.getByRole('button', { name: 'Show 20 more' }).click();
	await expect(section.getByTestId('destroyed-row')).toHaveCount(30);
	await section.getByRole('button', { name: 'Show 5 more' }).click();
	await expect(section.getByTestId('destroyed-row')).toHaveCount(35);
	await expect(section.getByRole('button', { name: /Show \d+ more/ })).toHaveCount(0);
});

// DECISIONS I-420: past the api's first 100, Show more asks for the page
// before the oldest row held, until every destroyed project is listed.
test('recently destroyed pages past the first hundred', async ({ page }) => {
	const now = Date.now();
	const rows = Array.from({ length: 130 }, (_, i) => ({
		id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
		name: `old-${String(i + 1).padStart(3, '0')}`,
		slug: `old-${String(i + 1).padStart(3, '0')}`,
		class: 'small',
		volume_bytes: 20 * 2 ** 30,
		destroyed_at: new Date(now - (i + 1) * 3_600_000).toISOString(),
		name_free: true,
		restorable_until: new Date(now + 29 * 86_400_000).toISOString(),
		snapshot: {
			id: `snap-${i}`,
			created_at: new Date(now - (i + 1) * 3_600_000).toISOString(),
			bytes: 2 ** 30,
			reason: 'stop',
			expires_at: new Date(now + 29 * 86_400_000).toISOString()
		}
	}));
	const asked: string[] = [];
	await page.route(/\/v1\/projects\/destroyed(\?.*)?$/, (route) => {
		const u = new URL(route.request().url());
		asked.push(u.search);
		const limit = Number(u.searchParams.get('limit') ?? 100);
		const before = u.searchParams.get('before');
		const from = before ? rows.findIndex((r) => r.id === before) + 1 : 0;
		return route.fulfill({ json: rows.slice(from, from + limit) });
	});
	await page.goto('/projects');
	const section = page.getByRole('region', { name: 'Recently destroyed' });
	const shown = section.getByTestId('destroyed-row');
	await expect(shown).toHaveCount(10);
	await expect(section.getByText('10 shown')).toBeVisible();
	// "Show more" while the api may hold more; "Show N more" once it has
	// sent its last page.
	const more = section.getByRole('button', { name: /^Show (\d+ )?more$/ });
	for (const n of [30, 50, 70, 90, 110, 130]) {
		await more.click();
		await expect(shown).toHaveCount(n);
	}
	await expect(shown.last()).toContainText('old-130');
	await expect(section.getByRole('button', { name: /Show/ })).toHaveCount(0);
	expect(asked.some((q) => q.includes('before=00000000-0000-4000-8000-000000000099'))).toBe(true);
});
