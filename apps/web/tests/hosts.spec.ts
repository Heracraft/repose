// Settings → Hosts (DECISIONS I-1366): the machines of your own that run
// your projects, with the same add, list and remove `repose host` has
// (the dashboard parity rule). The join token reaches the DOM once, in
// the dialog, and never in the list.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, ownedHost } from './helpers';

/**
 * The viewport at 1440 and 390 in both schemes, at 1x, for the review
 * CLAUDE.md asks for ("Judge visuals at real size"). Written only with
 * REPOSE_SHOTS_DIR set, as the other tabs' specs do.
 */
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

test.beforeEach(async ({ page }) => {
	// Every test starts from the plan's default of one host and the
	// fake's install line, whatever an earlier test left.
	await ownedHost({ limit: 1, set_install: false });
	await signIn(page);
});

const created: string[] = [];
test.afterAll(async () => {
	for (const id of created) {
		await fetch(`${apiURLFromEnv()}/projects/${id}`, {
			method: 'DELETE',
			headers: { Authorization: 'Bearer playwright' }
		});
	}
});

/**
 * Removes every host the account has, so each test starts empty. The
 * machines this file made go first: a host still holding one is refused
 * (`host_has_projects`), which is a case the tests below assert and
 * would otherwise leave a host behind for the next one.
 */
async function clearHosts(page: import('@playwright/test').Page) {
	const auth = { Authorization: 'Bearer playwright' };
	for (const id of created.splice(0)) {
		await fetch(`${apiURLFromEnv()}/projects/${id}`, { method: 'DELETE', headers: auth });
	}
	const res = await fetch(`${apiURLFromEnv()}/hosts`, { headers: auth });
	for (const h of (await res.json()) as { id: string }[]) {
		const gone = await fetch(`${apiURLFromEnv()}/hosts/${h.id}`, { method: 'DELETE', headers: auth });
		if (!gone.ok) throw new Error(`clearHosts: ${h.id}: ${gone.status} ${await gone.text()}`);
	}
	await page.goto('/settings/hosts');
}

