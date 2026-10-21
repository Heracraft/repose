// Checklist: "Settings: timezone, email toggle, ntfy URL, test button." and
// "Account deletion flow requires typing the handle and explains
// retention." The account is a section of Settings (DECISIONS I-578), and
// Settings is four tabs: General, Notifications, Secrets, machine.nix (I-841).
import { test, expect } from '@playwright/test';
import { fail, failNext, signIn, unfail } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

/** The Settings tabs, by name. */
const tab = (page: import('@playwright/test').Page, name: string) =>
	page.getByRole('navigation', { name: 'Settings' }).getByRole('link', { name, exact: true });

test('settings round-trips timezone, email toggle and ntfy URL, and the test button fires', async ({
	page
}) => {
	await page.goto('/settings');
	await expect(page).toHaveTitle(/^(\(\d+\) )?Settings · repose$/);

	// DECISIONS I-332: the checkbox and the timezone save on change, with a
	// toast; the ntfy URL has its own Save.
	// The account's own zone shows even when the browser's list lacks it (UTC).
	await expect(page.getByLabel('Timezone')).not.toHaveValue('');
	await page.getByLabel('Timezone').selectOption('Europe/Berlin');
	await expect(page.getByText('Timezone set to Europe/Berlin.')).toBeVisible();

	await tab(page, 'Notifications').click();
	await expect(page).toHaveURL('/settings/notifications');
	await expect(page).toHaveTitle(/^(\(\d+\) )?Notifications · Settings · repose$/);
	await expect(tab(page, 'Notifications')).toHaveAttribute('aria-current', 'page');
	const emailToggle = page.getByRole('checkbox', { name: 'Email notifications' });
	await emailToggle.uncheck();
	await expect(page.getByText('Email notifications off.')).toBeVisible();

	const save = page
		.locator('form', { has: page.getByLabel('ntfy URL') })
		.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeDisabled();
	await page.getByLabel('ntfy URL').fill('https://ntfy.sh/repose-test');
	await expect(page.getByText('Not saved yet.')).toBeVisible();
	await save.click();
	await expect(page.getByText('ntfy URL saved.')).toBeVisible();
	await expect(save).toBeDisabled();

	await page.reload();
	await expect(page.getByLabel('ntfy URL')).toHaveValue('https://ntfy.sh/repose-test');
	await expect(page.getByRole('checkbox', { name: 'Email notifications' })).not.toBeChecked();

	await page.getByRole('checkbox', { name: 'Email notifications' }).check();
	await expect(page.getByText('Email notifications on.')).toBeVisible();

	// The outcome stays under the button, naming each channel (I-841).
	await page.getByRole('button', { name: 'Send test' }).click();
	await expect(page.getByTestId('test-report')).toHaveText(
		'Test notification sent by email and ntfy.'
	);
	// A channel that refused it is named with the reason.
	await page.getByLabel('ntfy URL').fill('https://ntfy.sh/repose-fail');
	await save.click();
	await expect(save).toBeDisabled();
	await page.getByRole('button', { name: 'Send test' }).click();
	await expect(page.getByTestId('test-report')).toHaveText(
		'Test notification sent by email. Not sent by ntfy (the server answered 403).'
	);

	await tab(page, 'General').click();
	await expect(page).toHaveURL('/settings');
	await expect(page.getByLabel('Timezone')).toHaveValue('Europe/Berlin');

	// Put the fake's account back for later specs.
	await tab(page, 'Notifications').click();
	await page.getByLabel('ntfy URL').fill('');
	await save.click();
	await expect(page.getByText('ntfy turned off.')).toBeVisible();
});

test('a failed email toggle reverts the checkbox', async ({ page }) => {
	await page.goto('/settings/notifications');
	const emailToggle = page.getByRole('checkbox', { name: 'Email notifications' });
	const was = await emailToggle.isChecked();
	await failNext('PATCH', '/me', 'internal');
	await emailToggle.click();
	await expect(page.locator('[data-sonner-toast][data-type="error"]')).toBeVisible();
	await expect(emailToggle).toBeChecked({ checked: was });
});

