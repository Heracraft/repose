// Open in Claude (DECISIONS I-1606, I-1610): the Connect card's link to
// Claude Desktop's documented `claude://code/new` with the machine's ssh
// alias and its checkout. A running machine, frozen or not, opens at once;
// a stopped one starts first and opens once it reads running.
import { test, expect, type Page } from '@playwright/test';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	destroyProject,
	setResting,
	stopProject,
	failNext
} from './helpers';

const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };

/** Tells the fake the checkout's name, as the CLI's run and code do. */
async function setCheckout(id: string, checkout: string): Promise<void> {
	const res = await fetch(`${apiURLFromEnv()}/projects/${id}`, {
		method: 'PATCH',
		headers: auth,
		body: JSON.stringify({ checkout })
	});
	if (!res.ok) throw new Error(`setCheckout: ${res.status} ${await res.text()}`);
}

/**
 * What happened, in order: each start request, each read of the project
 * that came back running, and each claude:// link the page followed. A
 * followed link is recorded and not opened: Chromium has no handler for
 * the scheme here.
 */
async function journal(page: Page, id: string): Promise<string[]> {
	const seen: string[] = [];
	await page.exposeFunction('__oicOpened', (href: string) => seen.push(`open ${href}`));
	await page.addInitScript(() => {
		document.addEventListener(
			'click',
			(e) => {
				const a = (e.target as Element | null)?.closest?.('a');
				if (a && a.href.startsWith('claude:')) {
					e.preventDefault();
					(window as unknown as { __oicOpened: (h: string) => void }).__oicOpened(a.href);
				}
			},
			true
		);
	});
	page.on('request', (r) => {
		if (r.method() === 'POST' && r.url().endsWith(`/projects/${id}/start`)) seen.push('start');
	});
	page.on('response', async (r) => {
		if (r.request().method() !== 'GET' || !r.url().endsWith(`/projects/${id}`)) return;
		// The slot is taken when the answer arrives, so the order holds
		// while its body is read.
		const at = seen.push('read') - 1;
		try {
			const body = (await r.json()) as { state?: string };
			seen[at] = body.state === 'running' ? 'running' : 'read';
		} catch {
			// not JSON
		}
	});
	return seen;
}

/** The Connect card at 1440 and 390, both schemes, at 1x (CLAUDE.md). */
async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			const card = page.locator('section[aria-labelledby="connect-title"]');
			await card.scrollIntoViewIfNeeded();
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
			await card.screenshot({ path: `${dir}/${name}-card-${width}-${scheme}.png` });
		}
	}
	await page.emulateMedia({ colorScheme: null });
}

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('a running machine links to its checkout in Claude Desktop, and to nothing else', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'todo-app',
		remote_url: 'github.com/heracraft/todo-app'
	});
	try {
		await setCheckout(p.id, 'todo-app');
		await page.goto(`/projects/${p.id}`);
		const link = page.getByRole('link', { name: 'Open in Claude' });
		const want = `claude://code/new?ssh_host=${p.slug}.repose&ssh_folder=/home/dev/todo-app`;
		await expect(link).toHaveAttribute('href', want);
		const href = (await link.getAttribute('href')) ?? '';
		expect(href).not.toContain('q=');
		expect(href).not.toContain('@');
		expect(href).not.toContain(p.id);
		expect(href).not.toMatch(/token/i);
		await shots(page, 'open-in-claude-running');
	} finally {
		await destroyProject(apiURLFromEnv(), p.id);
	}
});

test('with no checkout known the link opens the home, ~', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'dash-made',
		remote_url: 'github.com/heracraft/dash-made'
	});
	try {
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByRole('link', { name: 'Open in Claude' })).toHaveAttribute(
			'href',
			`claude://code/new?ssh_host=${p.slug}.repose&ssh_folder=~`
		);
		// "" is a machine with no checkout: its home, by its path.
		await setCheckout(p.id, '');
		await page.reload();
		await expect(page.getByRole('link', { name: 'Open in Claude' })).toHaveAttribute(
			'href',
			`claude://code/new?ssh_host=${p.slug}.repose&ssh_folder=/home/dev`
		);
	} finally {
		await destroyProject(apiURLFromEnv(), p.id);
	}
});

test('a stopped machine starts once, and the link opens only after it runs', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'old-app',
		remote_url: 'github.com/heracraft/old-app'
	});
	try {
		await setCheckout(p.id, 'old-app');
		await stopProject(api, p.id);
		const seen = await journal(page, p.id);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText('stopped');
		seen.length = 0;
		const button = page.getByRole('button', { name: 'Open in Claude' });
		await shots(page, 'open-in-claude-stopped');
		await page.setViewportSize({ width: 1440, height: 900 });
		await button.click();
		const want = `claude://code/new?ssh_host=${p.slug}.repose&ssh_folder=/home/dev/old-app`;
		await expect
			.poll(() => seen.filter((s) => s.startsWith('open')), { timeout: 20_000 })
			.toEqual([`open ${want}`]);
		expect(seen.filter((s) => s === 'start')).toHaveLength(1);
		const opened = seen.indexOf(`open ${want}`);
		expect(seen.indexOf('start')).toBeLessThan(opened);
		expect(seen.indexOf('running')).toBeGreaterThan(seen.indexOf('start'));
		expect(seen.indexOf('running')).toBeLessThan(opened);
		await expect(page.getByRole('link', { name: 'Open in Claude' })).toHaveAttribute('href', want);
	} finally {
		await destroyProject(api, p.id);
	}
});

test('a start that is refused says why and opens nothing', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'full-app',
		remote_url: 'github.com/heracraft/full-app'
	});
	try {
		await stopProject(api, p.id);
		const seen = await journal(page, p.id);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText('stopped');
		await failNext('POST', '/projects/:id/start', 'capacity');
		await page.getByRole('button', { name: 'Open in Claude' }).click();
		await expect(page.getByText('No capacity right now.')).toBeVisible();
		await expect(page.getByRole('button', { name: 'Open in Claude' })).toBeEnabled();
		await page.waitForTimeout(1500);
		expect(seen.filter((s) => s.startsWith('open'))).toEqual([]);
		await expect(page.getByTestId('project-state')).toHaveText('stopped');
	} finally {
		await destroyProject(api, p.id);
	}
});

test('a frozen machine opens at once, with no start', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'cold-app',
		remote_url: 'github.com/heracraft/cold-app'
	});
	try {
		await setCheckout(p.id, 'cold-app');
		await setResting(p.id, 30);
		const seen = await journal(page, p.id);
		await page.goto(`/projects/${p.id}`);
		await expect(page.getByTestId('project-state')).toHaveText('frozen');
		await page.getByRole('link', { name: 'Open in Claude' }).click();
		const want = `claude://code/new?ssh_host=${p.slug}.repose&ssh_folder=/home/dev/cold-app`;
		await expect.poll(() => seen.filter((s) => s.startsWith('open'))).toEqual([`open ${want}`]);
		expect(seen.filter((s) => s === 'start')).toEqual([]);
		await shots(page, 'open-in-claude-frozen');
	} finally {
		await destroyProject(api, p.id);
	}
});
