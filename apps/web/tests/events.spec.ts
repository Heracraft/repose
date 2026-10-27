// A project's Events tab shows pages of 10, newest first, with Newest,
// Newer and Older, the position from the api's count, and a filter by
// kind; the page lives in the URL, so Back and a reload keep it
// (DECISIONS I-662, which replaced I-414's Show older; the tab is I-841).
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
		pager: top,
		rows: card.getByTestId('event-row'),
		position: card.getByTestId('events-position'),
		newest: top.getByRole('button', { name: 'Newest' }),
		newer: top.getByRole('button', { name: 'Newer' }),
		older: top.getByRole('button', { name: 'Older' })
	};
}

test('pages of 10 with Older, Newer and Newest, kept in the URL', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'busy-app',
		remote_url: 'github.com/heracraft/busy-app'
	});
	// 25 events and the project's own two (created, running), the oldest.
	await addEvents(p.id, 25);
	await page.goto(`/projects/${p.id}/events`);
	const ev = eventsCard(page);
	await expect(ev.rows).toHaveCount(10);
	await expect(ev.pager).toBeVisible();
	await expect(ev.position).toHaveText('Page 1 of 3');
	await expect(ev.card.getByTestId('events-total')).toHaveText('27 events');
	await expect(ev.rows.first()).toContainText('e024');
	await expect(ev.rows.last()).toContainText('e015');
	await expect(ev.newer).toHaveAttribute('aria-disabled', 'true');

	await ev.older.click();
	await expect(ev.position).toHaveText('Page 2 of 3');
	await expect(ev.rows.first()).toContainText('e014');
	await ev.older.click();
	await expect(ev.position).toHaveText('Page 3 of 3');
	await expect(ev.rows.first()).toContainText('e004');
	await expect(ev.rows).toHaveCount(7);
	// The list takes focus, named for its page.
	await expect(page.getByRole('list', { name: 'Events, page 3' })).toBeFocused();

	await ev.newer.click();
	await expect(ev.position).toHaveText('Page 2 of 3');
	await expect(ev.rows.first()).toContainText('e014');
	// Newer goes by the after cursor.
	await expect(page).toHaveURL(/events_after=.*events_page=2/);
	await page.reload();
	await expect(ev.position).toHaveText('Page 2 of 3');
	await expect(ev.rows.first()).toContainText('e014');

	// Back steps to the page before.
	await ev.older.click();
	await expect(ev.position).toHaveText('Page 3 of 3');
	await page.goBack();
	await expect(ev.position).toHaveText('Page 2 of 3');

	await ev.newest.click();
	await expect(ev.position).toHaveText('Page 1 of 3');
	await expect(ev.rows.first()).toContainText('e024');
	await expect(page).not.toHaveURL(/events_/);
	await expect(page).toHaveURL(`/projects/${p.id}/events`);
});

test('the last page ends with the project created and has no Older', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'deep-app',
		remote_url: 'github.com/heracraft/deep-app'
	});
	await addEvents(p.id, 25);
	await page.goto(`/projects/${p.id}/events`);
	const ev = eventsCard(page);
	for (const n of [2, 3]) {
		await ev.older.click();
		await expect(ev.position).toHaveText(`Page ${n} of 3`);
	}
	await expect(ev.rows).toHaveCount(7);
	await expect(ev.rows.last()).toHaveAttribute('data-kind', 'project.created');
	await expect(ev.older).toHaveAttribute('aria-disabled', 'true');
	await expect(ev.older).toHaveAttribute('title', 'No older events');
	// Newer from the last page goes by the after cursor.
	await ev.newer.click();
	await expect(ev.position).toHaveText('Page 2 of 3');
	await expect(page).toHaveURL(/events_after=/);
	await expect(ev.rows.first()).toContainText('e014');
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
	await page.goto(`/projects/${p.id}/events`);
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
	// One page of errors: no pager, the count says it (I-680).
	await expect(ev.pager).toHaveCount(0);
	await expect(ev.card.getByTestId('events-total')).toHaveText('10 events');
});

