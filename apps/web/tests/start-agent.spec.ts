// Starting an agent from the dashboard (DECISIONS I-1702, ENG-226, ENG-250):
// a machine made here starts the project's agent, the form asks which when
// the account has no machine to go by, and a running machine's page has
// Start agent, with Claude Code's Remote Control as an option.
import { test, expect, type Page } from '@playwright/test';
import { signIn, apiURLFromEnv, createProject, destroyProject, pruneProjects } from './helpers';

const litter: string[] = [];

test.beforeAll(async () => {
	await pruneProjects(apiURLFromEnv());
});

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test.afterAll(async () => {
	const api = apiURLFromEnv();
	while (litter.length) await destroyProject(api, litter.pop()!);
});

async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await page.emulateMedia({ colorScheme: null });
}

test('with no machine to go by, New project asks which agent and the machine starts it', async ({
	page
}) => {
	await page.goto('/projects');
	const form = page.getByTestId('new-machine-form');
	await expect(form).toBeVisible();
	const agent = form.getByLabel('Agent');
	await expect(agent).toHaveValue('claude');
	await shots(page, 'new-project-asks-agent');
	await agent.selectOption('codex');
	await expect(form.getByTestId('nm-summary')).toContainText('starts Codex');
	// Remote Control is Claude Code's, so it is offered for it alone.
	await form.getByRole('button', { name: 'Options' }).click();
	await expect(form.getByTestId('nm-remote-control')).toHaveCount(0);
	await form.getByLabel('Name').press('Enter');
	await page.waitForURL(/\/projects\/[^/]+$/);
	litter.push(page.url().split('/').pop()!);
	await expect(page.getByTestId('agents-card')).toContainText('codex');
});

test('a running machine with no agent has Start agent, and a second start says it runs', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'start-agent',
		remote_url: 'github.com/heracraft/start-agent',
		class: 'small'
	});
	litter.push(p.id);
	await page.goto(`/projects/${p.id}`);
	const start = page.getByTestId('start-agent');
	await expect(start).toContainText('No agent is running.');
	await expect(start.getByLabel('Agent')).toHaveValue('claude');
	await expect(start.getByRole('checkbox', { name: /Remote Control/ })).toBeVisible();
	await shots(page, 'start-agent-none');
	await start.getByRole('checkbox', { name: /Remote Control/ }).check();
	await start.getByRole('button', { name: 'Start agent' }).click();
	await expect(page.getByTestId('agents-card')).toContainText('claude');
	// Under the agents now, without a second heading.
	const under = page.getByTestId('start-agent');
	await expect(under).not.toContainText('No agent is running.');
	await under.getByRole('button', { name: 'Start agent' }).click();
	await expect(under.getByTestId('start-agent-line')).toHaveText(
		'Claude Code is already running here.'
	);
	await under.getByLabel('Agent').selectOption('pi');
	await expect(under.getByRole('checkbox', { name: /Remote Control/ })).toHaveCount(0);
	await under.getByRole('button', { name: 'Start agent' }).click();
	await expect(page.getByTestId('agents-card')).toContainText('pi');
	await shots(page, 'start-agent-two');
});

test('a stopped machine offers no Start agent', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'stopped-agent',
		remote_url: 'github.com/heracraft/stopped-agent',
		class: 'small'
	});
	litter.push(p.id);
	await fetch(`${api}/projects/${p.id}/stop`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: '{}'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeVisible();
	await expect(page.getByTestId('start-agent')).toHaveCount(0);
});
