// The Ubuntu base on the dashboard (DECISIONS I-1462, I-1490): an erofs
// project's Config page is the base, an earlier menu's entries read-only
// (the menu is gone, I-1698) and, only while one waits, the next base
// with Restart; the Nix shape is unchanged; the settings
// nav lists machine.nix only for an account with a Nix project; the add
// host dialog shows the x86 installer beside the Mac's step.
import { test, expect, type Page, type Locator } from '@playwright/test';
import { signIn, apiURLFromEnv, pruneProjects, ownedHost } from './helpers';

const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };

/** Captures the viewport at 1x, at 1440 and 390, light and dark (CLAUDE.md "Judge visuals at real size"). */
async function shots(page: Page, name: string, region?: Locator): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			if (region) await region.evaluate((el) => el.scrollIntoView({ block: 'center' }));
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await page.emulateMedia({ colorScheme: null });
}

async function makeProject(
	name: string,
	runtime: 'nix' | 'erofs'
): Promise<{ id: string; slug: string }> {
	const suffix = Math.random().toString(36).slice(2, 8);
	const res = await fetch(`${apiURLFromEnv()}/projects`, {
		method: 'POST',
		headers: auth,
		body: JSON.stringify({ name: `${name}-${suffix}`, class: 'large', guest_runtime: runtime })
	});
	if (!res.ok) throw new Error(`create: ${res.status} ${await res.text()}`);
	return res.json();
}

/** Serves the project with pending_base_version set, as the base bump would. */
async function withPendingBase(page: Page, id: string, version: string): Promise<void> {
	await page.route(
		(url) => url.pathname.endsWith(`/v1/projects/${id}`),
		async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const body = await res.json();
			await route.fulfill({ response: res, json: { ...body, pending_base_version: version } });
		}
	);
}

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test.afterAll(async () => {
	await pruneProjects(apiURLFromEnv());
});

test('an erofs project shows its base and no menu, editor or revisions', async ({ page }) => {
	const p = await makeProject('ubuntu-app', 'erofs');
	await page.goto(`/projects/${p.id}/config`);
	const facts = page.getByTestId('config-facts');
	await expect(facts).toContainText('Base');
	await expect(page.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
	await expect(page.getByRole('searchbox')).toHaveCount(0);
	await expect(page.getByRole('checkbox', { name: /PostgreSQL/ })).toHaveCount(0);
	await expect(page.getByRole('tablist', { name: 'Config editor' })).toHaveCount(0);
	await expect(page.getByText('Use your machine.nix on this machine')).toHaveCount(0);
	await expect(page.locator('.cm-content')).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Revisions' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Apply', exact: true })).toHaveCount(0);
	await expect(page.getByTestId('old-menu')).toHaveCount(0);
	await expect(page.getByTestId('pending-base')).toHaveCount(0);
	await shots(page, 'config-erofs');
});

// A project whose menu has entries from before I-1698 keeps them, and the
// page lists them in one read-only line.
test("an erofs project lists an earlier menu's entries, read-only", async ({ page }) => {
	const p = await makeProject('ubuntu-oldmenu', 'erofs');
	const put = await fetch(`${apiURLFromEnv()}/projects/${p.id}/config`, {
		method: 'PUT',
		headers: auth,
		body: JSON.stringify({
			menu: [{ id: 'postgresql', options: { version: '16' } }, { id: 'redis' }, { package: 'ffmpeg' }]
		})
	});
	expect(put.status).toBe(202);
	await page.goto(`/projects/${p.id}/config`);
	const line = page.getByTestId('old-menu');
	await expect(line).toHaveText(
		'Installed again after every base update, from the menu: postgresql 16, redis, ffmpeg',
		{ timeout: 10_000 }
	);
	await expect(page.getByRole('button', { name: 'Apply', exact: true })).toHaveCount(0);
	await expect(page.getByRole('checkbox', { name: /PostgreSQL/ })).toHaveCount(0);
	await shots(page, 'config-erofs-oldmenu');
});
test('a waiting base shows one line and Restart, and only then', async ({ page }) => {
	const p = await makeProject('ubuntu-pending', 'erofs');
	await withPendingBase(page, p.id, '2026.11.30');
	await page.goto(`/projects/${p.id}/config`);
	const banner = page.getByTestId('pending-base');
	await expect(banner.locator('p')).toHaveText(
		'Base 2026.11.30 is ready. It is used at the next restart.'
	);
	await expect(banner.getByRole('button')).toHaveText('Restart');
	await shots(page, 'config-erofs-pending');

	// A running machine asks first: its processes end.
	await banner.getByRole('button', { name: 'Restart' }).click();
	await expect(banner).toContainText('Its processes end');
	await shots(page, 'config-erofs-restart-ask');
	await banner.getByRole('button', { name: 'Not now' }).click();
	await expect(banner).toContainText('is ready');

	await banner.getByRole('button', { name: 'Restart' }).click();
	const stop = page.waitForRequest(
		(r) => r.method() === 'POST' && r.url().endsWith(`/projects/${p.id}/stop`)
	);
	const start = page.waitForRequest(
		(r) => r.method() === 'POST' && r.url().endsWith(`/projects/${p.id}/start`)
	);
	await banner.getByRole('button', { name: 'Restart now' }).click();
	expect(new URL((await stop).url()).pathname).toContain(p.id);
	await start;
});

