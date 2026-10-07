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

	const projects = page.getByRole('link', { name: 'Projects', exact: true });
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
	await expect(nix.getByText('Not saved yet.')).toBeVisible();

	await page.getByRole('link', { name: 'Projects', exact: true }).click();
	await expect(page.getByText('The ntfy URL is not saved.')).toBeVisible();
	await expect(nix.getByText('machine.nix is not saved.')).toBeVisible();
	await nix.getByRole('button', { name: 'Leave anyway' }).click();
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
	const headings = await page.locator('main h2').allInnerTexts();
	expect(headings).toEqual([
		'Account',
		'Timezone',
		'Notifications',
		'Install',
		'machine.nix',
		'Delete account'
	]);
});

test('account deletion requires typing the exact handle', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Everything, including snapshots, is deleted 30 days')).toBeVisible();

	const deleteBtn = page.getByRole('button', { name: 'Delete account' });
	await expect(deleteBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill('not-my-handle');
	await expect(deleteBtn).toBeDisabled();

	await page.getByLabel(/to confirm/).fill('heracraft');
	await expect(deleteBtn).toBeEnabled();
	await deleteBtn.click();

	await expect(page).toHaveURL('/', { timeout: 10_000 });
});
