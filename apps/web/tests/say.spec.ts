// A message for a running agent (`repose say`, DECISIONS I-1144): the
// Agents card on a project's Overview opens a field under the agent's own
// row and sends to that row's window, and the line it prints is the
// machine's answer (delivered, waiting, replaced). The api passes the
// machine's own refusal through where it names the windows, and that
// sentence names a CLI flag, so a refusal here is said in the dashboard's
// words ($lib/say.ts). The fake api records what the dashboard sent.
import { test, expect, type Page } from '@playwright/test';
import {
	apiURLFromEnv,
	createProject,
	fail,
	says,
	setAgents,
	setSay,
	signIn,
	unfail
} from './helpers';

/** The fake's say defaults: one window `claude`, answered `delivered`. */
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

const card = (page: Page) => page.getByTestId('agents-card');

test('an agent that needs input takes a message, and the fake got the window', async ({ page }) => {
	const p = await withAgents('say-needs-input', [
		{ agent: 'claude', window: 'claude', state: 'needs_input' }
	]);
	await page.goto(`/projects/${p.id}`);
	await expect(card(page)).toBeVisible();
	await expect(card(page)).toContainText('needs input');

	// The field opens in place of the row's own button and takes the focus.
	await card(page).getByRole('button', { name: 'Say something to claude' }).click();
	const field = card(page).getByLabel('Message for claude');
	await expect(field).toBeFocused();
	await field.fill('run the tests before you commit');

	// Send waits with aria-disabled, never disabled: a disabled element
	// drops focus to <body> (I-391, I-766).
	const send = card(page).getByRole('button', { name: 'Send' });
	await expect(send).not.toBeDisabled();
	await expect(send).not.toHaveAttribute('aria-disabled', 'true');
	await send.click();

	await expect(page.getByText('Delivered to claude.')).toBeVisible();
	// The panel closes and the keyboard goes back to the button that opened it.
	await expect(card(page).getByTestId('say-field')).toHaveCount(0);
	await expect(card(page).getByRole('button', { name: 'Say something to claude' })).toBeFocused();

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent).toHaveLength(1);
	expect(sent[0]).toMatchObject({ window: 'claude', text: 'run the tests before you commit' });
});

test('an agent mid-turn gets it at the end of the turn, and a second replaces the first', async ({
	page
}) => {
	const p = await withAgents('say-waiting', [
		{ agent: 'claude', window: 'claude', state: 'working' }
	]);
	await setSay({ agents: ['claude'], state: 'waiting' });
	await page.goto(`/projects/${p.id}`);
	await expect(card(page)).toBeVisible();

	await card(page).getByRole('button', { name: 'Say something to claude' }).click();
	await card(page).getByLabel('Message for claude').fill('stop after this file');
	// Enter sends; Shift+Enter breaks a line (the question field's rule).
	await card(page).getByLabel('Message for claude').press('Enter');
	await expect(
		page.getByText('claude is mid-turn. It gets the message when the turn ends.')
	).toBeVisible();

	await setSay({ agents: ['claude'], state: 'waiting', replaced: true });
	await card(page).getByRole('button', { name: 'Say something to claude' }).click();
	await card(page).getByLabel('Message for claude').fill('on second thought, keep going');
	await card(page).getByRole('button', { name: 'Send' }).click();
	await expect(
		page.getByText(
			'claude is mid-turn. It gets this message at the end of the turn, in place of the one that was waiting.'
		)
	).toBeVisible();

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent.map((s) => s.text)).toEqual([
		'stop after this file',
		'on second thought, keep going'
	]);
});

