// Checklist: "Menu tab renders every catalog group and search filters it;
// Apply sends {menu} and shows the generated fragment." and "Nix tab:
// editor with Nix highlighting, Apply, build log streams, a failing
// fragment shows the error block with the line highlighted."
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('menu tab renders catalog groups and search filters them', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'menu-app',
		remote_url: 'github.com/heracraft/menu-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Menu' }).click();

	await expect(page.getByText('runtimes')).toBeVisible();
	await expect(page.getByText('Services')).toBeVisible();
	await expect(page.getByText('PostgreSQL', { exact: true })).toBeVisible();

	await page.getByPlaceholder('Search packages and services…').fill('postgres');
	await expect(page.getByText('PostgreSQL', { exact: true })).toBeVisible();
	await expect(page.getByText('Bun', { exact: true })).toHaveCount(0);
});

test('applying a menu selection shows the generated fragment on the Nix tab', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'menu-apply-app',
		remote_url: 'github.com/heracraft/menu-apply-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Menu' }).click();
	await page.getByText('Zig', { exact: true }).click();
	await page.getByRole('button', { name: 'Apply', exact: true }).click();

	await expect(page.getByText('Applied.')).toBeVisible({ timeout: 10_000 });

	await page.getByRole('tab', { name: 'Nix' }).click();
	await expect(page.locator('.cm-content')).toContainText('pkgs.zig');
});

// A selection with nixpkgs packages added by name (`repose config add gcc`,
// DECISIONS I-220) lists them as "Extra packages", keeps them when the menu
// is applied again, and can drop one.
test('menu tab lists extra nixpkgs packages and keeps them on apply', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'menu-extra-app',
		remote_url: 'github.com/heracraft/menu-extra-app'
	});
	const put = await fetch(`${api}/projects/${p.id}/config`, {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json', Authorization: 'Bearer playwright' },
		body: JSON.stringify({ menu: [{ id: 'bun' }, { package: 'gcc' }, { package: 'air' }] })
	});
	expect(put.status).toBe(202);

	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Menu' }).click();
	await expect(page.getByRole('heading', { name: 'Extra packages' })).toBeVisible();
	await expect(page.getByText('gcc', { exact: true })).toBeVisible();
	await expect(page.getByText('air', { exact: true })).toBeVisible();
	await expect(page.getByRole('checkbox', { name: /^Bun/ })).toBeChecked();

	await page.getByRole('button', { name: 'Remove air' }).click();
	await page.getByRole('button', { name: 'Apply', exact: true }).click();
	await expect(page.getByText('Applied.')).toBeVisible({ timeout: 10_000 });

	await page.getByRole('tab', { name: 'Nix' }).click();
	await expect(page.locator('.cm-content')).toContainText('"gcc"');
	await expect(page.locator('.cm-content')).not.toContainText('"air"');
	await expect(page.locator('.cm-content')).toContainText('pkgs.bun');
});

test('a failing fragment shows the error block with the fragment line highlighted', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'broken-fragment-app',
		remote_url: 'github.com/heracraft/broken-fragment-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Nix' }).click();

	await page.locator('.cm-content').click();
	await page.keyboard.press('Control+A');
	await page.keyboard.type('{ pkgs, ... }: /* repose-force-eval-error */ { }');
	await page.getByRole('button', { name: 'Apply', exact: true }).click();

	await expect(page.getByText(/syntax error at fragment\.nix:1:32/)).toBeVisible({
		timeout: 10_000
	});
	await expect(page.locator('.cm-line-error')).toBeVisible();
});

test('hold base updates toggle round-trips', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hold-app',
		remote_url: 'github.com/heracraft/hold-app'
	});
	await page.goto(`/projects/${p.id}/config`);

	const hold = page.getByRole('checkbox', { name: /Hold base updates/ });
	await expect(hold).not.toBeChecked();

	await hold.check();
	await expect(hold).toBeChecked();

	await page.reload();
	await expect(page.getByRole('checkbox', { name: /Hold base updates/ })).toBeChecked();
});

