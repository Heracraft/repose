// An agent outside any terminal repose can see (DECISIONS I-1676, I-1678,
// ENG-255): Claude Desktop's "Open in Claude" session. The Agents card
// lists it with "no terminal" where a window goes and its state; the
// message box leaves it out of its targets, and says why when it is the
// only agent. AR_SHOTS=<dir> also writes the captures the change was
// judged by, at 1x, 1440 and 390 wide, light and dark.
import { test, expect, type Page } from '@playwright/test';
import { addEvents, apiURLFromEnv, createProject, setAgents, signIn } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

async function project(name: string, agents: Parameters<typeof setAgents>[1]) {
	const p = await createProject(apiURLFromEnv(), {
		name,
		remote_url: `github.com/heracraft/${name}`
	});
	await setAgents(p.id, agents);
	return p;
}

const desktop = {
	agent: 'claude',
	window: '',
	state: 'working',
	agent_id: 'a3e81b07c4d29f560',
	terminal: ''
};

async function shoot(page: Page, name: string) {
	const dir = process.env.AR_SHOTS;
	if (!dir) return;
	for (const scheme of ['light', 'dark'] as const) {
		for (const width of [1440, 390]) {
			await page.emulateMedia({ colorScheme: scheme });
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			const card = page.getByTestId('agents-card');
			await card.scrollIntoViewIfNeeded();
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.emulateMedia({ colorScheme: 'light' });
	await page.setViewportSize({ width: 1280, height: 720 });
}

test('a Desktop session shows on the Agents card with its state and no terminal', async ({
	page
}) => {
	const p = await project('nt-desktop', [desktop]);
	// Desktop's turn ended once already; guestd relays it under the
	// agent's name, so the thread has something to draw.
	await addEvents(p.id, 1, {
		kinds: ['completed'],
		agents: ['claude'],
		windows: ['claude'],
		summaries: ['Fixed the flaky checkout test.']
	});
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('agents-card');
	await expect(card).toContainText('claude');
	await expect(card.getByTestId('agent-no-terminal')).toHaveText('no terminal');
	await expect(card.locator('[data-agent-state="working"]')).toHaveText('working');
	// The box can't type into it, and says why rather than "No agent".
	const field = page.getByTestId('conversation').locator('textarea');
	await expect(field).toBeDisabled();
	await expect(field).toHaveAttribute(
		'placeholder',
		'claude runs outside a terminal repose can type into.'
	);
	await expect(page.getByTestId('conversation')).not.toContainText('No agent is running');
	await shoot(page, 'desktop-alone');
});

test('beside a tmux agent, the box goes to the tmux one and the card lists both', async ({
	page
}) => {
	const p = await project('nt-mixed', [
		{
			agent: 'claude',
			window: 'claude',
			state: 'idle',
			agent_id: 'a5f0c2e19b7d4a630',
			terminal: 'tmux:claude'
		},
		{ ...desktop, state: 'idle' }
	]);
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('agents-card');
	await expect(card.locator('li')).toHaveCount(2);
	await expect(card.getByTestId('agent-no-terminal')).toHaveCount(1);
	const convo = page.getByTestId('conversation');
	await expect(convo.getByLabel('Message for claude')).toBeEnabled();
	await expect(convo.getByLabel('To')).toHaveCount(0);
	await shoot(page, 'desktop-mixed');
});

test('an api that sends no agent_id reads as before', async ({ page }) => {
	const p = await project('nt-old', [{ agent: 'claude', window: 'claude-2', state: 'working' }]);
	await page.goto(`/projects/${p.id}`);
	const card = page.getByTestId('agents-card');
	await expect(card).toContainText('claude-2');
	await expect(card.getByTestId('agent-no-terminal')).toHaveCount(0);
});