test('Settings adds, lists and removes a host of your own', async ({ page }) => {
	await clearHosts(page);
	const section = page.locator('#hosts');
	await expect(
		section.getByRole('heading', { name: /^(Your own hardware|\d+ hosts?)$/ })
	).toBeVisible();

	const dialog = page.getByTestId('host-dialog');
	await expect(dialog.getByLabel('Name', { exact: true })).toHaveCount(0);

	// Add: a name, or none. The dialog says so rather than leaving the
	// field looking required.
	await section.getByRole('button', { name: 'Add a host' }).click();
	await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
	await expect(dialog.getByText('Leave it empty and it is named for you.')).toBeVisible();
	await dialog.getByLabel('Name', { exact: true }).fill('Mac Mini');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(dialog.getByText(/^1 to 32 of a-z/)).toBeVisible();

	await dialog.getByLabel('Name', { exact: true }).fill('mac-mini');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();

	// The one view of the step to run on the machine, with a copy button.
	await expect(dialog.getByRole('heading', { name: 'mac-mini' })).toBeVisible();
	await expect(dialog.getByText('Run this on the machine. It is not shown again.')).toBeVisible();
	const install = dialog.getByTestId('host-install');
	await expect(install).toBeVisible();
	await shots(page, 'hosts-added');
	const line = (await install.innerText()).trim();
	expect(line).toContain('REPOSE_JOIN_TOKEN=');
	await dialog.getByRole('button', { name: 'Done' }).click();
	await expect(dialog).not.toBeVisible();

	// The list has it, waiting, and carries no token.
	const table = page.getByTestId('own-hosts');
	const row = table.getByRole('row').filter({ hasText: 'mac-mini' });
	await expect(row).toContainText('waiting');
	await expect(section.getByRole('heading', { name: '1 host' })).toBeVisible();
	const token = line.split('REPOSE_JOIN_TOKEN=')[1].split(' ')[0];
	await expect(page.locator('body')).not.toContainText(token);

	// A second host is refused by the plan, in the api's own sentence.
	await section.getByRole('button', { name: 'Add a host' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('spare');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(page.getByText(/your plan includes 1 host/)).toBeVisible();
	await dialog.getByRole('button', { name: 'Cancel' }).click();

	// The same name twice is the name's own error, in the field.
	await section.getByRole('button', { name: 'Add a host' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('mac-mini');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(dialog.getByText('You already have a host called mac-mini.')).toBeVisible();
	await dialog.getByRole('button', { name: 'Cancel' }).click();

	// Remove is the two-step, and Keep it leaves the host alone.
	await row.getByRole('button', { name: 'Remove mac-mini…' }).click();
	const confirm = page.getByTestId('confirm-remove-host');
	await expect(confirm).toContainText('it joins again only with a new install');
	await shots(page, 'hosts-remove');
	await confirm.getByRole('button', { name: 'Keep it' }).click();
	await expect(confirm).not.toBeVisible();
	await expect(row).toBeVisible();

	await row.getByRole('button', { name: 'Remove mac-mini…' }).click();
	await page
		.getByTestId('confirm-remove-host')
		.getByRole('button', { name: 'Remove mac-mini now' })
		.click();
	// A removed host leaves the list: it is not hardware you have, and
	// `repose host ls` leaves it out too.
	await expect(row).toHaveCount(0);
	await expect(section.getByRole('heading', { name: 'Your own hardware' })).toBeVisible();
	// And the slot is free: a removed host does not hold the plan's one.
	await section.getByRole('button', { name: 'Add a host' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('mac-mini-2');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(dialog.getByRole('heading', { name: 'mac-mini-2' })).toBeVisible();
	await dialog.getByRole('button', { name: 'Done' }).click();
});

test('a host with no install line recorded shows the token and the page', async ({ page }) => {
	await clearHosts(page);
	// The live-today case: nothing has recorded an install line, so the
	// api answers an empty command and the page must not invent one.
	await ownedHost({ set_install: true, install_line: '' });
	await page.goto('/settings/hosts');
	await page.locator('#hosts').getByRole('button', { name: 'Add a host' }).click();
	const dialog = page.getByTestId('host-dialog');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(dialog.getByTestId('host-token')).toBeVisible();
	await expect(dialog.getByTestId('host-install')).toHaveCount(0);
	await expect(dialog.getByText(/Copy this token now/)).toBeVisible();
	await expect(dialog.getByRole('link', { name: 'What to do with it' })).toHaveAttribute(
		'href',
		'/docs/own-hardware'
	);
	// No name given, so the api named it.
	await expect(dialog.getByRole('heading', { name: 'host-1' })).toBeVisible();
});

test('a host that has registered shows its size, its machines and when it last answered', async ({
	page
}) => {
	await clearHosts(page);
	const hostId = await ownedHost({ name: 'mac-mini' });
	const p = await createProject(apiURLFromEnv(), {
		name: 'hosts-app',
		remote_url: 'github.com/heracraft/hosts-app'
	});
	created.push(p.id);
	await ownedHost({ project_id: p.id, host_id: hostId });

	await page.goto('/settings/hosts');
	const row = page.getByTestId('own-hosts').getByRole('row').filter({ hasText: 'mac-mini' });
	await expect(row).toContainText('online');
	await expect(row).toContainText('24 GB · 8 cores');
	await expect(row).toContainText('1 machine, 1 running');
	await expect(row).toContainText('aarch64');
	await shots(page, 'hosts-list');

	// A host still holding a machine is not removed, and the refusal
	// names the machine.
	await row.getByRole('button', { name: 'Remove mac-mini…' }).click();
	await page
		.getByTestId('confirm-remove-host')
		.getByRole('button', { name: 'Remove mac-mini now' })
		.click();
	await expect(
		page.getByText(new RegExp(`mac-mini still holds 1 machine.*${p.slug}`))
	).toBeVisible();
	// The host is still there, and the confirm stays open so the user can
	// read the refusal and say Keep it.
	await page.getByTestId('confirm-remove-host').getByRole('button', { name: 'Keep it' }).click();
	await expect(row).toContainText('online');

	// Switched off: Offline, and how long it has been quiet.
	await ownedHost({ id: hostId, quiet_minutes: 14 });
	await page.reload();
	const off = page.getByTestId('own-hosts').getByRole('row').filter({ hasText: 'mac-mini' });
	await expect(off).toContainText('offline');
	await expect(off).toContainText('14m ago');
	await shots(page, 'hosts-offline');
});

// The empty list is this page's most common state -- every account owns
// no host on the day this ships -- and it is where an effect that reads
// what it writes runs away: 19c's project page asked /hosts 291 times in
// two seconds that way (the empty Map it wrote was a new reference every
// time). Nothing here reads /hosts from an effect, and this counts the
// reads so it stays that way.
test('the page reads /hosts once, with no hosts and with one', async ({ page }) => {
	let reads = 0;
	await page.route('**/v1/hosts', async (route) => {
		if (route.request().method() === 'GET') reads++;
		await route.continue();
	});
	await clearHosts(page);
	await expect(page.locator('#hosts').getByText('No hosts of your own.')).toBeVisible();
	await page.waitForTimeout(2000);
	expect(reads).toBeLessThanOrEqual(3);

	// And with a host on it, where the row's own fields are read.
	reads = 0;
	await ownedHost({ name: 'mac-mini' });
	await page.goto('/settings/hosts');
	await expect(page.getByTestId('own-hosts')).toBeVisible();
	await page.waitForTimeout(2000);
	expect(reads).toBeLessThanOrEqual(3);
});

test('a plan that sells no host says so', async ({ page }) => {
	await clearHosts(page);
	await ownedHost({ limit: 0 });
	await page.goto('/settings/hosts');
	await page.locator('#hosts').getByRole('button', { name: 'Add a host' }).click();
	await page.getByTestId('host-dialog').getByRole('button', { name: 'Add', exact: true }).click();
	await expect(page.getByText(/Your plan does not include a host of your own/)).toBeVisible();
});

test('the Hosts tab is reachable from Settings at both widths', async ({ page }) => {
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/settings');
		const tab = page.getByRole('navigation', { name: 'Settings' }).getByRole('link', {
			name: 'Hosts'
		});
		await expect(tab).toBeVisible();
		// Every tab stays on the screen: a row that scrolls sideways hides
		// the last one, which is what I-1322 fixed for five tabs and this
		// is the sixth.
		const box = (await tab.boundingBox())!;
		expect(box.x).toBeGreaterThanOrEqual(0);
		expect(box.x + box.width).toBeLessThanOrEqual(width);
		await tab.click();
		await expect(page.locator('#hosts')).toBeVisible();
	}
});
