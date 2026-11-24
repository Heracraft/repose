// Messages on a project's Overview (DECISIONS I-1604, amending I-1144 and
// I-1220): what the owner sent and what the agents said back, oldest
// first, and the field for the next message under them. A message goes
// to the agent's own terminal as `repose say` sends it, and the line
// under it says what became of it. The api passes the machine's own
// refusal through where it names the windows, and that sentence names a
// CLI flag, so a refusal is said in the dashboard's words ($lib/say.ts).
import { test, expect, type Page } from '@playwright/test';
import {
	addEvents,
	apiURLFromEnv,
	createProject,
	fail,
	says,
	setAgents,
	setSay,
	signIn,
	stopProject,
	unfail
} from './helpers';

/** The fake's say defaults: one window `claude`, answered `delivered`, no reply. */
async function resetSay() {
	await setSay({ agents: ['claude'], state: 'delivered', replaced: false });
}

test.beforeEach(async ({ page }) => {
	await resetSay();
	await signIn(page);
});

test.afterEach(async () => {
	await resetSay();
});

/**
 * A running project whose signals report these agents. The say handler
 * keeps its own window list, so a test that moves the agents moves both
 * (helpers.setSay).
 */
async function withAgents(
	name: string,
	agents: { agent: string; window: string; state: string }[]
) {
	const p = await createProject(apiURLFromEnv(), {
		name,
		remote_url: `github.com/heracraft/${name}`
	});
	await setAgents(p.id, agents);
	return p;
}

const convo = (page: Page) => page.getByTestId('conversation');
const thread = (page: Page) => convo(page).getByRole('list', { name: 'Messages' });

test('a message goes to the agent, stays in the thread, and the reply lands under it', async ({
	page
}) => {
	const p = await withAgents('say-thread', [{ agent: 'claude', window: 'claude', state: 'idle' }]);
	// The agent ended a turn before the page opened: its last message is
	// the first thing in the thread.
	await addEvents(p.id, 1, {
		kinds: ['completed'],
		agents: ['claude'],
		windows: ['claude'],
		summaries: ['All 42 tests pass.']
	});
	await setSay({
		agents: ['claude'],
		state: 'delivered',
		reply: 'Squashed them into one commit.',
		replyMs: 500
	});
	await page.goto(`/projects/${p.id}`);
	await expect(thread(page).getByRole('listitem')).toHaveCount(1);
	await expect(thread(page)).toContainText('All 42 tests pass.');

	// One agent: the field is for it, named on the field and under it.
	const field = convo(page).getByLabel('Message for claude');
	await expect(field).toHaveAttribute('placeholder', 'Message claude');
	await expect(convo(page)).toContainText('claude · idle');

	// Send waits with aria-disabled, never disabled: a disabled element
	// drops focus to <body> (I-391, I-766).
	const send = convo(page).getByRole('button', { name: 'Send' });
	await expect(send).toHaveAttribute('aria-disabled', 'true');
	await field.fill('also squash the fixup commits');
	await expect(send).not.toHaveAttribute('aria-disabled', 'true');
	// Enter sends; Shift+Enter breaks a line.
	await field.press('Enter');

	const mine = thread(page).locator('[data-from="you"]');
	await expect(mine).toHaveCount(1);
	await expect(mine).toContainText('also squash the fixup commits');
	await expect(mine).toContainText('Delivered to claude');
	await expect(field).toHaveValue('');
	await expect(field).toBeFocused();

	// The reply is read without a reload, after the message.
	const items = thread(page).getByRole('listitem');
	await expect(items).toHaveCount(3, { timeout: 10_000 });
	await expect(items.nth(2)).toContainText('Squashed them into one commit.');

	// And it is all still there after a reload: the api holds both halves.
	await page.reload();
	await expect(thread(page).getByRole('listitem')).toHaveCount(3);

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent).toHaveLength(1);
	expect(sent[0]).toMatchObject({ window: 'claude', text: 'also squash the fixup commits' });
});

test('an agent mid-turn gets it at the end of the turn, and a second replaces the first', async ({
	page
}) => {
	const p = await withAgents('say-waiting', [
		{ agent: 'claude', window: 'claude', state: 'working' }
	]);
	await setSay({ agents: ['claude'], state: 'waiting' });
	await page.goto(`/projects/${p.id}`);
	const field = convo(page).getByLabel('Message for claude');

	await field.fill('stop after this file');
	await field.press('Enter');
	const mine = thread(page).locator('[data-from="you"]');
	await expect(mine.nth(0)).toContainText('Waiting for claude to finish its turn');
	await expect(convo(page)).toContainText('claude is working');

	await setSay({ agents: ['claude'], state: 'waiting', replaced: true });
	await field.fill('on second thought, keep going');
	await convo(page).getByRole('button', { name: 'Send' }).click();
	await expect(mine).toHaveCount(2);
	await expect(mine.nth(0)).toContainText('Replaced by a later message');
	await expect(mine.nth(1)).toContainText('Waiting for claude to finish its turn');

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent.map((s) => [s.text, s.state])).toEqual([
		['stop after this file', 'replaced'],
		['on second thought, keep going', 'waiting']
	]);
});

