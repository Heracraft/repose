// Account secrets (DECISIONS I-697): Settings lists, adds and deletes
// them; a project's Secrets page shows the ones that reach it and marks
// the one its own replaces. Values never reach the DOM.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('Settings adds and deletes an account secret; a project page shows it', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'acct-secrets-app',
		remote_url: 'github.com/heracraft/acct-secrets-app'
	});
	// The project has its own STRIPE_KEY.
	const res = await fetch(`${apiURLFromEnv()}/projects/${p.id}/secrets/STRIPE_KEY`, {
		method: 'PUT',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ value: btoa('own') })
	});
	expect(res.ok).toBe(true);

	await page.goto('/settings#secrets');
	const section = page.locator('#secrets');
	await expect(section.getByRole('heading', { name: 'Secrets' })).toBeVisible();
	for (const [name, value] of [
		['COOLIFY_BASE_URL', 'https://coolify.hunter2.example'],
		['STRIPE_KEY', 'sk_live_hunter2']
	]) {
		await section.getByLabel('Name', { exact: true }).fill(name);
		await section.getByLabel('Value', { exact: true }).fill(value);
		await section.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(
			section.getByTestId('account-secrets').getByText(name, { exact: true })
		).toBeVisible();
	}
	// The toast names the projects whose own STRIPE_KEY wins (others from
	// earlier tests in this fixture may have one too).
	await expect(page.getByText(new RegExp(`${p.slug}.* own\\.$`))).toBeVisible();
	expect(await page.content()).not.toContain('hunter2');

	await page.goto(`/projects/${p.id}/secrets`);
	const fromAccount = page.getByTestId('account-secrets');
	await expect(fromAccount.getByRole('heading', { name: 'From your account' })).toBeVisible();
	await expect(fromAccount.getByText('COOLIFY_BASE_URL')).toBeVisible();
	await expect(
		fromAccount.getByRole('row', { name: /STRIPE_KEY/ }).getByText("This project's own is used")
	).toBeVisible();
	await expect(
		fromAccount
			.getByRole('row', { name: /COOLIFY_BASE_URL/ })
			.getByText("This project's own is used")
	).toHaveCount(0);

	// Delete asks first; Keep it backs out.
	await page.goto('/settings#secrets');
	await section.getByRole('button', { name: 'Delete COOLIFY_BASE_URL' }).click();
	const ask = section.getByTestId('confirm-delete-account-secret');
	await expect(ask).toContainText('from every project?');
	await expect(ask.getByRole('button', { name: 'Keep it' })).toBeFocused();
	await section.getByRole('button', { name: 'Delete COOLIFY_BASE_URL now' }).click();
	await expect(section.getByText('COOLIFY_BASE_URL', { exact: true })).toHaveCount(0);
	await expect(section.getByRole('button', { name: 'Delete STRIPE_KEY' })).toBeFocused();
	await section.getByRole('button', { name: 'Delete STRIPE_KEY' }).click();
	await section.getByRole('button', { name: 'Delete STRIPE_KEY now' }).click();
	await expect(section.getByTestId('account-secrets')).toHaveCount(0);

	await page.goto(`/projects/${p.id}/secrets`);
	await expect(page.getByText('STRIPE_KEY', { exact: true })).toBeVisible();
	await expect(page.getByTestId('account-secrets')).toHaveCount(0);
});

test('an account secret name is checked before it is sent', async ({ page }) => {
	await page.goto('/settings#secrets');
	const section = page.locator('#secrets');
	await section.getByLabel('Name', { exact: true }).fill('BASH_ENV');
	await section.getByLabel('Value', { exact: true }).fill('x');
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(section.getByText(/reserved/)).toBeVisible();
	await section.getByLabel('Name', { exact: true }).fill('MY-KEY');
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(section.getByText(/\[A-Z\]\[A-Z0-9_\]/)).toBeVisible();
});