test('leaving Notifications with an unsaved ntfy URL asks first', async ({ page }) => {
	await page.goto('/settings/notifications');
	await page.getByLabel('ntfy URL').fill('https://ntfy.sh/repose-unsaved');

	// The page asks in place, with the house banner, not a native dialog.
	let dialogs = 0;
	page.on('dialog', (d) => {
		dialogs++;
		void d.dismiss();
	});

	// Other specs can leave questions pending, which the link's name counts.
	const projects = page
		.getByRole('navigation', { name: 'Dashboard' })
		.getByRole('link', { name: /^Projects\b/ });
	await projects.click();
	const ask = page.getByRole('alert').filter({ hasText: 'The ntfy URL is not saved.' });
	await expect(ask).toBeVisible();
	await expect(page.getByRole('button', { name: 'Stay' })).toBeFocused();
	await page.getByRole('button', { name: 'Stay' }).click();
	await expect(ask).toHaveCount(0);
	// Focus goes back to the link that asked to leave, not to <body> (I-393).
	await expect(projects).toBeFocused();
	await expect(page.getByLabel('ntfy URL')).toHaveValue('https://ntfy.sh/repose-unsaved');
	await expect(page).toHaveURL('/settings/notifications');

	// Another Settings tab is a navigation too, and asks the same way.
	await tab(page, 'General').click();
	await expect(ask).toBeVisible();
	await page.getByRole('button', { name: 'Stay' }).click();
	await expect(tab(page, 'General')).toBeFocused();

	await projects.click();
	await page.getByRole('button', { name: 'Leave' }).click();
	await expect(page).toHaveURL('/projects');
	expect(dialogs).toBe(0);
});

// machine.nix is its own tab since I-841: an unsaved edit asks before
// another tab opens, with the same Stay and Leave.
test('leaving machine.nix unsaved for another tab asks first', async ({ page }) => {
	await page.goto('/settings/machine-nix');
	const nix = page.locator('#machine-nix');
	await nix.locator('.cm-content').click();
	await page.keyboard.type('{ }');
	await expect(nix.getByText('Unsaved changes')).toBeVisible();

	await tab(page, 'Secrets').click();
	const nixAsk = nix.getByRole('alert').filter({ hasText: 'machine.nix is not saved.' });
	await expect(nixAsk).toBeVisible();
	await expect(nixAsk.getByRole('button', { name: 'Stay', exact: true })).toBeFocused();
	await nixAsk.getByRole('button', { name: 'Leave', exact: true }).click();
	await expect(page).toHaveURL('/settings/secrets');
});

// A failed first load used to leave the page on "Loading…" for good.
test('/settings shows a failed first load and Retry loads it', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Timezone' })).toBeVisible();
	// Every read fails until unfail: the rail's own read of /me in the
	// root layout (I-841) would otherwise take a one-shot failure.
	await fail('GET', '/me', 'internal');
	try {
		await page.reload();
		await expect(page.getByRole('alert')).toBeVisible();
	} finally {
		await unfail('GET', '/me');
	}
	const failed = page.getByRole('alert');
	await expect(page.getByText('Loading…')).toHaveCount(0);
	// Install does not read /v1/me, so it stays.
	for (const heading of ['Install']) {
		await expect(page.getByRole('heading', { level: 2, name: heading, exact: true })).toBeVisible();
	}
	await expect(page.getByRole('heading', { level: 2, name: 'Account', exact: true })).toHaveCount(
		0
	);
	await failed.getByRole('button', { name: 'Retry' }).click();
	for (const heading of ['Account', 'Timezone', 'Delete account']) {
		await expect(page.getByRole('heading', { level: 2, name: heading, exact: true })).toBeVisible();
	}
	await expect(failed).toHaveCount(0);
});

// DECISIONS I-578, I-841: the account is the first section of General,
// under the tabs, and each tab holds its own sections once.
test('each Settings tab holds its own sections, the account first', async ({ page }) => {
	await page.goto('/settings');
	const account = page.locator('#account');
	await expect(account.getByRole('heading', { level: 2, name: 'Account' })).toBeVisible();
	await expect(account.getByText('heracraft').first()).toBeVisible();
	await expect(account.getByText('Email')).toBeVisible();
	await expect(account.getByText('GitHub')).toBeVisible();
	await expect(page.getByRole('heading', { level: 2, name: 'Delete account' })).toBeVisible();
	const h2 = () => page.locator('main h2').allInnerTexts();
	expect(await h2()).toEqual(['Account', 'Timezone', 'Install', 'Delete account']);
	const tabs = page.getByRole('navigation', { name: 'Settings' }).getByRole('link');
	await expect(tabs).toHaveText(['General', 'Notifications', 'Secrets', 'machine.nix']);
	await expect(tab(page, 'General')).toHaveAttribute('aria-current', 'page');

	for (const [name, url, headings] of [
		['Notifications', '/settings/notifications', ['Email', 'ntfy', 'Google Chat', 'Test']],
		['Secrets', '/settings/secrets', ['Secrets']],
		['machine.nix', '/settings/machine-nix', ['machine.nix']]
	] as const) {
		await tab(page, name).click();
		await expect(page).toHaveURL(url);
		await expect(tab(page, name)).toHaveAttribute('aria-current', 'page');
		await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
		await expect.poll(h2).toEqual([...headings]);
	}
});

