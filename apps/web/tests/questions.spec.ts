// A question an agent asked with repose-ask (DECISIONS I-245) shows on the
// project page and is answered there: an option button, free text, or
// Cancel question. The fake api records what the dashboard sent.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addQuestion } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

async function questionsOf(projectId: string) {
	const res = await fetch(`${apiURLFromEnv()}/projects/${projectId}/questions`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	expect(res.ok).toBe(true);
	return (await res.json()).questions as Array<{
		id: string;
		state: string;
		answer: string | null;
		answered_via: string | null;
	}>;
}

test('no card without a pending question; an option button answers', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-app',
		remote_url: 'github.com/heracraft/ask-app'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible();
	await expect(page.getByTestId('questions')).toHaveCount(0);

	const q = await addQuestion(p.id, {
		agent: 'claude',
		text: 'Drop the legacy sessions table?',
		options: ['yes', 'no']
	});
	await page.reload();
	const card = page.getByTestId('questions');
	await expect(card).toBeVisible();
	await expect(card.getByText('claude asks')).toBeVisible();
	await expect(card.getByText('Drop the legacy sessions table?')).toBeVisible();
	await expect(card.getByRole('button', { name: 'no', exact: true })).toBeVisible();
	await card.getByRole('button', { name: 'yes', exact: true }).click();

	await expect(card.getByText('Answered: yes')).toBeVisible();
	await expect(card.getByTestId('question')).toHaveCount(0);
	const got = (await questionsOf(p.id)).find((x) => x.id === q.id);
	expect(got).toMatchObject({ state: 'answered', answer: 'yes', answered_via: 'dashboard' });
});

test('a free-text question takes a typed answer; Cancel question cancels', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-text-app',
		remote_url: 'github.com/heracraft/ask-text-app'
	});
	const q1 = await addQuestion(p.id, {
		agent: 'codex',
		text: 'Which port should the dev server use?'
	});
	const q2 = await addQuestion(p.id, { agent: 'pi', text: 'Anything else before I push?' });
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	await expect(card.getByTestId('question')).toHaveCount(2);

	const first = card.getByTestId('question').filter({ hasText: 'Which port' });
	await first.getByLabel('Your answer').fill('4173');
	await first.getByRole('button', { name: 'Answer' }).click();
	await expect(card.getByText('Answered: 4173')).toBeVisible();

	const second = card.getByTestId('question').filter({ hasText: 'Anything else' });
	await expect(second.getByRole('button', { name: /Cancel question/ })).toHaveAttribute(
		'title',
		'The agent stops waiting'
	);
	await second.getByRole('button', { name: /Cancel question/ }).click();
	await expect(card.getByTestId('question')).toHaveCount(0);

	const all = await questionsOf(p.id);
	expect(all.find((x) => x.id === q1.id)).toMatchObject({
		state: 'answered',
		answer: '4173',
		answered_via: 'dashboard'
	});
	expect(all.find((x) => x.id === q2.id)?.state).toBe('cancelled');
});
