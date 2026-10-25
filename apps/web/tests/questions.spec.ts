// A question an agent asked with repose-ask (DECISIONS I-245) shows in
// the "Waiting on you" card on the project page and on /questions, and is
// answered there: an option button, free text, or Dismiss (I-693). A
// question closed anywhere else leaves one line saying how. The rail's
// Questions place carries the count and each project's row turns amber
// (I-841). The fake api records what the dashboard sent.
import { test, expect, type Page } from '@playwright/test';
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
	await expect(page.getByRole('heading', { name: 'Connect' })).toBeVisible();
	await expect(page.getByTestId('questions')).toHaveCount(0);

	const q = await addQuestion(p.id, {
		agent: 'claude',
		text: 'Drop the legacy sessions table?',
		options: ['yes', 'no']
	});
	await page.reload();
	const card = page.getByRole('region', { name: 'Waiting on you' });
	await expect(card).toHaveAttribute('data-testid', 'questions');
	// The count is the rail's, once a screen; the card's head has none.
	await expect(card.locator('.hk-badge')).toHaveCount(0);
	const row = card.getByTestId('question');
	await expect(row).toContainText('claude');
	await expect(row).toContainText('asked just now');
	await expect(row.getByText('Drop the legacy sessions table?')).toBeVisible();
	// Short enough for 3 lines: no Show all.
	await expect(row.getByRole('button', { name: 'Show all' })).toHaveCount(0);
	await expect(row.getByRole('button', { name: 'no', exact: true })).toBeVisible();
	await row.getByRole('button', { name: 'yes', exact: true }).click();

	// The row collapses to one line: the question, muted, and the answer.
	// An answer from the dashboard does not say so (review r1 dash-r1-8).
	await expect(row.getByTestId('question-outcome')).toHaveText('Answered yes');
	await expect(row).toContainText('Drop the legacy sessions table?');
	await expect(card.getByRole('status')).toHaveText('Answered yes');
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
	await expect(first.getByTestId('question-outcome')).toHaveText('Answered 4173');
	await expect(first).toContainText('Which port should the dev server use?');
	// The keyboard goes on to the next question.
	const second = card.locator(`[data-qid="${q2.id}"]`);
	await expect(second.getByLabel('Your answer')).toBeFocused();

	const dismiss = second.getByRole('button', { name: 'Dismiss the question from pi' });
	await expect(dismiss).toHaveAttribute('title', 'The agent stops waiting');
	await expect(dismiss).toHaveText('Dismiss');
	await dismiss.click();
	await expect(second.getByTestId('question-outcome')).toHaveText('Dismissed');

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
	await expect(
		card.getByTestId('question').filter({ hasText: 'terminal' }).getByTestId('question-outcome')
	).toHaveText('Answered in the terminal');
	await expect(
		card.getByTestId('question').filter({ hasText: 'Withdrawn' }).getByTestId('question-outcome')
	).toHaveText('Withdrawn by the agent');
	await page.clock.runFor(8_500);
	await expect(page.getByTestId('questions')).toHaveCount(0);
});

test('a question the api closed with no source does not say you dismissed it', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-gone-app',
		remote_url: 'github.com/heracraft/ask-gone-app'
	});
	const q = await addQuestion(p.id, { agent: 'claude', text: 'Still there?' });
	await page.clock.install();
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	await expect(card.getByTestId('question')).toHaveCount(1);
	// asker_gone, interrupted and a machine stop are a plain cancel (I-690).
	await closeQuestion(q.id, 'asker_gone');
	await page.clock.runFor(10_500);
	await expect(card.getByTestId('question').getByTestId('question-outcome')).toHaveText(
		'The agent stopped waiting'
	);
});

test('a row closed elsewhere holds its place and focus until its line arrives', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-hold-app',
		remote_url: 'github.com/heracraft/ask-hold-app'
	});
	const q1 = await addQuestion(p.id, { agent: 'claude', text: 'Which port?' });
	const q2 = await addQuestion(p.id, { agent: 'codex', text: 'Which branch?' });
	await page.clock.install();
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	await expect(card.getByTestId('question')).toHaveCount(2);
	const field = card.locator(`[data-qid="${q2.id}"] textarea`);
	await field.fill('main');
	await expect(field).toBeFocused();

	// Hold the card's lookup of how the question closed.
	let release!: () => void;
	const held = new Promise<void>((r) => (release = r));
	let asked = false;
	await page.route(
		(u) => u.pathname.endsWith(`/projects/${p.id}/questions`) && !u.search,
		async (route) => {
			asked = true;
			await held;
			await route.continue();
		}
	);
	await closeQuestion(q2.id, 'terminal');
	await page.clock.runFor(10_500);
	await expect.poll(() => asked).toBe(true);
	// Still drawn as it was, the draft and the focus with it.
	await expect(card.getByTestId('question')).toHaveCount(2);
	await expect(card.locator(`[data-qid="${q2.id}"]`)).toHaveAttribute('data-state', 'pending');
	await expect(field).toHaveValue('main');
	await expect(field).toBeFocused();

	release();
	await expect(card.locator(`[data-qid="${q2.id}"]`).getByTestId('question-outcome')).toHaveText(
		'Answered in the terminal'
	);
	await expect(card.locator(`[data-qid="${q1.id}"] textarea`)).toBeFocused();
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