// The tabs fit at 360 and 390 without sideways scroll, and the title and
// tabs stay at one y from tab to tab.
test('the Settings tabs fit a phone and hold still between tabs', async ({ page }) => {
	for (const width of [360, 390]) {
		await page.setViewportSize({ width, height: 800 });
		await page.goto('/settings');
		const nav = page.getByRole('navigation', { name: 'Settings' });
		await expect(nav.getByRole('link', { name: 'machine.nix' })).toBeInViewport({ ratio: 1 });
		expect(await nav.evaluate((n) => n.scrollWidth <= n.clientWidth)).toBe(true);
	}
	await page.setViewportSize({ width: 1440, height: 900 });
	const ys: number[] = [];
	for (const url of [
		'/settings',
		'/settings/notifications',
		'/settings/secrets',
		'/settings/machine-nix'
	]) {
		await page.goto(url);
		const nav = page.getByRole('navigation', { name: 'Settings' });
		await expect(nav).toBeVisible();
		ys.push(Math.round((await nav.boundingBox())!.y));
	}
	expect(new Set(ys).size).toBe(1);
});

test('account deletion requires typing the exact handle', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Everything, including snapshots, is deleted 30 days')).toBeVisible();

	// The field opens in place of its button and takes the focus; Keep it
	// closes it and gives the focus back (I-386, I-837).
	const open = page.getByRole('button', { name: 'Delete account…', exact: true });
	await open.click();
	await expect(page.getByLabel(/to confirm/)).toBeFocused();
	await page.locator('#delete-account').getByRole('button', { name: 'Keep it' }).click();
	await expect(open).toBeFocused();
	await open.click();

	const deleteBtn = page.getByRole('button', { name: 'Delete account', exact: true });
	await expect(deleteBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill('not-my-handle');
	await expect(deleteBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill('heracraft');
	await expect(deleteBtn).toBeEnabled();
	await deleteBtn.click();

	await expect(page).toHaveURL('/', { timeout: 10_000 });
});

// I-841: every row is two tracks from a 640px column, the h2 in 12rem
// and the controls in at most 40rem, so the account's facts and the
// install line start at one x, and on a phone the row is one column.
test('settings rows share one control column', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/settings');
	const facts = page.locator('#account dl');
	await expect(facts).toBeVisible();
	const install = page.locator('#install .set-body');
	const f = (await facts.boundingBox())!;
	const line = (await install.boundingBox())!;
	expect(Math.abs(line.x - f.x)).toBeLessThan(1);
	expect(Math.round(f.width)).toBeLessThanOrEqual(640);
	// The h2 column is 12rem and the gap 3rem: the controls start 240px in.
	const h2 = (await page
		.getByRole('heading', { level: 2, name: 'Account', exact: true })
		.boundingBox())!;
	expect(Math.round(f.x - h2.x)).toBe(240);
	// The account's key and value sit side by side.
	const k = (await page.locator('#account dt', { hasText: 'Handle' }).boundingBox())!;
	const v = (await page.locator('#account dd').first().boundingBox())!;
	expect(v.x - (k.x + k.width)).toBeLessThan(200);
	expect(Math.abs(v.y - k.y)).toBeLessThan(4);
	// Notifications draws the same rows.
	await tab(page, 'Notifications').click();
	const ntfy = (await page.getByLabel('ntfy URL').boundingBox())!;
	expect(Math.abs(ntfy.x - f.x)).toBeLessThan(1);

	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/settings');
	const f2 = (await facts.boundingBox())!;
	const h22 = (await page
		.getByRole('heading', { level: 2, name: 'Account', exact: true })
		.boundingBox())!;
	expect(Math.abs(f2.x - h22.x)).toBeLessThan(1);
	expect(f2.y).toBeGreaterThan(h22.y + h22.height);
});

// The Save buttons are secondary ones: at rest the flat disabled look, and
// once there is a change the enabled .hk-btn-secondary, which must not look
// disabled.
test('a Save with a change to save does not look disabled', async ({ page }) => {
	await page.goto('/settings/notifications');
	const save = page
		.locator('form', { has: page.getByLabel('ntfy URL') })
		.getByRole('button', { name: 'Save', exact: true });
	const colour = () => save.evaluate((b) => getComputedStyle(b).color);
	const resting = await colour();
	await page.getByLabel('ntfy URL').fill('https://ntfy.sh/repose-look');
	await expect(save).toBeEnabled();
	// The colour transitions over 150 ms (the kit's buttons), so one read taken right
	// after the button is enabled can still see the resting colour: this failed
	// once in each full run of the suite on the r20261015-2 tree and never when
	// the file ran alone. The matcher below retries until the colour changes.
	await expect(save).not.toHaveCSS('color', resting);
});

test('Sign out on Settings signs out', async ({ page }) => {
	await page.goto('/settings');
	await page.locator('#account').getByRole('button', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/', { timeout: 10_000 });
	await page.goto('/settings');
	await expect(page).not.toHaveURL('/settings');
});
