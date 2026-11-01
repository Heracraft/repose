// An agent's `--link URL --label TEXT` (DECISIONS I-931): the dashboard
// draws it under the event and under the question, named by its label and
// its host, opening in a new tab.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addEvents, addQuestion } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('the link shows under the event and the question, with its host', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'link-app',
		remote_url: 'github.com/heracraft/link-app'
	});
	await addEvents(p.id, 1, {
		kinds: ['agent_done'],
		agents: ['claude'],
		summaries: ['Simple resume done.'],
		link: 'http://localhost:8742/out/compare-simple.html',
		linkLabel: 'Compare resumes'
	});
	await addQuestion(p.id, {
		agent: 'codex',
		text: 'Merge it?',
		options: ['yes', 'no'],
		link: 'https://github.com/acme/app/pull/12',
		link_label: 'PR 12'
	});
	await page.goto(`/projects/${p.id}/events`);
	const ev = page.getByTestId('event-row').filter({ hasText: 'Simple resume done.' });
	const a = ev.getByRole('link', { name: 'Compare resumes · localhost:8742' });
	await expect(a).toHaveAttribute('href', 'http://localhost:8742/out/compare-simple.html');
	await expect(a).toHaveAttribute('target', '_blank');
	await expect(a).toHaveAttribute('rel', 'noopener noreferrer');

	await page.goto(`/projects/${p.id}`);
	const q = page.getByTestId('question').filter({ hasText: 'Merge it?' });
	await expect(q.getByRole('link', { name: 'PR 12 · github.com' })).toHaveAttribute(
		'href',
		'https://github.com/acme/app/pull/12'
	);
	// Recent activity on /projects, and the Answered list once it is answered.
	await page.goto('/projects');
	await expect(
		page.getByRole('link', { name: 'Compare resumes · localhost:8742' }).first()
	).toBeVisible();
	await page.goto(`/projects/${p.id}`);
	await q.getByRole('button', { name: 'yes', exact: true }).click();
	await page.goto('/questions');
	await expect(
		page
			.getByTestId('answered-row')
			.filter({ hasText: 'Merge it?' })
			.getByRole('link', { name: 'PR 12 · github.com' })
	).toBeVisible();
});