test('two agents: the field goes to the one picked, else the one that spoke last', async ({
	page
}) => {
	const p = await withAgents('say-two-agents', [
		{ agent: 'claude', window: 'claude', state: 'idle' },
		{ agent: 'codex', window: 'codex-2', state: 'needs_input' }
	]);
	await setSay({ agents: ['claude', 'codex-2'] });
	await addEvents(p.id, 1, {
		kinds: ['needs_input'],
		agents: ['codex'],
		windows: ['codex-2'],
		summaries: ['codex wants to run npm install']
	});
	await page.goto(`/projects/${p.id}`);

	const to = convo(page).getByLabel('To');
	await expect(to).toHaveValue('codex-2');
	await expect(to.locator('option')).toHaveText(['claude · idle', 'codex-2 · needs input']);
	await expect(thread(page)).toContainText('needs input');
	await convo(page).getByLabel('Message for codex-2').fill('yes, allow it');
	await convo(page).getByRole('button', { name: 'Send' }).click();
	await expect(thread(page).locator('[data-from="you"]')).toContainText('Delivered to codex-2');

	await to.selectOption('claude');
	await convo(page).getByLabel('Message for claude').fill('and you, rebase on main');
	await convo(page).getByRole('button', { name: 'Send' }).click();
	await expect(thread(page).locator('[data-from="you"]').nth(1)).toContainText(
		'Delivered to claude'
	);

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent.map((s) => [s.window, s.text])).toEqual([
		['codex-2', 'yes, allow it'],
		['claude', 'and you, rebase on main']
	]);
});

test("a refusal is said in the dashboard's words and the text comes back", async ({ page }) => {
	const p = await withAgents('say-no-agent', [
		{ agent: 'claude', window: 'claude', state: 'needs_input' }
	]);
	// The agent went while the page held its signals: the api answers
	// detail.reason no_agent with the machine's own sentence.
	await setSay({ agents: [] });
	await page.goto(`/projects/${p.id}`);

	const field = convo(page).getByLabel('Message for claude');
	await field.fill('are you there');
	await field.press('Enter');

	await expect(convo(page).getByTestId('convo-error')).toHaveText(
		'That agent is no longer running on this machine.'
	);
	await expect(page.locator('body')).not.toContainText('--agent');
	// Nothing typed is lost, and nothing claims to have been sent.
	await expect(field).toHaveValue('are you there');
	await expect(thread(page)).toHaveCount(0);
});

test('a failed send says so once, under the field, and keeps the text', async ({ page }) => {
	const p = await withAgents('say-failed', [
		{ agent: 'claude', window: 'claude', state: 'needs_input' }
	]);
	await page.goto(`/projects/${p.id}`);

	await fail('POST', '/projects/:id/say', 'internal');
	try {
		await convo(page).getByLabel('Message for claude').fill('anybody home');
		await convo(page).getByRole('button', { name: 'Send' }).click();
		await expect(convo(page).getByTestId('convo-error')).toContainText(
			'Could not send the message.'
		);
		await expect(page.getByText('Could not send the message.')).toHaveCount(1);
		await expect(convo(page).getByLabel('Message for claude')).toHaveValue('anybody home');
	} finally {
		await unfail('POST', '/projects/:id/say');
	}
	expect((await says()).filter((s) => s.project_id === p.id)).toHaveLength(0);
});

test('a machine that never ran an agent has no Messages; a stopped one keeps its thread', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'say-no-agents',
		remote_url: 'github.com/heracraft/say-no-agents'
	});
	await setAgents(p.id, []);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('details-card')).toBeVisible();
	await expect(convo(page)).toHaveCount(0);

	await addEvents(p.id, 1, {
		kinds: ['agent_done'],
		agents: ['claude'],
		windows: ['claude'],
		summaries: ['Refactor finished; PR #12 is open.']
	});
	await stopProject(apiURLFromEnv(), p.id);
	await page.reload();
	await expect(thread(page)).toContainText('Refactor finished; PR #12 is open.');
	const field = convo(page).getByRole('textbox');
	await expect(field).toBeDisabled();
	await expect(field).toHaveAttribute('placeholder', 'The machine is not running.');
});

// The field is a control a thumb hits, so at 390 on a touch screen it
// measures 44px like every other one (Guide B 1.7).
test.describe('the field at 390 on a touch screen', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('the field a thumb types into is 44px tall, and so is Send', async ({ page }) => {
		const p = await withAgents('say-touch', [
			{ agent: 'claude', window: 'claude', state: 'needs_input' }
		]);
		await page.goto(`/projects/${p.id}`);
		const field = convo(page).getByLabel('Message for claude');
		await expect(field).toBeVisible();
		const h = await field.evaluate((el) => el.getBoundingClientRect().height);
		expect(h).toBeGreaterThanOrEqual(43.5);
		const send = await convo(page)
			.getByRole('button', { name: 'Send' })
			.evaluate((el) => el.getBoundingClientRect().height);
		expect(send).toBeGreaterThanOrEqual(43.5);
	});
});
