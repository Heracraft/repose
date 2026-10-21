// Account secrets (DECISIONS I-697): Settings → Secrets lists, adds and
// deletes them, the form opening inline from "Add secret" (I-841); a
// project's Config tab shows the ones that reach it and marks the one its
// own replaces. Values never reach the DOM.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';
import type { Locator } from '@playwright/test';

/** Opens the inline form and fills it; Add submits. */
async function add(section: Locator, name: string, value?: string) {
	const open = section.getByRole('button', { name: 'Add secret' });
	if ((await open.getAttribute('aria-expanded')) !== 'true') await open.click();
	await expect(section.getByLabel('Name', { exact: true })).toBeFocused();
	await section.getByLabel('Name', { exact: true }).fill(name);
	if (value !== undefined) await section.getByLabel('Value', { exact: true }).fill(value);
}

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

// The fixture's one account is shared by every spec after this one, and
// billing.spec counts the memory its machines use: a project this spec
// started and left running made it read 8 GB, not 0 (CI on b2524af4).
const created: string[] = [];
test.afterAll(async () => {
	for (const id of created) {
		await fetch(`${apiURLFromEnv()}/projects/${id}`, {
			method: 'DELETE',
			headers: { Authorization: 'Bearer playwright' }
		});
	}
});

test('Settings adds and deletes an account secret; a project page shows it', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'acct-secrets-app',
		remote_url: 'github.com/heracraft/acct-secrets-app'
	});
	created.push(p.id);
	// The project has its own STRIPE_KEY.
	const res = await fetch(`${apiURLFromEnv()}/projects/${p.id}/secrets/STRIPE_KEY`, {
		method: 'PUT',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ value: btoa('own') })
	});
	expect(res.ok).toBe(true);

	// The old fragment URL lands on the tab (I-841).
	await page.goto('/settings#secrets');
	await expect(page).toHaveURL('/settings/secrets');
	const section = page.locator('#secrets');
	await expect(section.getByRole('heading', { name: 'Secrets', exact: true })).toBeVisible();
	// The form is closed until asked for.
	await expect(section.getByLabel('Name', { exact: true })).toHaveCount(0);
	for (const [name, value] of [
		['COOLIFY_BASE_URL', 'https://coolify.hunter2.example'],
		['STRIPE_KEY', 'sk_live_hunter2']
	]) {
		await add(section, name, value);
		await section.getByRole('button', { name: 'Add', exact: true }).click();
		await expect(
			section.getByTestId('account-secrets').getByText(name, { exact: true })
		).toBeVisible();
		// Stored: the form closes and Add secret has the focus back.
		await expect(section.getByLabel('Name', { exact: true })).toHaveCount(0);
		await expect(section.getByRole('button', { name: 'Add secret' })).toBeFocused();
	}
	// The toast names the projects whose own STRIPE_KEY wins (others from
	// earlier tests in this fixture may have one too).
	await expect(page.getByText(new RegExp(`${p.slug}.* own\\.$`))).toBeVisible();
	expect(await page.content()).not.toContain('hunter2');

	await page.goto(`/projects/${p.id}/config`);
	const fromAccount = page.getByTestId('account-secrets');
	await expect(fromAccount.getByRole('heading', { name: 'From your account' })).toBeVisible();
	await expect(fromAccount.getByText('COOLIFY_BASE_URL')).toBeVisible();
	const item = (name: string) => fromAccount.getByRole('listitem').filter({ hasText: name });
	await expect(item('STRIPE_KEY').getByText('This project’s own is used')).toBeVisible();
	await expect(item('COOLIFY_BASE_URL').getByText('This project’s own is used')).toHaveCount(0);
	// Its link goes to the account's list.
	await expect(fromAccount.getByRole('link', { name: 'Settings' })).toHaveAttribute(
		'href',
		'/settings/secrets'
	);

	// Delete… asks first; Keep it backs out.
	await page.goto('/settings/secrets');
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
	await expect(section.getByText('No account secrets.')).toBeVisible();
	// The last row gone, focus goes to Add secret, not <body>.
	await expect(section.getByRole('button', { name: 'Add secret' })).toBeFocused();

	await page.goto(`/projects/${p.id}/config`);
	await expect(page.locator('#secrets').getByText('STRIPE_KEY', { exact: true })).toBeVisible();
	await expect(page.getByTestId('account-secrets')).toHaveCount(0);
});

test('an account secret name is checked before it is sent', async ({ page }) => {
	await page.goto('/settings/secrets');
	const section = page.locator('#secrets');
	await add(section, 'BASH_ENV', 'x');
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(section.getByText('BASH_ENV is reserved.')).toBeVisible();
	await section.getByLabel('Name', { exact: true }).fill('MY-KEY');
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	// The rule in words, not as a regular expression (Guide B 1.3).
	await expect(
		section.getByText('Use A-Z, 0-9 and _, starting with a letter, at most 64 characters.')
	).toBeVisible();
	await expect(section.getByLabel('Name', { exact: true })).toBeFocused();
	// Cancel closes the form, cleared, and gives the focus back.
	await section.getByRole('button', { name: 'Cancel', exact: true }).click();
	await expect(section.getByLabel('Name', { exact: true })).toHaveCount(0);
	await expect(section.getByRole('button', { name: 'Add secret' })).toBeFocused();
	await section.getByRole('button', { name: 'Add secret' }).click();
	await expect(section.getByLabel('Name', { exact: true })).toHaveValue('');
});

// The Settings form takes a file as the project page does (I-698): the
// picked file's bytes are the value, and one over 64 KB is refused.
test('Settings Load from file reads the value from the picked file', async ({ page }) => {
	await page.goto('/settings/secrets');
	const section = page.locator('#secrets');
	await add(section, 'gcp_sa_json');

	const chooser = page.waitForEvent('filechooser');
	await section.getByRole('button', { name: 'Load from file' }).click();
	const bytes = Buffer.from('{"type":"service_account","k":"hunter2"}\n');
	await (await chooser).setFiles({ name: 'sa.json', mimeType: 'application/json', buffer: bytes });
	await expect(section.getByTestId('account-secret-file-name')).toHaveText('sa.json');
	await expect(section.getByLabel('Value', { exact: true })).toBeDisabled();

	const put = page.waitForRequest(
		(r) => r.method() === 'PUT' && r.url().endsWith('/me/secrets/GCP_SA_JSON')
	);
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	const body = (await put).postDataJSON() as { value: string };
	expect(body.value).toBe(bytes.toString('base64'));
	await expect(
		section.getByTestId('account-secrets').getByText('GCP_SA_JSON', { exact: true })
	).toBeVisible();
	await expect(section.getByTestId('account-secret-file-name')).toHaveCount(0);
	expect(await page.content()).not.toContain('hunter2');

	await add(section, 'BIG_ACCOUNT_FILE');
	await section.locator('input[type=file]').setInputFiles({
		name: 'big.bin',
		mimeType: 'application/octet-stream',
		buffer: Buffer.alloc(64 * 1024 + 1, 7)
	});
	await section.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(section.getByText('Secret values are limited to 64 KB.')).toBeVisible();
	await expect(section.getByText('BIG_ACCOUNT_FILE', { exact: true })).toHaveCount(0);

	// Account secrets reach every project page; leave none for later specs.
	const del = await fetch(`${apiURLFromEnv()}/me/secrets/GCP_SA_JSON`, {
		method: 'DELETE',
		headers: { Authorization: 'Bearer playwright' }
	});
	expect(del.ok).toBe(true);
});
