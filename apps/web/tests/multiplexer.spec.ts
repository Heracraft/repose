// DECISIONS I-502, I-509: a herdr project's page names herdr beside the
// size, read only, and has no tmux clients row; a tmux project's page is
// as it was. The fake api refuses herdr until its base gate opens, so the
// project's answer is rewritten on its way to the page.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

async function asHerdr(page: Page, id: string, multiplexer: 'herdr' | 'tmux') {
	await page.route(
		(url) => url.pathname.endsWith(`/v1/projects/${id}`),
		async (route) => {
			if (route.request().method() !== 'GET') return route.continue();
			const res = await route.fetch();
			const body = await res.json();
			body.multiplexer = multiplexer;
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

for (const m of ['herdr', 'tmux'] as const) {
	test(`project page on ${m}`, async ({ page }) => {
		const p = await createProject(apiURLFromEnv(), {
			name: `mux-${m}`,
			remote_url: `github.com/heracraft/mux-${m}`
		});
		await asHerdr(page, p.id, m);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByText('SSH sessions')).toBeVisible();
		if (m === 'herdr') {
			await expect(page.getByText('herdr', { exact: true })).toBeVisible();
			await expect(page.getByText('tmux clients')).toHaveCount(0);
		} else {
			await expect(page.getByText('herdr', { exact: true })).toHaveCount(0);
			await expect(page.getByText('tmux clients')).toBeVisible();
		}
		const dir = process.env.REPOSE_SHOTS_DIR;
		if (m === 'herdr' && dir) {
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
