// A project's webhook routes on its Config tab (DECISIONS I-1110): add,
// the table, Rotate and Remove asking in the row, and the empty state.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('a project with no routes says so, and Add route adds one', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hooks-app',
		remote_url: 'github.com/heracraft/hooks-app'
	});
	await page.goto(`/projects/${p.id}/config#webhooks`);
	const section = page.locator('#webhooks');
	await expect(section.getByRole('heading', { name: 'Webhooks' })).toBeVisible();
	await expect(section.getByText('No webhook routes yet')).toBeVisible();

	await section.getByRole('button', { name: 'Add route' }).click();
	await section.getByLabel('Name').fill('stripe');
	await section.getByLabel('Port and path in the machine').fill('3000/api/webhooks/stripe');
	await section.getByRole('button', { name: 'Add route' }).click();

	await expect(section.getByRole('heading', { name: '1 webhook route' })).toBeVisible();
	await expect(section.getByRole('cell', { name: 'stripe' }).first()).toBeVisible();
	await expect(
		section.getByRole('cell', { name: ':3000/api/webhooks/stripe', exact: true })
	).toBeVisible();
	// The URL is never printed on the page; Copy puts it on the clipboard.
	await expect(section.getByRole('button', { name: 'Copy the URL of stripe' })).toBeVisible();
	await expect(page.locator('body')).not.toContainText('hooks.repose');
});

test('a bad name is refused in the form, before anything is sent', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hooks-bad-app',
		remote_url: 'github.com/heracraft/hooks-bad-app'
	});
	await page.goto(`/projects/${p.id}/config#webhooks`);
	const section = page.locator('#webhooks');
	await section.getByRole('button', { name: 'Add route' }).click();
	await section.getByLabel('Name').fill('Stripe');
	await section.getByLabel('Port and path in the machine').fill('3000');
	await section.getByRole('button', { name: 'Add route' }).click();
	await expect(section.getByText('A name is 1 to 32 of a-z')).toBeVisible();

	await section.getByLabel('Name').fill('ok');
	await section.getByLabel('Port and path in the machine').fill('nope');
	await section.getByRole('button', { name: 'Add route' }).click();
	await expect(section.getByText('A target is a port, or a port and a path')).toBeVisible();
});

test('Rotate and Remove ask in the row and say what they cost', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hooks-rm-app',
		remote_url: 'github.com/heracraft/hooks-rm-app'
	});
	await page.goto(`/projects/${p.id}/config#webhooks`);
	const section = page.locator('#webhooks');
	await section.getByRole('button', { name: 'Add route' }).click();
	await section.getByLabel('Name').fill('github');
	await section.getByLabel('Port and path in the machine').fill('3000');
	await section.getByRole('button', { name: 'Add route' }).click();
	await expect(section.getByRole('heading', { name: '1 webhook route' })).toBeVisible();

	await section.getByRole('button', { name: 'Rotate the token of github' }).click();
	await expect(page.getByTestId('confirm-rotate-webhook')).toContainText('answers 410 for 30 days');
	await page.getByRole('button', { name: 'Keep it' }).click();
	await expect(page.getByTestId('confirm-rotate-webhook')).toHaveCount(0);

	await section.getByRole('button', { name: 'Remove github' }).click();
	await expect(page.getByTestId('confirm-remove-webhook')).toContainText('then 404');
	await page.getByRole('button', { name: 'Remove', exact: true }).click();
	await expect(section.getByText('No webhook routes yet')).toBeVisible();
});
