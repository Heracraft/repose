// The Ubuntu base on the dashboard (DECISIONS I-1462, I-1490): an erofs
// project's Config page is the menu, the base and, only while one waits,
// the next base with Restart; the Nix shape is unchanged; the settings
// nav lists machine.nix only for an account with a Nix project; the add
// host dialog shows the x86 installer beside the Mac's step.
import { test, expect, type Page } from '@playwright/test';
import { signIn, apiURLFromEnv, pruneProjects, ownedHost } from './helpers';

const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };

/** Captures the viewport at 1x, at 1440 and 390, light and dark (CLAUDE.md "Judge visuals at real size"). */
async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
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
		(url) => url.pathname.endsWith(`/projects/${id}`),
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

test('an erofs project shows the menu and its base, and no Nix editor', async ({ page }) => {
	const p = await makeProject('ubuntu-app', 'erofs');
	await page.goto(`/projects/${p.id}/config`);
	const facts = page.getByTestId('config-facts');
	await expect(facts).toContainText('Base');
	await expect(page.getByText('PostgreSQL', { exact: true })).toBeVisible();
	await expect(page.getByRole('tablist', { name: 'Config editor' })).toHaveCount(0);
	await expect(page.getByText('Use your machine.nix on this machine')).toHaveCount(0);
	await expect(page.locator('.cm-content')).toHaveCount(0);
	await expect(page.getByTestId('pending-base')).toHaveCount(0);
	await shots(page, 'config-erofs');

	await page.getByText('Zig', { exact: true }).click();
	await page.getByRole('button', { name: 'Apply', exact: true }).click();
	await expect(page.getByRole('heading', { name: 'Install log' })).toBeVisible();
	await expect(page.getByText('Applied.')).toBeVisible({ timeout: 10_000 });
	await expect(page.locator('.build-log')).toContainText('zig ready');
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

test('a Nix project keeps its tabs and machine.nix, and never shows the base line', async ({
	page
}) => {
	const p = await makeProject('nix-app', 'nix');
	// The api never sets it on a Nix project; even if it did, nothing shows.
	await withPendingBase(page, p.id, '2026.11.30');
	await page.goto(`/projects/${p.id}/config`);
	await expect(page.getByRole('tablist', { name: 'Config editor' })).toBeVisible();
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
		await expect(dialog.getByRole('heading', { name: 'On a Mac mini' })).toBeVisible();
		await expect(dialog.getByTestId('host-install')).toContainText('repose host add --join ');
		await expect(dialog.getByTestId('host-mac-size')).toHaveText(
			'It downloads about 250 MB and needs 20 GB of free disk.'
		);
		const x86 = dialog.getByTestId('host-install-x86');
		await expect(x86).toContainText(
			'curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join '
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