// A stop that failed reaches the user by itself since `repose stop`
// returns early (I-710): it is an error, and the Errors filter finds it.
test('a failed stop is an error', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'stop-app',
		remote_url: 'github.com/heracraft/stop-app'
	});
	await addEvents(p.id, 4, {
		kinds: ['stop_failed', 'agent_message'],
		agents: ['', 'claude'],
		summaries: ['stopping stop-app failed: the host did not answer', 'working']
	});
	await page.goto(`/projects/${p.id}/events`);
	const ev = eventsCard(page);
	await ev.card.getByRole('link', { name: 'Errors' }).click();
	await expect(page).toHaveURL(/events_kind=errors/);
	await expect(ev.rows).toHaveCount(2);
	for (const kind of await ev.rows.evaluateAll((els) => els.map((e) => e.dataset.kind))) {
		expect(kind).toBe('stop_failed');
	}
	await expect(ev.rows.first()).toContainText('failed');
});

test('a project with few events has one page', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'calm-app',
		remote_url: 'github.com/heracraft/calm-app'
	});
	await addEvents(p.id, 5);
	await page.goto(`/projects/${p.id}/events`);
	const ev = eventsCard(page);
	await expect(ev.rows).toHaveCount(7);
	// One page has no pager; the count beside the title is enough (I-680).
	await expect(ev.card.getByTestId('events-total')).toHaveText('7 events');
	await expect(ev.pager).toHaveCount(0);
	await expect(ev.card.getByRole('group', { name: /end of list/ })).toHaveCount(0);
});

// The Overview paints its sections from one first load, so nothing lands
// above what is on screen later: the questions card used to mount a few
// hundred ms in and push the page down.
test('the project Overview holds still while it loads, at 390', async ({ page }) => {
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
	await expect(page.getByTestId('latest-events').getByTestId('event-row')).toHaveCount(5);
	await page.waitForTimeout(1500);
	const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
	expect(cls).toBeLessThanOrEqual(0.05);
});

// The same on the Events tab: the header and the tabs hold still while
// the page of events arrives under them.
test('the Events tab holds still while it loads, at 390', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	const p = await createProject(apiURLFromEnv(), {
		name: 'still-events',
		remote_url: 'github.com/heracraft/still-events'
	});
	await addEvents(p.id, 40);
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
	await page.goto(`/projects/${p.id}/events`);
	await expect(eventsCard(page).rows).toHaveCount(10);
	await page.waitForTimeout(1500);
	const cls = await page.evaluate(() => (window as unknown as { __cls: number }).__cls);
	expect(cls).toBeLessThanOrEqual(0.05);
});

// An error's banner on the Overview links to the Errors filter.
test('the last-error banner links to the errors on the Events tab', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'errors-link',
		remote_url: 'github.com/heracraft/errors-link'
	});
	await page.route(`**/v1/projects/${p.id}`, async (route) => {
		if (route.request().method() !== 'GET') return route.fallback();
		const res = await route.fetch();
		const json = await res.json();
		await route.fulfill({
			response: res,
			json: { ...json, state: 'error', last_error: 'boot_failed: the machine did not boot.' }
		});
	});
	await page.goto(`/projects/${p.id}`);
	await page.getByTestId('last-error').getByRole('link', { name: 'Errors' }).click();
	await expect(page).toHaveURL(`/projects/${p.id}/events?events_kind=errors`);
	await expect(
		page.getByTestId('events-card').getByRole('link', { name: 'Errors' })
	).toHaveAttribute('aria-current', 'page');
});

// The old project URLs with an events_* key land on the tab, keys kept
// (I-662 links in docs and bookmarks; lib/legacyUrls.ts).
test('an old events link lands on the Events tab at its page', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'old-link',
		remote_url: 'github.com/heracraft/old-link'
	});
	await addEvents(p.id, 25);
	await page.goto(`/projects/${p.id}?events_kind=errors`);
	await expect(page).toHaveURL(`/projects/${p.id}/events?events_kind=errors`);
	await expect(page.getByTestId('events-total')).toHaveText(/^\d+ events?$|^Events$/);
});