test('the fold names the window only where its row does', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'ask-fold-app',
		remote_url: 'github.com/heracraft/ask-fold-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'First?' });
	await addQuestion(p.id, { agent: 'claude', text: 'Second?' });
	// The fake names the window after the agent; give these one of their own.
	await page.route(
		(u) => u.pathname.endsWith(`/projects/${p.id}/questions`),
		async (route) => {
			const res = await route.fetch();
			const body = await res.json();
			for (const q of body.questions) q.window = 'shell';
			await route.fulfill({ response: res, json: body });
		}
	);
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	const row = card.getByTestId('question');
	const fold = card.getByText(/earlier question from/);
	await expect(row).toContainText('shell');
	await expect(fold).toHaveText(/^1 earlier question from claude\s·\sshell$/);

	await page.setViewportSize({ width: 390, height: 844 });
	await expect(row.getByText('shell')).toBeHidden();
	await expect(fold.getByText(/shell/)).toBeHidden();
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
	// A turn under way is marked as working (its pulsing dot); an agent
	// that is only running is not.
	await expect(running.locator('[data-agent-state="working"]')).toHaveCount(0);
	await expect(
		agents
			.getByRole('listitem')
			.filter({ hasText: 'claude' })
			.locator('[data-agent-state="working"]')
	).toHaveText('working');
	await expect(
		agents.getByRole('listitem').filter({ hasText: 'claude' }).locator('.hk-dot--busy')
	).toHaveCount(1);
	await expect(agents.getByRole('listitem').filter({ hasText: 'newagent' })).toContainText(
		'thinking hard'
	);
});

// Two lines a question (I-837): the question, then the agent and its age
// with the answer on the same line from 768px; options that fit sit at the
// end of the question's own line.
test('a short question with options answers on its own line at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	const p = await createProject(apiURLFromEnv(), {
		name: 'q-line-app',
		remote_url: 'github.com/heracraft/q-line-app'
	});
	await addQuestion(p.id, { agent: 'claude', text: 'Ship it?', options: ['yes', 'no'] });
	await addQuestion(p.id, { agent: 'codex', text: 'Which branch?' });
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('questions');
	const short = card.getByTestId('question').filter({ hasText: 'Ship it?' });
	const text = await short.getByText('Ship it?').boundingBox();
	const yes = await short.getByRole('button', { name: 'yes', exact: true }).boundingBox();
	if (!text || !yes) throw new Error('no question parts');
	expect(Math.abs(yes.y + yes.height / 2 - (text.y + text.height / 2))).toBeLessThan(8);
	expect(yes.x).toBeGreaterThan(text.x + text.width);

	const free = card.getByTestId('question').filter({ hasText: 'Which branch?' });
	const meta = await free.getByText('codex', { exact: true }).boundingBox();
	const field = await free.getByLabel('Your answer').boundingBox();
	if (!meta || !field) throw new Error('no free-text parts');
	expect(Math.abs(field.y + field.height / 2 - (meta.y + meta.height / 2))).toBeLessThan(8);
	// Two rows take at most 180px at 1440 (spec 2.4).
	const rows = await card
		.getByTestId('question')
		.evaluateAll((els) => els.reduce((n, el) => n + el.getBoundingClientRect().height, 0));
	expect(rows).toBeLessThanOrEqual(180);
});

/** Narrows the account's pending list to these questions; the suite shares one account. */
async function onlyPending(page: Page, ids: string[]) {
	await page.route(/\/questions\?state=pending$/, async (route) => {
		const res = await route.fetch();
		const body = (await res.json()) as { questions: { id: string }[] };
		body.questions = body.questions.filter((q) => ids.includes(q.id));
		await route.fulfill({ response: res, json: body });
	});
}

// The count is on Questions, not Projects; the title says it to a
// background tab; the asking project's rail row turns amber (I-841).
test('the rail counts questions on Questions and marks the asking project', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'rail-ask-app',
		remote_url: 'github.com/heracraft/rail-ask-app'
	});
	const q1 = await addQuestion(p.id, {
		agent: 'claude',
		text: 'Rail one?',
		options: ['yes', 'no']
	});
	const q2 = await addQuestion(p.id, { agent: 'codex', text: 'Rail two?' });
	await onlyPending(page, [q1.id, q2.id]);
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/projects');
	const nav = page.getByRole('navigation', { name: 'Dashboard' });
	const questions = nav.getByRole('link', { name: 'Questions, 2 waiting' });
	await expect(questions).toHaveAttribute('href', '/questions');
	await expect(questions.getByTestId('pending-questions')).toHaveText('2');
	await expect(nav.getByRole('link', { name: 'Projects', exact: true })).toBeVisible();
	await expect(page).toHaveTitle('(2) Projects · repose');
	const rowLink = nav.getByRole('link', { name: `${p.name}, running, waiting on you` });
	// The rail lists 8 projects; an asking one sorts to the top.
	await expect(rowLink).toBeVisible();
	await expect(rowLink.locator('[data-tone="warn"]')).toHaveCount(1);

	await questions.click();
	await expect(page).toHaveURL('/questions');
	await expect(page.getByRole('heading', { level: 1, name: 'Questions' })).toBeVisible();
	await expect(page).toHaveTitle('(2) Questions · repose');
	await expect(questions).toHaveAttribute('aria-current', 'page');
	await closeQuestion(q1.id);
	await closeQuestion(q2.id);
});

