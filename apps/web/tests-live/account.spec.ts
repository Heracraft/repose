// The live suite's signed-in half. It runs against the deployed dashboard
// with a real Logto session captured by `pnpm run live:auth`, and closes
// the rows of docs/workstreams/08-dashboard.md §9 that say "the real
// Logto" — sign-in, callback, token refresh, sign-out — plus settings and
// its account section against a real account rather than internal/fakes/api.
//
// It is deliberately non-destructive. It changes the signed-in user's
// settings and puts them back; it asserts on the delete-account control
// without pressing it; it creates no project and touches no guest (the m3
// session owns host-01). The one exception is `Send test`, which is a
// notification the owner asked for by running this suite.
import { test, expect, type Page } from '@playwright/test';
import { LIVE_URL, haveAuthState, NO_AUTH_REASON } from './live';

test.skip(!haveAuthState(), NO_AUTH_REASON);

/** Waits for the signed-in shell rather than for a fixed time. */
async function gotoSignedIn(page: Page, path: string) {
	await page.goto(path);
	await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible({ timeout: 30_000 });
}

test('the saved session lands on /projects, not the landing page', async ({ page }) => {
	await page.goto('/');
	// The layout redirects an authenticated visitor away from `/`.
	await expect(page).toHaveURL(`${LIVE_URL}/projects`, { timeout: 30_000 });
	await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
	// Either a project table or the empty-state card, never "Loading…"
	// forever: both mean GET /projects answered with a real token.
	await expect(page.locator('table').or(page.getByText('No projects yet'))).toBeVisible({
		timeout: 30_000
	});
});

// 08 §9 "Sign-in, callback, token refresh and sign-out work against the
// real Logto". Dropping the stored access token and reloading forces the
// SDK to spend its refresh token against Logto's real token endpoint; the
// page then has to load data again, which it cannot do without a new one.
test('a dropped access token is refreshed against the real Logto', async ({ page }) => {
	await gotoSignedIn(page, '/projects');
	const dropped = await page.evaluate(() => {
		const keys = Object.keys(localStorage).filter(
			(k) => k.startsWith('logto:') && /accessToken/i.test(k)
		);
		for (const k of keys) localStorage.removeItem(k);
		return keys;
	});
	expect(dropped.length, 'no Logto access-token key in localStorage to drop').toBeGreaterThan(0);

	await page.reload();
	await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible({ timeout: 30_000 });
	await expect(page.locator('table').or(page.getByText('No projects yet'))).toBeVisible({
		timeout: 30_000
	});
	// A failed refresh signs out with this exact toast (08 §6 row 2).
	await expect(page.getByText('Session expired, sign in again.')).toHaveCount(0);
});

test('/settings shows the real identity and guards deletion', async ({ page }) => {
	await gotoSignedIn(page, '/settings');
	const handle = (await page.locator('#account dd.font-mono').first().innerText()).trim();
	expect(handle, 'no handle rendered on /settings').not.toBe('');

	await expect(
		page.getByText('Everything, including snapshots, is deleted 30 days later.')
	).toBeVisible();

	const button = page.getByRole('button', { name: 'Delete account' });
	await expect(button).toBeDisabled();
	const input = page.locator('#delete-account').getByRole('textbox');
	await input.fill(`${handle}x`);
	await expect(button).toBeDisabled();
	await input.fill(handle);
	await expect(button).toBeEnabled();
	// Deliberately not pressed: this is the owner's real account.
	await input.fill('');
	await expect(button).toBeDisabled();
});

