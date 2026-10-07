// DECISIONS I-502, I-509: the stored multiplexer is the next start's, so
// a project page names herdr beside the size only while the machine is
// stopped (a running one may still run tmux until it stops), and the
// tmux clients row is always there. The fake api refuses herdr until its
// base gate opens, so the project's answer is rewritten on its way to the
// page.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

async function asHerdr(
	page: Page,
	id: string,
	multiplexer: 'herdr' | 'tmux',
	state: 'running' | 'stopped'
) {
	await page.route(
		(url) => url.pathname.endsWith(`/v1/projects/${id}`),
		async (route) => {
			if (route.request().method() !== 'GET') return route.continue();
			const res = await route.fetch();
			const body = await res.json();
			body.multiplexer = multiplexer;
			body.state = state;
			body.signals = {
				ssh_sessions: 1,
				tmux_clients: 0,
				docker: 0,
				agents: [{ agent: 'claude', window: 'claude', state: 'working' }]
			};
			await route.fulfill({ response: res, json: body });
		}
	);
}

const cases = [
	{ m: 'herdr', state: 'running', label: false },
	{ m: 'herdr', state: 'stopped', label: true },
	{ m: 'tmux', state: 'running', label: false }
] as const;

for (const { m, state, label } of cases) {
	test(`project page on ${m}, ${state}`, async ({ page }) => {
		const p = await createProject(apiURLFromEnv(), {
			name: `mux-${m}-${state}`,
			remote_url: `github.com/heracraft/mux-${m}-${state}`
		});
		await asHerdr(page, p.id, m, state);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByText('SSH sessions')).toBeVisible();
		if (label) {
			await expect(page.getByText('herdr', { exact: true })).toBeVisible();
		} else {
			await expect(page.getByText('herdr', { exact: true })).toHaveCount(0);
		}
		await expect(page.getByText('tmux clients')).toBeVisible();
		const dir = process.env.REPOSE_SHOTS_DIR;
		if (label && dir) {
			for (const width of [1440, 390]) {
				for (const scheme of ['light', 'dark'] as const) {
					await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
					await page.emulateMedia({ colorScheme: scheme });
					await page.screenshot({ path: `${dir}/herdr-project-${width}-${scheme}.png` });
				}
			}
		}
	});
}