// /questions: every waiting question under its project's name, count and
// state, answered in place; the answer then shows under Answered (I-841).
test('/questions groups by project and answers in place', async ({ page }) => {
	const api = apiURLFromEnv();
	const a = await createProject(api, { name: 'qpage-a', remote_url: 'github.com/h/qpage-a' });
	const b = await createProject(api, { name: 'qpage-b', remote_url: 'github.com/h/qpage-b' });
	const qa1 = await addQuestion(a.id, {
		agent: 'claude',
		text: 'Merge the docs PR?',
		options: ['yes', 'no']
	});
	const qa2 = await addQuestion(a.id, { agent: 'codex', text: 'Which test runner?' });
	const qb = await addQuestion(b.id, {
		agent: 'pi',
		text: 'Keep the cache dir?',
		options: ['keep', 'drop']
	});
	await onlyPending(page, [qa1.id, qa2.id, qb.id]);
	await page.goto('/questions');
	const card = page.getByTestId('questions');
	await expect(card.getByRole('heading', { name: 'Waiting on you' })).toBeVisible();
	await expect(card.getByTestId('question')).toHaveCount(3);
	// One head a project: its link, its count and its machine's state.
	// (Which leads is layoutQuestions', unit-tested in question-view.test.ts.)
	const heads = card.locator('.waiting-project');
	await expect(heads).toHaveCount(2);
	const headA = heads.filter({ has: page.getByRole('link', { name: a.name, exact: true }) });
	await expect(headA).toContainText('2 questions');
	await expect(headA).toContainText('running');
	await expect(headA.getByRole('link')).toHaveAttribute('href', `/projects/${a.id}`);
	const headB = heads.filter({ has: page.getByRole('link', { name: b.name, exact: true }) });
	await expect(headB).toContainText('1 question');

	const row = card.locator(`[data-qid="${qb.id}"]`);
	await row.getByRole('button', { name: 'keep', exact: true }).click();
	await expect(row.getByTestId('question-outcome')).toHaveText('Answered keep');
	const answered = page.getByRole('region', { name: 'Answered' });
	await expect(answered).toContainText('Last 7 days');
	const done = answered.locator(`[data-qid="${qb.id}"]`);
	await expect(done.getByTestId('answered-outcome')).toHaveText('keep');
	await expect(done).toContainText('Keep the cache dir?');
	await expect(done.getByRole('link', { name: b.name })).toHaveAttribute(
		'href',
		`/projects/${b.id}`
	);
	await closeQuestion(qa1.id);
	await closeQuestion(qa2.id);
});

// Answered words each outcome as the closing line does, with where the
// answer came from, for the last 7 days only.
test('Answered names each outcome and where it came from', async ({ page }) => {
	const now = Date.now();
	const mk = (i: number, over: Record<string, unknown>) => ({
		id: `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`,
		project_id: '00000000-0000-4000-9000-000000000001',
		project: 'alpha',
		agent: 'claude',
		window: 'claude',
		text: `Closed question ${i}?`,
		options: ['yes', 'no'],
		state: 'answered',
		answer: 'yes',
		answered_via: 'email',
		created_at: new Date(now - (i + 5) * 60_000).toISOString(),
		expires_at: new Date(now + 3600_000).toISOString(),
		answered_at: new Date(now - i * 60_000).toISOString(),
		...over
	});
	const all = [
		mk(1, {}),
		mk(2, { answered_via: 'terminal', answer: null }),
		mk(3, { state: 'cancelled', answer: null, answered_via: 'dashboard' }),
		mk(4, { answered_at: new Date(now - 8 * 86_400_000).toISOString() })
	];
	await page.route(/\/questions\?state=pending$/, (route) =>
		route.fulfill({ json: { questions: [] } })
	);
	await page.route(/\/questions\?state=all$/, (route) =>
		route.fulfill({ json: { questions: all } })
	);
	await page.goto('/questions');
	await expect(page.getByTestId('questions-empty')).toHaveText('No questions waiting.');
	await expect(page).toHaveTitle('Questions · repose');
	const outcomes = page.getByRole('region', { name: 'Answered' }).getByTestId('answered-outcome');
	await expect(outcomes).toHaveText(['yes · by email', 'Answered in the terminal', 'Dismissed']);
	// A project not in the account's list keeps its name, unlinked.
	await expect(
		page.getByRole('region', { name: 'Answered' }).getByRole('link', { name: 'alpha' })
	).toHaveCount(0);
});
