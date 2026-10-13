// A question an agent asked with repose-ask (DECISIONS I-245) shows in
// the "Waiting on you" card on the project page and is answered there: an
// option button, free text, or Dismiss (I-693). A question closed
// anywhere else leaves one line saying how. The fake api records what the
// dashboard sent.
import { test, expect } from '@playwright/test';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	addQuestion,
	closeQuestion,
	setAgents
} from './helpers';

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
	const card = page.getByRole('region', { name: 'Waiting on you' });
	await expect(card).toHaveAttribute('data-testid', 'questions');
	await expect(card.getByText('1', { exact: true })).toBeVisible();
	const row = card.getByTestId('question');
	await expect(row).toContainText('claude');
	await expect(row).toContainText('asked just now');
	await expect(row.getByText('Drop the legacy sessions table?')).toBeVisible();
	// Short enough for 3 lines: no Show all.
	await expect(row.getByRole('button', { name: 'Show all' })).toHaveCount(0);
	await expect(row.getByRole('button', { name: 'no', exact: true })).toBeVisible();
	await row.getByRole('button', { name: 'yes', exact: true }).click();

	// The row collapses to one line naming the answer and where it came from.
	await expect(row).toHaveText('Answered yes from the dashboard');
	await expect(card.getByRole('status')).toHaveText('Answered yes from the dashboard');
	await expect(row.getByRole('button')).toHaveCount(0);
	// The keyboard goes to the card's title, the last question gone.
	await expect(card.getByRole('heading', { name: 'Waiting on you' })).toBeFocused();
	const got = (await questionsOf(p.id)).find((x) => x.id === q.id);
	expect(got).toMatchObject({ state: 'answered', answer: 'yes', answered_via: 'dashboard' });
	// Then it leaves, and the card with it.
	await expect(page.getByTestId('questions')).toHaveCount(0, { timeout: 12_000 });
});

test('a free-text question takes a typed answer; Dismiss dismisses', async ({ page }) => {
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

	const first = card.locator(`[data-qid="${q1.id}"]`);
	const field = first.getByLabel('Your answer');
	await expect(first.getByRole('button', { name: 'Send' })).toBeDisabled();
	// Shift+Enter breaks a line; Enter sends.
	await field.fill('4173');
	await field.press('Shift+Enter');
	await expect(field).toHaveValue('4173\n');
	await field.press('Backspace');
	await field.press('Enter');
	await expect(first).toHaveText('Answered 4173 from the dashboard');
	// The keyboard goes on to the next question.
	const second = card.locator(`[data-qid="${q2.id}"]`);
	await expect(second.getByLabel('Your answer')).toBeFocused();

	const dismiss = second.getByRole('button', { name: 'Dismiss the question from pi' });
	await expect(dismiss).toHaveAttribute('title', 'The agent stops waiting');
	await expect(dismiss).toHaveText('Dismiss');
	await dismiss.click();
	await expect(second).toHaveText('Dismissed');

	const all = await questionsOf(p.id);
	expect(all.find((x) => x.id === q1.id)).toMatchObject({
		state: 'answered',
		answer: '4173',
		answered_via: 'dashboard'
	});
	expect(all.find((x) => x.id === q2.id)?.state).toBe('cancelled');
});

test('a question answered in the terminal says so, then leaves', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-term-app',
		remote_url: 'github.com/heracraft/ask-term-app'
	});
	const q1 = await addQuestion(p.id, { agent: 'claude', text: 'Which branch?' });
	const q2 = await addQuestion(p.id, {
		agent: 'codex',
		text: 'Keep the cache?',
		options: ['yes', 'no']
	});
	await page.clock.install();
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	await expect(card.getByTestId('question')).toHaveCount(2);

	await closeQuestion(q1.id, 'terminal');
	await closeQuestion(q2.id, 'withdrawn');
	// The card's poll finds both gone and asks the api how they closed.
	await page.clock.runFor(10_500);
	await expect(card.getByTestId('question').filter({ hasText: 'terminal' })).toHaveText(
		'Answered in the terminal'
	);
	await expect(card.getByTestId('question').filter({ hasText: 'Withdrawn' })).toHaveText(
		'Withdrawn by the agent'
	);
	await expect(card.getByText('2', { exact: true })).toHaveCount(0);
	await page.clock.runFor(8_500);
	await expect(page.getByTestId('questions')).toHaveCount(0);
});

test('a long question is clamped with Show all; older ones from one window fold', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-long-app',
		remote_url: 'github.com/heracraft/ask-long-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'An older question?' });
	const long = Array.from({ length: 12 }, (_, i) => `Sentence ${i + 1} of a long question.`).join(
		' '
	);
	await addQuestion(p.id, { agent: 'claude', text: long, options: ['go', 'wait'] });
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	// Two waiting, one row: the older is folded under the newer.
	await expect(card.getByRole('heading', { name: 'Waiting on you' })).toBeVisible();
	await expect(card.getByTestId('question')).toHaveCount(1);
	await expect(card).toContainText('1 earlier question from claude');

	const row = card.getByTestId('question');
	const text = row.getByText(/^Sentence 1 /);
	const clampedHeight = (await text.boundingBox())?.height ?? 0;
	expect(clampedHeight).toBeLessThanOrEqual(3 * 24 + 1);
	await row.getByRole('button', { name: 'Show all' }).click();
	expect((await text.boundingBox())?.height ?? 0).toBeGreaterThan(clampedHeight);
	await expect(row.getByRole('button', { name: 'Show less' })).toHaveAttribute(
		'aria-expanded',
		'true'
	);

	await card.getByRole('button', { name: 'Show', exact: true }).click();
	await expect(card.getByTestId('question')).toHaveCount(2);
	await expect(card.getByTestId('question').nth(1)).toContainText('An older question?');
});

test('the expiry shows only in its last 10 minutes, in its own words', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-expiry-app',
		remote_url: 'github.com/heracraft/ask-expiry-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'Soon?', timeout_s: 9 * 60 });
	await addQuestion(p.id, { agent: 'codex', text: 'Later?', timeout_s: 3600 });
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	await expect(card.getByTestId('question').filter({ hasText: 'Soon?' })).toContainText(
		'expires in 9m'
	);
	await expect(card.getByTestId('question').filter({ hasText: 'Later?' })).not.toContainText(
		'expires'
	);
});

test('the Agents card shows running for a heuristic-only agent and tolerates an unknown state', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'agents-running-app',
		remote_url: 'github.com/heracraft/agents-running-app'
	});
	await setAgents(p.id, [
		{ agent: 'aider', window: 'aider', state: 'running' },
		{ agent: 'claude', window: 'main', state: 'working' },
		{ agent: 'newagent', window: 'x', state: 'thinking_hard' }
	]);
	await page.goto(`/projects/${p.id}`);
	const agents = page.getByTestId('agents-card');
	const running = agents.getByRole('listitem').filter({ hasText: 'aider' });
	await expect(running.getByText('running', { exact: true })).toBeVisible();
	await expect(running.locator('.badge--new')).toHaveCount(0);
	await expect(
		agents.getByRole('listitem').filter({ hasText: 'claude' }).locator('.badge--new')
	).toHaveText('working');
	await expect(agents.getByRole('listitem').filter({ hasText: 'newagent' })).toContainText(
		'thinking hard'
	);
});
