// The project page's Events card shows pages of 25, newest first, with
// Newest, Newer and Older, the position from the api's count, and a filter
// by kind; the page lives in the URL, so Back and a reload keep it
// (DECISIONS I-662, which replaced I-414's Show older).
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addEvents, addQuestion } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

function eventsCard(page: Page) {
	const card = page.getByTestId('events-card');
	const top = card.getByRole('navigation', { name: 'Event pages' });
	return {
		card,
		rows: card.getByTestId('event-row'),
		position: card.getByTestId('events-position'),
		newest: top.getByRole('button', { name: 'Newest' }),
		newer: top.getByRole('button', { name: 'Newer' }),
		older: top.getByRole('button', { name: 'Older' })
	};
}

test('pages of 25 with Older, Newer and Newest, kept in the URL', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'busy-app',
		remote_url: 'github.com/heracraft/busy-app'
	});
	// 75 events and the project's own two (created, running), the oldest.
	await addEvents(p.id, 75);
	await page.goto(`/projects/${p.id}`);
	const ev = eventsCard(page);
	await expect(ev.rows).toHaveCount(25);
	await expect(ev.position).toHaveText('Page 1 of 4');
	await expect(ev.card.getByTestId('events-total')).toHaveText('77 events');
	await expect(ev.rows.first()).toContainText('e074');
	await expect(ev.rows.last()).toContainText('e050');
	await expect(ev.newer).toHaveAttribute('aria-disabled', 'true');

	await ev.older.click();
	await expect(ev.position).toHaveText('Page 2 of 4');
	await expect(ev.rows.first()).toContainText('e049');
	await ev.older.click();
	await expect(ev.position).toHaveText('Page 3 of 4');
	await expect(ev.rows.first()).toContainText('e024');
	await expect(ev.rows.last()).toContainText('e000');
	// The list takes focus, named for its page.
	await expect(page.getByRole('list', { name: 'Events, page 3' })).toBeFocused();

	await ev.newer.click();
	await expect(ev.position).toHaveText('Page 2 of 4');
	await expect(ev.rows.first()).toContainText('e049');
	// Newer goes by the after cursor.
	await expect(page).toHaveURL(/events_after=.*events_page=2/);
	await page.reload();
	await expect(ev.position).toHaveText('Page 2 of 4');
	await expect(ev.rows.first()).toContainText('e049');

	// Back steps to the page before.
	await ev.older.click();
	await expect(ev.position).toHaveText('Page 3 of 4');
	await page.goBack();
	await expect(ev.position).toHaveText('Page 2 of 4');

	await ev.newest.click();
	await expect(ev.position).toHaveText('Page 1 of 4');
	await expect(ev.rows.first()).toContainText('e074');
	await expect(page).not.toHaveURL(/events_/);
});

test('the last page ends with the project created and has no Older', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'deep-app',
		remote_url: 'github.com/heracraft/deep-app'
	});
	await addEvents(p.id, 75);
	await page.goto(`/projects/${p.id}`);
	const ev = eventsCard(page);
	for (const n of [2, 3, 4]) {
		await ev.older.click();
		await expect(ev.position).toHaveText(`Page ${n} of 4`);
	}
	await expect(ev.rows).toHaveCount(2);
	await expect(ev.rows.last()).toHaveAttribute('data-kind', 'project.created');
	await expect(ev.older).toHaveAttribute('aria-disabled', 'true');
	await expect(ev.older).toHaveAttribute('title', 'No older events');
	// Newer from the last page goes by the after cursor.
	await ev.newer.click();
	await expect(ev.position).toHaveText('Page 3 of 4');
	await expect(page).toHaveURL(/events_after=/);
	await expect(ev.rows.first()).toContainText('e024');
});

test('the Errors filter shows only errors', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'mixed-app',
		remote_url: 'github.com/heracraft/mixed-app'
	});
	await addEvents(p.id, 30, {
		kinds: ['agent_message', 'error', 'completed'],
		agents: ['claude', 'codex'],
		summaries: ['working', 'tests failed', 'done']
	});
	await page.goto(`/projects/${p.id}`);
	const ev = eventsCard(page);
	await ev.card.getByRole('link', { name: 'Errors' }).click();
	await expect(ev.card.getByRole('link', { name: 'Errors' })).toHaveAttribute(
		'aria-current',
		'page'
	);
	await expect(page).toHaveURL(/events_kind=errors/);
	await expect(ev.rows).toHaveCount(10);
	for (const kind of await ev.rows.evaluateAll((els) => els.map((e) => e.dataset.kind))) {
		expect(kind).toBe('error');
	}
	await expect(ev.position).toHaveText('Page 1 of 1');
	await expect(ev.card.getByTestId('events-total')).toHaveText('10 events');
});

test('a project with few events has one page', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'calm-app',
		remote_url: 'github.com/heracraft/calm-app'
	});
	await addEvents(p.id, 5);
	await page.goto(`/projects/${p.id}`);
	const ev = eventsCard(page);
	await expect(ev.rows).toHaveCount(7);
	await expect(ev.position).toHaveText('Page 1 of 1');
	await expect(ev.older).toHaveAttribute('aria-disabled', 'true');
	await expect(ev.card.getByRole('group', { name: /end of list/ })).toHaveCount(0);
});

// The page paints its cards from one first load, so nothing lands above
// what is on screen later: the questions card used to mount a few hundred
// ms in and push the page down.
test('the project page holds still while it loads, at 390', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	const p = await createProject(apiURLFromEnv(), {
		name: 'still-app',
		remote_url: 'github.com/heracraft/still-app'
	});
	await addEvents(p.id, 40);
	await addQuestion(p.id, { agent: 'claude', text: 'Ship it?', options: ['yes', 'no'] });
	await page.addInitScript(() => {
		(window as unknown as { __cls: number }).__cls = 0;
		new PerformanceObserver((list) => {
			for (const e of list.getEntries() as unknown as {
				value: number;
				hadRecentInput: boolean;
			}[]) {
				if (!e.hadRecentInput) (window as unknown as { __cls: number }).__cls += e.value;
			}
		}).observe({ type: 'layout-shift', buffered: true });
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('questions')).toBeVisible();
	await expect(eventsCard(page).rows).toHaveCount(25);
	await page.waitForTimeout(1500);
	const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
	expect(cls).toBeLessThanOrEqual(0.05);
});
