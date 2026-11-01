// A question an agent asked with `repose-ask --secret NAME` (DECISIONS
// I-929): the row offers "Enter NAME" in place of a field, which opens a
// dialog over a blurred page; the value is pasted there, masked, and sent
// as the project's secret, never as an answer. A notification's link
// (?answer=<id>) opens the same dialog.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addQuestion } from './helpers';

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
	await expect(dialog).toContainText('claude asks for a secret on');
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
	await dialog.getByRole('button', { name: 'Save STRIPE_WEBHOOK_SECRET' }).click();
	await expect(dialog.getByText('Paste the value.')).toBeVisible();

	// A copied value's trailing newline is dropped; nothing else is.
	await value.fill(' whsec_test_123 \n');
	await expect(dialog).toContainText('16 B');
	await value.press('Control+Enter');
	await expect(dialog).toBeHidden();
	expect(sent?.answer).toBeUndefined();
	expect(atob(sent?.value ?? '')).toBe(' whsec_test_123 ');
	await expect(row).toContainText('Set STRIPE_WEBHOOK_SECRET');
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
