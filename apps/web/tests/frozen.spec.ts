// The frozen look (DECISIONS I-683; its frost, the I-841 amendment of
// 2026-10-21) is a mock behind a flag: the dev server shows it, a
// production build never does, even when an api sends `resting`. This
// suite runs the production build, so it guards that.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, setResting } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('a production build shows no frozen machine, even when the api says resting', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'resting-app',
		remote_url: 'github.com/heracraft/resting-app'
	});
	await setResting(p.id, 30);
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: p.name, exact: true })).toBeVisible();
	await expect(page.locator('[data-frozen]')).toHaveCount(0);
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-state')).toHaveText('running');
	await expect(page.getByTestId('frozen')).toHaveCount(0);
	// Nor its frost (Frost.svelte): not on the head, the Machine section or a row.
	await expect(page.locator('.hk-frost, [data-frost-on]')).toHaveCount(0);
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: p.name, exact: true })).toBeVisible();
	await expect(page.locator('.hk-frost, [data-frost-on]')).toHaveCount(0);
	await setResting(p.id, -1);
});