test('a Nix project keeps its editor and machine.nix, and never shows the base line', async ({
	page
}) => {
	const p = await makeProject('nix-app', 'nix');
	// The api never sets it on a Nix project; even if it did, nothing shows.
	await withPendingBase(page, p.id, '2026.11.30');
	await page.goto(`/projects/${p.id}/config`);
	await expect(page.locator('.cm-content')).toBeVisible();
	await expect(page.getByRole('tablist', { name: 'Config editor' })).toHaveCount(0);
	await expect(page.getByText('Use your machine.nix on this machine')).toBeVisible();
	await expect(page.getByTestId('pending-base')).toHaveCount(0);
	await shots(page, 'config-nix');
});
test('settings lists machine.nix only for an account with a Nix project', async ({ page }) => {
	await pruneProjects(apiURLFromEnv());
	await makeProject('only-ubuntu', 'erofs');
	await page.goto('/settings');
	const nav = page.getByRole('navigation', { name: 'Settings' });
	await expect(nav.getByRole('link', { name: 'Hosts' })).toBeVisible();
	await expect(nav.getByRole('link', { name: 'machine.nix' })).toHaveCount(0);
	await shots(page, 'settings-nav-erofs');

	// A direct link still opens it, with its own tab.
	await page.goto('/settings/machine-nix');
	await expect(nav.getByRole('link', { name: 'machine.nix' })).toHaveAttribute(
		'aria-current',
		'page'
	);

	await makeProject('one-nix', 'nix');
	await page.goto('/projects');
	await page.goto('/settings');
	await expect(nav.getByRole('link', { name: 'machine.nix' })).toBeVisible();
});

test('adding a host shows the Mac step with its size and the x86 installer', async ({ page }) => {
	await ownedHost({ limit: 1, set_install: true, install_line: '' });
	try {
		await page.goto('/settings/hosts');
		await page.getByRole('button', { name: 'Add a host' }).click();
		const dialog = page.getByTestId('host-dialog');
		await dialog.getByLabel('Name', { exact: true }).fill('box');
		await dialog.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(dialog.getByRole('heading', { name: 'On a Mac', exact: true })).toBeVisible();
		// The step is a repose command, so the CLI comes first (review-dash P2 #3).
		await expect(dialog.getByTestId('host-mac-cli')).toHaveText(
			'The Mac needs the repose CLI and repose login first: Install.'
		);
		// Escape keeps the one view of the step (review-dash P3 #6).
		await page.keyboard.press('Escape');
		await expect(dialog.getByTestId('host-install')).toBeVisible();
		await expect(dialog.getByTestId('host-install')).toContainText('repose host add --join ');
		await expect(dialog.getByTestId('host-mac-size')).toHaveText(
			'Repose takes about 50 MB on the Mac, and the first machine downloads about 3 GB more; it needs 10 GB of free disk.'
		);
		// Finding 9: what the installer takes for the machines' disks.
		await expect(dialog.getByTestId('host-x86-disk')).toHaveText(
			"It keeps your machines' disks in /var/lib/repose/guests.img, or with --device takes over a whole empty disk."
		);
		const x86 = dialog.getByTestId('host-install-x86');
		// ENG-206: the argument goes through a file, never sudo's command line.
		await expect(x86).toContainText("printf '%s\\n' ");
		await expect(x86).toContainText(
			' > repose-join && curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join-file repose-join'
		);
		for (const width of [1440, 390]) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			const pre = x86.locator('pre');
			const o = await pre.evaluate((el) => ({ s: el.scrollWidth, c: el.clientWidth }));
			expect(o.s, `x86 line scrolled sideways at ${width}px`).toBeLessThanOrEqual(o.c + 1);
		}
		await shots(page, 'hosts-x86');
		await dialog.getByRole('button', { name: 'Done' }).click();
	} finally {
		const res = await fetch(`${apiURLFromEnv()}/hosts`, { headers: auth });
		for (const h of (await res.json()) as { id: string }[]) {
			await fetch(`${apiURLFromEnv()}/hosts/${h.id}`, { method: 'DELETE', headers: auth });
		}
		await ownedHost({ limit: 1, set_install: false });
	}
});