test('two agents give a field each, and the one under codex-2 sends to codex-2', async ({
	page
}) => {
	const p = await withAgents('say-two-agents', [
		{ agent: 'claude', window: 'claude', state: 'idle' },
		{ agent: 'codex', window: 'codex-2', state: 'needs_input' }
	]);
	await setSay({ agents: ['claude', 'codex-2'] });
	await page.goto(`/projects/${p.id}`);
	await expect(card(page)).toBeVisible();
	await expect(card(page).getByRole('button', { name: /^Say something to/ })).toHaveCount(2);

	await card(page).getByRole('button', { name: 'Say something to codex-2' }).click();
	// One field at a time: the card is 18rem in the two-column Overview.
	await expect(card(page).getByTestId('say-field')).toHaveCount(1);
	await expect(card(page).getByTestId('say-field')).toHaveAttribute('data-say-window', 'codex-2');
	await card(page).getByLabel('Message for codex-2').fill('yes, allow it');
	await card(page).getByRole('button', { name: 'Send' }).click();
	await expect(page.getByText('Delivered to codex-2.')).toBeVisible();

	const sent = (await says()).filter((s) => s.project_id === p.id);
	expect(sent).toHaveLength(1);
	expect(sent[0]).toMatchObject({ window: 'codex-2', text: 'yes, allow it' });
});

test("a refusal is said in the dashboard's words, not the machine's", async ({ page }) => {
	const p = await withAgents('say-no-agent', [
		{ agent: 'claude', window: 'claude', state: 'needs_input' }
	]);
	// The agent went while the page held its signals: the api answers
	// detail.reason no_agent with the machine's own sentence.
	await setSay({ agents: [] });
	await page.goto(`/projects/${p.id}`);
	await expect(card(page)).toBeVisible();

	await card(page).getByRole('button', { name: 'Say something to claude' }).click();
	await card(page).getByLabel('Message for claude').fill('are you there');
	await card(page).getByRole('button', { name: 'Send' }).click();

	await expect(page.getByText('That agent is no longer running on this machine.')).toBeVisible();
	await expect(page.getByText('no agent is running on this machine')).toHaveCount(0);
	await expect(page.locator('body')).not.toContainText('--agent');
	// The panel stays open with what was typed, so nothing is retyped.
	await expect(card(page).getByLabel('Message for claude')).toHaveValue('are you there');

	// Cancel closes it and gives the focus back.
	await card(page).getByRole('button', { name: 'Cancel' }).click();
	await expect(card(page).getByRole('button', { name: 'Say something to claude' })).toBeFocused();
});

test('a failed send is one toast and the panel stays open', async ({ page }) => {
	const p = await withAgents('say-failed', [
		{ agent: 'claude', window: 'claude', state: 'needs_input' }
	]);
	await page.goto(`/projects/${p.id}`);
	await expect(card(page)).toBeVisible();

	await fail('POST', '/projects/:id/say', 'internal');
	try {
		await card(page).getByRole('button', { name: 'Say something to claude' }).click();
		await card(page).getByLabel('Message for claude').fill('anybody home');
		await card(page).getByRole('button', { name: 'Send' }).click();
		await expect(page.getByText('Could not send the message.')).toBeVisible();
		await expect(page.getByText('Could not send the message.')).toHaveCount(1);
		await expect(card(page).getByLabel('Message for claude')).toHaveValue('anybody home');
	} finally {
		await unfail('POST', '/projects/:id/say');
	}
	expect((await says()).filter((s) => s.project_id === p.id)).toHaveLength(0);
});

test('a machine with no agent has no Agents card and no field', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'say-no-agents',
		remote_url: 'github.com/heracraft/say-no-agents'
	});
	await setAgents(p.id, []);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('details-card')).toBeVisible();
	await expect(card(page)).toHaveCount(0);
	await expect(page.getByTestId('say-field')).toHaveCount(0);
});

// The field is a control a thumb hits, so at 390 on a touch screen it
// measures 44px like every other one (Guide B 1.7). Its one-line height
// cancels the kit's coarse-pointer minimum, so the narrow width restores
// it; design.spec's sweep cannot catch this one, because the field does
// not exist until Say is pressed.
test.describe('the field at 390 on a touch screen', () => {
	test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

	test('the field a thumb types into is 44px tall', async ({ page }) => {
		const p = await withAgents('say-touch', [
			{ agent: 'claude', window: 'claude', state: 'needs_input' }
		]);
		await page.goto(`/projects/${p.id}`);
		await card(page).getByRole('button', { name: /Say something to claude/ }).click();
		const field = card(page).getByTestId('say-field').locator('textarea');
		await expect(field).toBeVisible();
		const h = await field.evaluate((el) => el.getBoundingClientRect().height);
		expect(h).toBeGreaterThanOrEqual(43.5);
	});
});
