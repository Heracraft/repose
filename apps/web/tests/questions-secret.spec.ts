// A question an agent asked with `repose-ask --secret NAME` (DECISIONS
// I-929): the row offers "Enter NAME" in place of a field, which opens a
// dialog over a blurred page; the value is pasted there, masked, and sent
// as the project's secret, never as an answer. A notification's link
// (?answer=<id>) opens the same dialog.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addQuestion, closeQuestion } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

async function secretsOf(projectId: string): Promise<string[]> {
	const res = await fetch(`${apiURLFromEnv()}/projects/${projectId}/secrets`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	return ((await res.json()) as Array<{ name: string }>).map((s) => s.name);
}

test('Enter NAME opens the dialog; the pasted value is stored as the secret', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-secret',
		remote_url: 'github.com/heracraft/ask-secret'
	});
	const q = await addQuestion(p.id, {
		agent: 'claude',
		secret: 'STRIPE_WEBHOOK_SECRET',
		text: 'Stripe dashboard, Developers > Webhooks > Signing secret.'
	});
	await page.goto(`/projects/${p.id}`);
	const row = page.getByTestId('question');
	// No field to type the value into on the row.
	await expect(row.getByRole('textbox')).toHaveCount(0);
	await row.getByRole('button', { name: 'Enter STRIPE_WEBHOOK_SECRET' }).click();

	const dialog = page.getByRole('dialog', { name: 'STRIPE_WEBHOOK_SECRET' });
	await expect(dialog).toBeVisible();
	await expect(dialog).toContainText('claude asks for it on');
	await expect(dialog).toContainText('Signing secret.');
	const value = dialog.getByLabel('Value', { exact: true });
	await expect(value).toBeFocused();
	// The rest of the page is behind the modal.
	expect(await page.evaluate(() => document.querySelector('dialog')?.matches(':modal'))).toBe(true);

	let sent: { value?: string; answer?: string } | undefined;
	page.on('request', (r) => {
		if (r.method() === 'POST' && r.url().endsWith(`/questions/${q.id}/answer`))
			sent = r.postDataJSON();
	});
	await dialog.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(dialog.getByText('Paste the value.')).toBeVisible();

	// Spaces are kept, and said.
	await value.fill(' whsec_test_123 ');
	await expect(dialog).toContainText('16 B, starts and ends with a space');
	// The field is a password field: a screen reader does not read it out.
	await expect(value).toHaveAttribute('type', 'password');
	await value.press('Enter');
	await expect(dialog).toBeHidden();
	expect(sent?.answer).toBeUndefined();
	expect(atob(sent?.value ?? '')).toBe(' whsec_test_123 ');
	await expect(row).toContainText('Saved STRIPE_WEBHOOK_SECRET');
	expect(await secretsOf(p.id)).toContain('STRIPE_WEBHOOK_SECRET');
	// The value is nowhere in the page once it is sent.
	expect(await page.content()).not.toContain('whsec_test_123');
});

test('a notification link opens the dialog; Escape closes it without answering', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-link',
		remote_url: 'github.com/heracraft/ask-link'
	});
	const q = await addQuestion(p.id, {
		agent: 'codex',
		secret: 'OPENAI_API_KEY',
		text: 'For the embedding tests.'
	});
	await page.goto(`/projects/${p.id}?answer=${q.id}`);
	const dialog = page.getByRole('dialog', { name: 'OPENAI_API_KEY' });
	await expect(dialog).toBeVisible();
	// The query leaves the URL, so a reload does not open it again.
	await expect(page).toHaveURL(new RegExp(`/projects/${p.id}$`));
	await dialog.getByLabel('Value', { exact: true }).fill('sk-abc');
	await page.keyboard.press('Escape');
	await expect(dialog).toBeHidden();
	await expect(page.getByTestId('question')).toHaveAttribute('data-state', 'pending');
	// Reopened, the field is empty: the value did not stay in the page.
	await page.getByRole('button', { name: 'Enter OPENAI_API_KEY' }).click();
	await expect(dialog.getByLabel('Value', { exact: true })).toHaveValue('');
});

test('a pasted PEM goes in whole; a closed question offers Save anyway', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-pem',
		remote_url: 'github.com/heracraft/ask-pem'
	});
	const q = await addQuestion(p.id, {
		agent: 'claude',
		secret: 'DEPLOY_KEY',
		text: 'The deploy key.'
	});
	await page.goto(`/projects/${p.id}`);
	await page.getByRole('button', { name: 'Enter DEPLOY_KEY' }).click();
	const dialog = page.getByRole('dialog', { name: 'DEPLOY_KEY' });
	const pem = '-----BEGIN KEY-----\nMIIE\n-----END KEY-----\n';
	await dialog.getByLabel('Value', { exact: true }).evaluate((el, text) => {
		const dt = new DataTransfer();
		dt.setData('text/plain', text);
		el.dispatchEvent(
			new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })
		);
	}, pem);
	const area = dialog.getByLabel('Value', { exact: true });
	await expect(area).toHaveJSProperty('tagName', 'TEXTAREA');
	await expect(dialog).toContainText('3 lines');

	// The agent stops waiting before the save lands.
	await closeQuestion(q.id, 'asker_gone');
	let put: string | undefined;
	page.on('request', (r) => {
		if (r.method() === 'PUT' && r.url().endsWith(`/projects/${p.id}/secrets/DEPLOY_KEY`))
			put = (r.postDataJSON() as { value: string }).value;
	});
	await dialog.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(dialog).toContainText('The question is closed');
	await dialog.getByRole('button', { name: 'Save anyway' }).click();
	await expect(dialog).toBeHidden();
	expect(atob(put ?? '')).toBe(pem.replace(/\n$/, ''));
});
