// Checklist: "Settings: timezone, email toggle, ntfy URL, test button." and
// "Account deletion flow requires typing the handle and explains
// retention." The account is a section of Settings (DECISIONS I-578).
import { test, expect } from '@playwright/test';
import { failNext, signIn } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('settings round-trips timezone, email toggle and ntfy URL, and the test button fires', async ({
	page
}) => {
	await page.goto('/settings');
	await expect(page).toHaveTitle('Settings · repose');

	// DECISIONS I-332: the checkbox and the timezone save on change, with a
	// toast; the ntfy URL has its own Save.
	// The account's own zone shows even when the browser's list lacks it (UTC).
	await expect(page.getByLabel('Timezone')).not.toHaveValue('');

	const emailToggle = page.getByRole('checkbox', { name: 'Email notifications' });
	await emailToggle.uncheck();
	await expect(page.getByText('Email notifications off.')).toBeVisible();

	await page.getByLabel('Timezone').selectOption('Europe/Berlin');
	await expect(page.getByText('Timezone set to Europe/Berlin.')).toBeVisible();

	// machine.nix has a Save of its own further down the page (I-578).
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
	await expect(page.getByLabel('Timezone')).toHaveValue('Europe/Berlin');

	await page.getByRole('checkbox', { name: 'Email notifications' }).check();
	await expect(page.getByText('Email notifications on.')).toBeVisible();

	await page.getByRole('button', { name: 'Send test' }).click();
	await expect(page.getByText(/Test notification/)).toBeVisible();
});

test('a failed email toggle reverts the checkbox', async ({ page }) => {
	await page.goto('/settings');
	const emailToggle = page.getByRole('checkbox', { name: 'Email notifications' });
	const was = await emailToggle.isChecked();
	await failNext('PATCH', '/me', 'internal');
	await emailToggle.click();
	await expect(page.locator('[data-sonner-toast][data-type="error"]')).toBeVisible();
	await expect(emailToggle).toBeChecked({ checked: was });
});

test('leaving settings with an unsaved ntfy URL asks first', async ({ page }) => {
	await page.goto('/settings');
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
	await expect(page).toHaveURL('/settings');

	await projects.click();
	await page.getByRole('button', { name: 'Leave' }).click();
	await expect(page).toHaveURL('/projects');
	expect(dialogs).toBe(0);
});

// Settings has two guarded forms since I-578. Both ask on the same
// navigation, and one Leave passes both: each used to cancel the other's
// Leave, so the page never let go.
test('with the ntfy URL and machine.nix both unsaved, one Leave leaves', async ({ page }) => {
	await page.goto('/settings');
	await page.getByLabel('ntfy URL').fill('https://ntfy.sh/repose-unsaved');
	const nix = page.locator('#machine-nix');
	await nix.locator('.cm-content').click();
	await page.keyboard.type('{ }');
	await expect(nix.getByText('Unsaved changes')).toBeVisible();

	await page
		.getByRole('navigation', { name: 'Dashboard' })
		.getByRole('link', { name: /^Projects\b/ })
		.click();
	await expect(page.getByText('The ntfy URL is not saved.')).toBeVisible();
	const nixAsk = nix.getByRole('alert').filter({ hasText: 'machine.nix is not saved.' });
	await expect(nixAsk).toBeVisible();
	// Both questions have one shape: Stay, which has focus, and Leave.
	await expect(nixAsk.getByRole('button', { name: 'Stay', exact: true })).toBeVisible();
	await nixAsk.getByRole('button', { name: 'Leave', exact: true }).click();
	await expect(page).toHaveURL('/projects');
});

// A failed first load used to leave the page on "Loading…" for good.
test('/settings shows a failed first load and Retry loads it', async ({ page }) => {
	// Loaded once first, so the projects page that signIn lands on has
	// made its own GET /me and cannot take the one failure; the reload
	// is then the only request for it.
	await page.goto('/settings');
	await expect(page.getByRole('heading', { name: 'Timezone' })).toBeVisible();
	await failNext('GET', '/me', 'internal');
	await page.reload();
	const failed = page.getByRole('alert');
	await expect(failed).toBeVisible();
	await expect(page.getByText('Loading…')).toHaveCount(0);
	// Install and machine.nix do not read /v1/me, so they stay.
	for (const heading of ['Install', 'machine.nix']) {
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

// DECISIONS I-578: the account is the first section of Settings, under the
// page title, and its headings appear once each.
test('settings carries the account section first and no heading twice', async ({ page }) => {
	await page.goto('/settings');
	const account = page.locator('#account');
	await expect(account.getByRole('heading', { level: 2, name: 'Account' })).toBeVisible();
	await expect(account.getByText('heracraft').first()).toBeVisible();
	await expect(account.getByText('Email')).toBeVisible();
	await expect(account.getByText('GitHub')).toBeVisible();
	await expect(page.getByRole('heading', { level: 2, name: 'Delete account' })).toBeVisible();
	// Notifications second, the section people come back to (I-837).
	const headings = await page.locator('main h2').allInnerTexts();
	expect(headings).toEqual([
		'Account',
		'Notifications',
		'Timezone',
		'Install',
		'machine.nix',
		'Secrets',
		'Delete account'
	]);
});

test('account deletion requires typing the exact handle', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Everything, including snapshots, is deleted 30 days')).toBeVisible();

	// The field opens in place of its button and takes the focus; Cancel
	// closes it and gives the focus back (I-386, I-837).
	const open = page.getByRole('button', { name: 'Delete account…', exact: true });
	await open.click();
	await expect(page.getByLabel(/to confirm/)).toBeFocused();
	await page.locator('#delete-account').getByRole('button', { name: 'Cancel' }).click();
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

// I-837: every section is two tracks from 1024px, the h2 in 15rem and
// the controls in one 40rem column, the machine.nix editor included, so
// the right edges line up from the account's facts to the editor.
test('settings sections share one 40rem column at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/settings');
	const editor = page.locator('#machine-nix .nix-frame');
	await expect(editor).toBeVisible();
	const install = page.locator('section', { has: page.getByRole('heading', { name: 'Install' }) });
	const box = (await editor.boundingBox())!;
	const facts = (await page.locator('#account dl').boundingBox())!;
	const line = (await install.locator('pre').boundingBox())!;
	expect(Math.round(box.width)).toBe(640);
	expect(Math.abs(box.x - facts.x)).toBeLessThan(1);
	expect(Math.abs(box.x + box.width - (facts.x + facts.width))).toBeLessThan(1);
	expect(line.x).toBeGreaterThanOrEqual(box.x);
	// The h2 column is 15rem and the gap 3rem: the controls start 288px in.
	const h2 = (await page
		.getByRole('heading', { level: 2, name: 'Account', exact: true })
		.boundingBox())!;
	expect(Math.round(box.x - h2.x)).toBe(288);
	// The account's key and value sit side by side, not 672px apart.
	const handle = page.locator('#account dt', { hasText: 'Handle' });
	const value = page.locator('#account dd').first();
	const k = await handle.boundingBox();
	const v = await value.boundingBox();
	expect(v!.x - (k!.x + k!.width)).toBeLessThan(200);
	expect(Math.abs(v!.y - k!.y)).toBeLessThan(4);
});

// The Save buttons are secondary ones: at rest the flat disabled look, and
// once there is a change the enabled .hk-btn-secondary, which must not look
// disabled.
test('a Save with a change to save does not look disabled', async ({ page }) => {
	await page.goto('/settings');
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