test('the Menu and Nix tabs are ARIA tabs the arrow keys move between', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'tabs-app',
		remote_url: 'github.com/heracraft/tabs-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	const menu = page.getByRole('tab', { name: 'Menu' });
	const nix = page.getByRole('tab', { name: 'Nix' });
	await menu.click();
	await expect(menu).toHaveAttribute('aria-selected', 'true');
	await expect(page.getByRole('tabpanel', { name: 'Menu' })).toBeVisible();
	await page.keyboard.press('ArrowRight');
	await expect(nix).toBeFocused();
	await expect(nix).toHaveAttribute('aria-selected', 'true');
	await expect(menu).toHaveAttribute('tabindex', '-1');
	await expect(page.getByRole('tabpanel', { name: 'Nix' })).toBeVisible();
	await page.keyboard.press('Home');
	await expect(menu).toBeFocused();
	await expect(page.getByRole('tabpanel', { name: 'Menu' })).toBeVisible();
});

// Apply sits in one bar held to the foot of the window, beside the state
// of the tab in front (I-664, I-837), which says when a change is not
// applied.
test('the Apply bar says Unsaved changes beside Apply, which waits for a change', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'nix-bar-app',
		remote_url: 'github.com/heracraft/nix-bar-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Nix' }).click();
	const panel = page.getByRole('tabpanel', { name: 'Nix' });
	const bar = page.locator('.hk-bottom-bar');
	// Nothing to apply: the bar says nothing (the tab's head says when it
	// was applied), and Apply is off, as on the Menu tab.
	await expect(bar.getByRole('status')).toHaveText('');
	const apply = bar.getByRole('button', { name: 'Apply', exact: true });
	await expect(apply).toBeVisible();
	await expect(apply).toBeDisabled();
	await expect(apply).toHaveClass(/hk-btn-secondary/);
	await panel.locator('.cm-content').click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await expect(bar.getByRole('status')).toHaveText('Unsaved changes');
	await expect(apply).toBeEnabled();
	await expect(apply).toHaveClass(/(^|\s)hk-btn(\s|$)/);
});

test('a Menu search that matches nothing says so', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'menu-none-app',
		remote_url: 'github.com/heracraft/menu-none-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('searchbox', { name: 'Search packages and services' }).fill('zzzz');
	await expect(page.getByRole('tabpanel', { name: 'Menu' })).toContainText(
		'No package matches “zzzz”.'
	);
});

test('at lg the revisions sit in a rail beside the editor', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'rail-app',
		remote_url: 'github.com/heracraft/rail-app'
	});
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Nix' }).click();
	const editor = await page.locator('.nix-frame').boundingBox();
	const revisions = await page.getByRole('heading', { name: 'Revisions' }).boundingBox();
	expect(revisions!.x).toBeGreaterThan(editor!.x + editor!.width);
	expect(revisions!.x + 300).toBeGreaterThanOrEqual(1200);
	// The editor is the page's subject: it opens with room to write in
	// (28rem) even for an empty fragment.
	expect((await page.locator('.nix-editor').boundingBox())!.height).toBeGreaterThanOrEqual(440);
});

// An edit not applied holds a navigation away: Stay keeps it, Leave goes
// on (I-578's guard, on the config page since I-837).
test('leaving with an unapplied edit asks first', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'nix-guard-app',
		remote_url: 'github.com/heracraft/nix-guard-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('tab', { name: 'Nix' }).click();
	await page.locator('.cm-content').click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await page
		.getByRole('navigation', { name: 'Project' })
		.getByRole('link', { name: 'Overview' })
		.click();
	const ask = page.getByRole('alert').filter({ hasText: 'Unsaved changes' });
	await expect(ask).toBeVisible();
	await expect(ask.getByRole('button', { name: 'Stay' })).toBeFocused();
	// One banner per fact: the bar under it does not say it again.
	await expect(page.locator('.hk-bottom-bar').getByRole('status')).not.toContainText(
		'Unsaved changes'
	);
	await ask.getByRole('button', { name: 'Stay' }).click();
	await expect(ask).toHaveCount(0);
	await expect(page.locator('.hk-bottom-bar').getByRole('status')).toHaveText('Unsaved changes');
	await expect(page).toHaveURL(`/projects/${p.id}/config`);

	await page
		.getByRole('navigation', { name: 'Project' })
		.getByRole('link', { name: 'Overview' })
		.click();
	await page.getByRole('alert').getByRole('button', { name: 'Leave' }).click();
	await expect(page).toHaveURL(`/projects/${p.id}`);
});