// ENG-204: removing an x86 host names the line that takes repose off the
// box, which nothing else does; a Mac's appliance goes with the CLI.
test('removing an x86 host shows the uninstall line', async ({ page }) => {
	await ownedHost({ limit: 2 });
	await ownedHost({ name: 'box', arch: 'x86_64' });
	await ownedHost({ name: 'mini' });
	try {
		await page.goto('/settings/hosts');
		await page.getByRole('button', { name: 'Remove mini…' }).click();
		await expect(page.getByTestId('host-uninstall-x86')).toHaveCount(0);
		await page.getByRole('button', { name: 'Keep it' }).click();
		await page.getByRole('button', { name: 'Remove box…' }).click();
		const confirm = page.getByTestId('confirm-remove-host');
		await expect(confirm.getByTestId('host-uninstall-x86')).toHaveText(
			'Repose keeps running on the box until you run this there as root:'
		);
		await expect(confirm).toContainText(
			'curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --uninstall'
		);
		for (const width of [1440, 390]) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			const pre = confirm.locator('pre');
			const o = await pre.evaluate((el) => ({ s: el.scrollWidth, c: el.clientWidth }));
			expect(o.s, `uninstall line scrolled sideways at ${width}px`).toBeLessThanOrEqual(o.c + 1);
		}
		await shots(page, 'hosts-x86-remove');
	} finally {
		const res = await fetch(`${apiURLFromEnv()}/hosts`, { headers: auth });
		for (const h of (await res.json()) as { id: string }[]) {
			await fetch(`${apiURLFromEnv()}/hosts/${h.id}`, { method: 'DELETE', headers: auth });
		}
		await ownedHost({ limit: 1 });
	}
});

// Council finding 5 (D3): a reload, or back then forward, during a build
// shows the running build again, not an idle page with Apply. On a Nix
// project since the menu left (I-1698).
test('a reload or back and forward during a build shows it again', async ({ page }) => {
	const p = await makeProject('nix-reload', 'nix');
	let hold = true;
	const released: (() => void)[] = [];
	// The op reads as running, and its log stays open, until released.
	await page.route(
		(url) => /\/ops\/[^/]+$/.test(url.pathname),
		async (route) => {
			if (!hold) return route.fallback();
			const res = await route.fetch();
			const body = await res.json();
			await route.fulfill({
				response: res,
				json: { ...body, state: 'running', phase: 'apply_config' }
			});
		}
	);
	await page.route(
		(url) => /\/ops\/[^/]+\/log$/.test(url.pathname),
		async (route) => {
			if (hold) await new Promise<void>((r) => released.push(r));
			await route.fallback();
		}
	);
	await page.goto(`/projects/${p.id}/config`);
	await page.locator('.cm-content').click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await page.getByRole('button', { name: 'Apply', exact: true }).click();
	const status = page.locator('.cbar-status');
	await expect(status).toContainText('Building…');

	await page.reload();
	await expect(status).toContainText('Building…');
	await expect(page.getByRole('button', { name: /Apply/ })).toBeDisabled();

	await page.goto(`/projects/${p.id}`);
	await page.goBack();
	await expect(status).toContainText('Building…');
	await page.goForward();
	await page.goBack();
	await expect(status).toContainText('Building…');

	hold = false;
	for (const r of released.splice(0)) r();
	await page.reload();
	await expect(status).not.toContainText('Building…', { timeout: 10_000 });
	await expect(page.getByRole('button', { name: 'Apply', exact: true })).toBeVisible();
});

// Council finding 7 (D5): Resize changes the size class, as `repose resize
// --size` does: a stopped machine at once, a running one after asking,
// with a stop and a start.
test('Resize changes the machine size, as repose resize --size does', async ({ page }) => {
	const p = await makeProject('ubuntu-size', 'erofs');
	await page.goto(`/projects/${p.id}`);
	await page.getByRole('button', { name: 'Resize…' }).click();
	const panel = page.locator('#resize-panel');
	await panel.getByLabel('Size').selectOption('xl');
	await panel.getByRole('button', { name: 'Change size' }).click();
	await expect(panel).toContainText('ends every process on it');
	await shots(page, 'resize-size-ask', panel);
	await panel.getByRole('button', { name: 'Stop, change and start' }).click();
	await expect
		.poll(
			async () =>
				(await (await fetch(`${apiURLFromEnv()}/projects/${p.id}`, { headers: auth })).json())
					.class,
			{ timeout: 15_000 }
		)
		.toBe('xl');
	await expect(page.locator('main')).toContainText('running', { timeout: 15_000 });
});

// Council finding 10 (D9): a waiting base shows on the Overview and the
// project list, not only on the Config tab (I-1522).
test('a waiting base shows on the Overview and the project list', async ({ page }) => {
	const p = await makeProject('ubuntu-waits', 'erofs');
	await withPendingBase(page, p.id, '2026.11.30');
	await page.route(
		(url) => url.pathname.endsWith('/v1/projects'),
		async (route) => {
			if (route.request().method() !== 'GET') return route.fallback();
			const res = await route.fetch();
			const list = (await res.json()) as { id: string }[];
			await route.fulfill({
				response: res,
				json: list.map((x) => (x.id === p.id ? { ...x, pending_base_version: '2026.11.30' } : x))
			});
		}
	);
	await page.goto(`/projects/${p.id}`);
	const details = page.locator('.details');
	await expect(details).toContainText('2026.11.30 at the next restart');
	await shots(page, 'overview-pending-base', details);
	await page.goto('/projects');
	const row = page.getByRole('row').filter({ hasText: p.slug });
	await expect(row).toContainText('new base at next restart');
});