test('/settings round-trips timezone, email toggle and ntfy URL', async ({ page }) => {
	await gotoSignedIn(page, '/settings');

	// Each control saves as it changes; the ntfy URL has its own Save
	// (DECISIONS I-332), and machine.nix has another further down (I-578).
	const tz = page.getByLabel('Timezone');
	const email = page.getByRole('checkbox', { name: 'Email notifications' });
	const ntfy = page.locator('#ntfy-url');
	const saveNtfy = page.locator('form', { has: ntfy }).getByRole('button', { name: 'Save' });

	const beforeTz = await tz.inputValue();
	const beforeEmail = await email.isChecked();
	const beforeNtfy = await ntfy.inputValue();

	const probeTz = beforeTz === 'Europe/Lisbon' ? 'Europe/Berlin' : 'Europe/Lisbon';
	const probeNtfy = `https://ntfy.sh/repose-live-check-${Date.now()}`;

	await tz.selectOption(probeTz);
	await expect(page.getByText(`Timezone set to ${probeTz}.`)).toBeVisible({ timeout: 15_000 });
	await email.setChecked(!beforeEmail);
	await expect(
		page.getByText(beforeEmail ? 'Email notifications off.' : 'Email notifications on.')
	).toBeVisible({ timeout: 15_000 });
	await ntfy.fill(probeNtfy);
	await saveNtfy.click();
	await expect(page.getByText('ntfy URL saved.')).toBeVisible({ timeout: 15_000 });

	await page.reload();
	await expect(tz).toHaveValue(probeTz, { timeout: 30_000 });
	await expect(ntfy).toHaveValue(probeNtfy);
	expect(await email.isChecked()).toBe(!beforeEmail);

	// Put the account back exactly as it was.
	await tz.selectOption(beforeTz);
	await expect(page.getByText(`Timezone set to ${beforeTz}.`)).toBeVisible({ timeout: 15_000 });
	await email.setChecked(beforeEmail);
	await ntfy.fill(beforeNtfy);
	await saveNtfy.click();
	await expect(page.getByText(beforeNtfy ? 'ntfy URL saved.' : 'ntfy turned off.')).toBeVisible({
		timeout: 15_000
	});
});

// The button exists to prove a channel end to end; 08 §6 requires it to
// degrade rather than break if the api has no route. Either outcome is a
// pass here, and the run's output says which happened.
test('Send test reaches POST /me/notify-test', async ({ page }) => {
	await gotoSignedIn(page, '/settings');
	const responded = page.waitForResponse((r) => r.url().includes('/me/notify-test'), {
		timeout: 30_000
	});
	await page.getByRole('button', { name: 'Send test' }).click();
	const res = await responded;
	expect([200, 404]).toContain(res.status());
	if (res.status() === 404) {
		await expect(page.getByText('Test not available yet')).toBeVisible();
	} else {
		await expect(
			page
				.getByText('Test notification sent.')
				.or(page.getByText('The test notification failed on every channel.'))
		).toBeVisible({ timeout: 15_000 });
	}
});

test('/billing renders against the real api', async ({ page }) => {
	await gotoSignedIn(page, '/billing');
	await expect(page.getByRole('heading', { name: 'Billing' })).toBeVisible();
	// DECISIONS I-289, I-604: without POLAR_ACCESS_TOKEN the api answers
	// billing_disabled and the page must say so; with it, the plan cards,
	// the full state or the subscription (08-dashboard.md §5.8).
	await expect(
		page
			.getByTestId('billing-disabled')
			.or(page.getByTestId('plan-solo'))
			.or(page.getByTestId('full'))
			.or(page.getByTestId('plan'))
	).toBeVisible({ timeout: 30_000 });
});

// Sign-out revokes the refresh token at Logto, which invalidates the saved
// state for every later run, so it is opt-in and goes last.
test('sign-out returns to the landing page and clears the session', async ({ page }) => {
	test.skip(
		process.env.REPOSE_LIVE_SIGNOUT !== '1',
		'sign-out revokes the saved session; set REPOSE_LIVE_SIGNOUT=1 and re-run live:auth afterwards'
	);
	await gotoSignedIn(page, '/projects');
	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page).toHaveURL(`${LIVE_URL}/`, { timeout: 30_000 });
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
	await page.goto('/projects');
	await expect(page).toHaveURL(`${LIVE_URL}/`, { timeout: 30_000 });
});
