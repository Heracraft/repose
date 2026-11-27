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
	// Every test starts from the plan's default, any number of hosts
	// (I-1594), and the fake's install line, whatever an earlier test left.
	await ownedHost({ limit: -1, set_install: false });
	await signIn(page);
});

const created: string[] = [];
test.afterAll(async () => {
	const auth = { Authorization: 'Bearer playwright' };
	for (const id of created.splice(0)) {
		await fetch(`${apiURLFromEnv()}/projects/${id}`, {
			method: 'DELETE',
			headers: auth
		});
	}
	const res = await fetch(`${apiURLFromEnv()}/hosts`, { headers: auth });
	if (res.ok) {
		for (const h of (await res.json()) as { id: string }[]) {
			await fetch(`${apiURLFromEnv()}/hosts/${h.id}`, {
				method: 'DELETE',
				headers: auth
			});
		}
	}
	await ownedHost({ limit: -1, set_install: false });
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
		const gone = await fetch(`${apiURLFromEnv()}/hosts/${h.id}`, {
			method: 'DELETE',
			headers: auth
		});
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
	await expect(dialog.getByText(/Run this on the machine you are adding/)).toBeVisible();
	const install = dialog.getByTestId('host-install');
	await expect(install).toBeVisible();
	// The 360-char join command wraps across lines at both desktop and phone
	// widths so a user can read all of it without horizontal scrolling (ENG-63).
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
		const pre = install.locator('pre');
		const overflow = await pre.evaluate((el) => ({
			scrollWidth: el.scrollWidth,
			clientWidth: el.clientWidth
		}));
		expect(
			overflow.scrollWidth,
			`install command scrolled horizontally at ${width}px (${overflow.scrollWidth} > ${overflow.clientWidth})`
		).toBeLessThanOrEqual(overflow.clientWidth + 1);
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await shots(page, 'hosts-added');
	// The deployment's own installer line, which the fake records as
	// `repose host add --join %s`. Decoded by the rule the CLI parses it
	// with, not matched as a string: this assertion used to be
	// `toContain('repose host add --join ')`, which the recorded line
	// supplies whatever the dialog puts after it.
	const bundle = await joinBundleOf(install);
	await dialog.getByRole('button', { name: 'Done' }).click();
	await expect(dialog).not.toBeVisible();

	// The list has it, waiting, and carries no token.
	const table = page.getByTestId('own-hosts');
	const row = table.getByRole('row').filter({ hasText: 'mac-mini' });
	await expect(row).toContainText('waiting');
	await expect(section.getByRole('heading', { name: '1 host' })).toBeVisible();
	// The bundle's token, not the argument: the argument is on the page
	// by design and the token inside it never is.
	await expect(page.locator('body')).not.toContainText(bundle.token);

	// A second and a third host go in: the plan sells any number
	// (I-1594).
	for (const spare of ['spare', 'spare-2']) {
		await section.getByRole('button', { name: 'Add a host' }).click();
		await dialog.getByLabel('Name', { exact: true }).fill(spare);
		await dialog.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(dialog.getByRole('heading', { name: spare })).toBeVisible();
		await dialog.getByRole('button', { name: 'Done' }).click();
	}
	await expect(table.getByRole('row').filter({ hasText: 'spare' })).toHaveCount(2);
	await expect(section.getByRole('heading', { name: '3 hosts' })).toBeVisible();

	// A plan that sells no host refuses in the api's own sentence, and
	// the sentence is IN the dialog rather than behind it (ENG-25).
	await ownedHost({ limit: 0 });
	await section.getByRole('button', { name: 'Add a host' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('spare-3');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	const refusal = dialog.getByTestId('host-add-error');
	await expect(refusal).toHaveText(/A host of your own needs a plan/);
	// Visible is not enough: a toast is "visible" while sitting under the
	// dialog's backdrop, because a dialog opened with showModal() is in
	// the top layer and no z-index reaches it. The customer pressed Add,
	// nothing appeared to happen, and the reason was behind the thing
	// they were looking at. So this asks the browser what is actually at
	// the middle of the refusal, the way the review measured it.
	const onTop = await refusal.evaluate((el) => {
		const b = el.getBoundingClientRect();
		const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
		return el.contains(hit) || hit === el;
	});
	expect(onTop, 'the refusal is covered by something').toBe(true);
	await shots(page, 'hosts-refused');
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await ownedHost({ limit: -1 });

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
	await expect(section.getByRole('heading', { name: '2 hosts' })).toBeVisible();
	// A removed host's name is free again.
	await section.getByRole('button', { name: 'Add a host' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('mac-mini');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(dialog.getByRole('heading', { name: 'mac-mini' })).toBeVisible();
	await dialog.getByRole('button', { name: 'Done' }).click();
});

/**
 * The rule `repose host add --join` parses its argument by
 * (`hostmgr.DecodeJoinBundle`, and the appliance's join unit in shell):
 * base64url, JSON, version 1, a token in it. Asserting the dialog's text
 * against this rather than against a string shape is the whole point —
 * the spec used to assert that the copied text contained `repose host
 * add --join `, and that passed for weeks while the dialog showed a bare
 * 43-character token, because the prefix came from the assertion and not
 * from the page.
 */
async function joinBundleOf(
	step: import('@playwright/test').Locator
): Promise<{ v: number; token: string }> {
	// The command itself, not the box around it: the box carries the
	// Copy button's own label, and the line wraps on a phone, so the
	// argument is read off the `pre` with its whitespace removed —
	// base64url has none.
	const text = (await step.locator('pre').innerText()).trim();
	const prefix = 'repose host add --join ';
	expect(text.startsWith(prefix), `the step does not run anything: ${text}`).toBe(true);
	const arg = text.slice(prefix.length).replace(/\s+/g, '');
	const json = Buffer.from(arg, 'base64url').toString('utf8');
	const bundle = JSON.parse(json) as { v: number; token: string };
	expect(bundle.v).toBe(1);
	expect(typeof bundle.token).toBe('string');
	expect(bundle.token.length).toBeGreaterThan(0);
	return bundle;
}

test('with no install line recorded the dialog shows the join step, never the token', async ({
	page
}) => {
	await clearHosts(page);
	// The live-today case, and the one that shipped broken: no deployment
	// has recorded an install line, so the api answers an empty `command`
	// and the dialog must fall back to `install.argument` in the command
	// that takes it. It showed `join_token` instead, which a customer
	// cannot act on, and the dialog is one-shot so remove-and-add handed
	// out the same wrong value again (ENG-29).
	await ownedHost({ set_install: true, install_line: '' });
	await page.goto('/settings/hosts');
	await page.locator('#hosts').getByRole('button', { name: 'Add a host' }).click();
	const dialog = page.getByTestId('host-dialog');
	await dialog.getByRole('button', { name: 'Add', exact: true }).click();

	const install = dialog.getByTestId('host-install');
	await expect(install).toBeVisible();
	await expect(dialog.getByTestId('host-token')).toHaveCount(0);
	// What is on the page is what the CLI would accept.
	const bundle = await joinBundleOf(install);

	// And the token is not on the page in any form: the api sends it as
	// `join_token` too, and showing it is what this test exists to stop.
	await expect(page.locator('body')).not.toContainText(bundle.token);

	await expect(dialog.getByText(/Run this on the machine you are adding/)).toBeVisible();
	await expect(dialog.getByRole('link', { name: 'What this does' })).toHaveAttribute(
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

	// Offline and still holding a machine: this one IS removable
	// (DECISIONS I-1420), because refusing it was the dead end in
	// ENG-119 -- and it is the one path where removing a host loses
	// machines, so the question says so in the same words `repose host
	// rm` uses.
	await off.getByRole('button', { name: 'Remove mac-mini…' }).click();
	await expect(page.getByTestId('confirm-remove-host')).toContainText(
		'It is not answering, and the machine on it cannot be recovered'
	);
	await page.getByTestId('confirm-remove-host').getByRole('button', { name: 'Keep it' }).click();
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

test('an account with no plan is told a host needs one', async ({ page }) => {
	await clearHosts(page);
	await ownedHost({ limit: 0 });
	await page.goto('/settings/hosts');
	await page.locator('#hosts').getByRole('button', { name: 'Add a host' }).click();
	await page.getByTestId('host-dialog').getByRole('button', { name: 'Add', exact: true }).click();
	await expect(page.getByText(/A host of your own needs a plan/)).toBeVisible();
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

// Even with a 32-char host name or an unbroken long arch string, the table
// never overflows 390px and Remove stays fully on-screen and clickable (ENG-88).
test('a host row with a long name and unbroken arch fits 390px with Remove clickable', async ({
	page
}) => {
	await clearHosts(page);
	await ownedHost({
		name: 'a-very-long-hostname-up-to-32chr',
		arch: '/nix/store/b9khbvcckcvs6mmjrb77kp0yhznh41s1'
	});
	await page.goto('/settings/hosts');
	const table = page.getByTestId('own-hosts');
	await expect(table).toBeVisible();

	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
		const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
		expect(scrollWidth, `page overflowed horizontally at ${width}px`).toBeLessThanOrEqual(width);

		const btn = table.getByRole('button', { name: 'Remove a-very-long-hostname-up-to-32chr…' });
		await expect(btn).toBeVisible();
		const box = (await btn.boundingBox())!;
		expect(box.x, `Remove left edge at ${width}px`).toBeGreaterThanOrEqual(0);
		expect(box.x + box.width, `Remove right edge at ${width}px`).toBeLessThanOrEqual(width);
		const onTop = await btn.evaluate((el) => {
			const b = el.getBoundingClientRect();
			const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
			return el.contains(hit) || hit === el;
		});
		expect(onTop, `Remove is covered or clipped at ${width}px`).toBe(true);
	}
	await shots(page, 'hosts-long-row');
});

// A Mac that parks its machines restarts from here as `repose host
// restart` does on it (I-1690): Restart parks every machine and restarts
// the Mac; a machine that cannot be parked stops it with its name, and
// Restart anyway goes ahead. A host that cannot park offers no Restart.
test('a Mac that parks its machines restarts from the Hosts page', async ({ page }) => {
	await clearHosts(page);
	const hostId = await ownedHost({ name: 'mac-mini' });
	const p = await createProject(apiURLFromEnv(), {
		name: 'restart-app',
		remote_url: 'github.com/heracraft/restart-app'
	});
	created.push(p.id);
	await ownedHost({ project_id: p.id, host_id: hostId });

	await page.goto('/settings/hosts');
	const row = page.getByTestId('own-hosts').getByRole('row').filter({ hasText: 'mac-mini' });
	await expect(row).toContainText('online');
	await expect(row.getByRole('button', { name: 'Restart mac-mini…' })).toHaveCount(0);

	await ownedHost({ state_host: 'mac-mini', restart: true });
	await page.reload();
	await expect(row.getByRole('button', { name: 'Restart mac-mini…' })).toBeVisible();
	await shots(page, 'hosts-restart-row');
	await row.getByRole('button', { name: 'Restart mac-mini…' }).click();
	const confirm = page.getByTestId('confirm-restart-host');
	await expect(confirm).toContainText(
		'Its running machine is parked with its memory, then the Mac restarts; the machine runs again when you log in to the Mac.'
	);
	await shots(page, 'hosts-restart-confirm');
	await confirm.getByRole('button', { name: 'Restart mac-mini now' }).click();
	await expect(page.getByTestId('host-restarted')).toHaveText(
		'Parked 1 machine; mac-mini is restarting.'
	);

	// One machine the Mac cannot park: refused with its name, nothing
	// restarted; Restart anyway goes ahead and says it starts fresh.
	await ownedHost({ state_host: 'mac-mini', restart: true, not_parked: [p.slug] });
	await page.reload();
	await row.getByRole('button', { name: 'Restart mac-mini…' }).click();
	await confirm.getByRole('button', { name: 'Restart mac-mini now' }).click();
	await expect(page.getByTestId('host-restart-refused')).toContainText(
		`1 machine could not be parked and would start fresh (${p.slug}: started with nobody logged in). Nothing restarted`
	);
	await shots(page, 'hosts-restart-refused');
	await confirm.getByRole('button', { name: 'Restart mac-mini anyway' }).click();
	await expect(page.getByTestId('host-restarted')).toHaveText(
		`Parked 0 machines; mac-mini is restarting. ${p.slug} starts fresh.`
	);

	// A locked Mac parks nothing, and the refusal says to unlock it.
	await ownedHost({ state_host: 'mac-mini', restart: true, not_parked: [p.slug], locked: true });
	await page.reload();
	await row.getByRole('button', { name: 'Restart mac-mini…' }).click();
	await confirm.getByRole('button', { name: 'Restart mac-mini now' }).click();
	await expect(page.getByTestId('host-restart-refused')).toHaveText(
		"This Mac's screen is locked, and a locked Mac cannot save a machine's memory. Nothing restarted; unlock it and restart again."
	);
	await confirm.getByRole('button', { name: 'Cancel' }).click();

	// Offline: no Restart, since there is nothing to ask.
	await ownedHost({ id: hostId, quiet_minutes: 14 });
	await page.reload();
	await expect(row.getByRole('button', { name: 'Restart mac-mini…' })).toHaveCount(0);
});
