// Checklist: "Every route in 5.2 exists and renders with the fake API."
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, setAgents } from './helpers';

let projectId: string;
let projectName: string;

test.beforeAll(async () => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'routes-app',
		remote_url: 'github.com/heracraft/routes-app'
	});
	projectId = p.id;
	projectName = p.name;
	// Agents shows only while the machine reports one (spec 2.5).
	await setAgents(p.id, [{ agent: 'claude', window: 'claude', state: 'working' }]);
});

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('/projects renders the projects list', async ({ page }) => {
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: projectName, exact: true })).toBeVisible();
});

test('/questions renders', async ({ page }) => {
	await page.goto('/questions');
	await expect(page.getByRole('heading', { level: 1, name: 'Questions' })).toBeVisible();
	await expect(page).toHaveTitle(/(^|\) )Questions · repose$/);
});

// A project is five tabs under one header (I-841): the layout draws the
// title and the tabs, each tab its own sections.
test('/projects/[id] renders the Overview sections and the five tabs', async ({ page }) => {
	await page.goto(`/projects/${projectId}`);
	await expect(page.getByRole('heading', { level: 1, name: projectName })).toBeVisible();
	for (const name of ['Connect', 'Agents', 'Machine', 'Details', 'Destroy']) {
		await expect(page.getByRole('heading', { level: 2, name, exact: true })).toBeVisible();
	}
	const tabs = page.getByRole('navigation', { name: 'Project' }).getByRole('link');
	await expect(tabs).toHaveText(['Overview', 'Events', 'Usage', 'Snapshots', 'Config']);
});

for (const [tab, path] of [
	['Events', 'events'],
	['Usage', 'usage'],
	['Snapshots', 'snapshots']
]) {
	test(`/projects/[id]/${path} renders the ${tab} tab`, async ({ page }) => {
		await page.goto(`/projects/${projectId}/${path}`);
		await expect(page.getByRole('heading', { level: 1, name: projectName })).toBeVisible();
		await expect(page).toHaveTitle(new RegExp(`${tab} · ${projectName} · repose$`));
		await expect(
			page.getByRole('navigation', { name: 'Project' }).getByRole('link', { name: tab })
		).toHaveAttribute('aria-current', 'page');
	});
}

test('/projects/[id]/config renders the Menu and Nix tabs and the secrets', async ({ page }) => {
	await page.goto(`/projects/${projectId}/config`);
	await expect(page.getByRole('tab', { name: 'Menu' })).toBeVisible();
	await expect(page.getByRole('tab', { name: 'Nix' })).toBeVisible();
	await expect(
		page.getByRole('heading', { level: 2, name: /^(Secrets|\d+ secrets?)$/ })
	).toBeVisible();
});

test('/projects/[id]/secrets moves to the secrets on Config', async ({ page }) => {
	await page.goto(`/projects/${projectId}/secrets`);
	await expect(page).toHaveURL(`/projects/${projectId}/config#secrets`);
});

test('/billing renders', async ({ page }) => {
	await page.goto('/billing');
	await expect(page.getByRole('heading', { name: 'Billing', exact: true })).toBeVisible();
	// The suite runs the fake with billing off unless a spec turns it on.
	await expect(page.getByRole('heading', { name: 'Billing is not switched on yet' })).toBeVisible();
});

test('/settings renders General', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Timezone')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Account', exact: true })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Delete account', exact: true })).toBeVisible();
});

test('/settings/notifications renders', async ({ page }) => {
	await page.goto('/settings/notifications');
	await expect(page.getByText('Email notifications')).toBeVisible();
	await expect(page).toHaveTitle(/Notifications · Settings · repose$/);
});

test('/settings/secrets renders', async ({ page }) => {
	await page.goto('/settings/secrets');
	await expect(page.locator('#secrets')).toBeVisible();
	await expect(page).toHaveTitle(/Secrets · Settings · repose$/);
});

test('/settings/machine-nix renders', async ({ page }) => {
	await page.goto('/settings/machine-nix');
	await expect(page.locator('#machine-nix .nix-frame')).toBeVisible();
	await expect(page).toHaveTitle(/machine\.nix · Settings · repose$/);
});

// DECISIONS I-578: the account moved into Settings; emails, docs and
// bookmarks that link to /account land there.
test('/account redirects permanently to /settings', async ({ page, request }) => {
	const res = await request.get('/account', { maxRedirects: 0 });
	expect(res.status()).toBe(308);
	expect(res.headers()['location']).toBe('/settings');
	const head = await request.head('/account', { maxRedirects: 0 });
	expect(head.status()).toBe(308);
	expect(head.headers()['location']).toBe('/settings');

	await page.goto('/account');
	await expect(page).toHaveURL('/settings');
	await expect(page.locator('#account').getByText('heracraft').first()).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Delete account' })).toBeVisible();

	// The 308 keeps the fragment, and machine.nix is a tab of its own (I-841).
	await page.goto('/account#machine-nix');
	await expect(page).toHaveURL('/settings/machine-nix');
});

// Every old URL keeps working (DECISIONS I-841, lib/legacyUrls.ts): the
// sections that became tabs move there, and the history entry is
// replaced, so Back leaves instead of bouncing.
test('old dashboard URLs land on their new homes', async ({ page }) => {
	const cases: [string, string][] = [
		['/settings#machine-nix', '/settings/machine-nix'],
		['/settings#secrets', '/settings/secrets'],
		['/settings#notifications', '/settings/notifications'],
		['/settings#delete-account', '/settings#delete-account'],
		[`/projects/${projectId}?events_page=2`, `/projects/${projectId}/events?events_page=2`],
		[`/projects/${projectId}#events-card`, `/projects/${projectId}/events`],
		[`/projects/${projectId}#usage-card`, `/projects/${projectId}/usage`],
		[`/projects/${projectId}#snapshots`, `/projects/${projectId}/snapshots`],
		[`/projects/${projectId}#secrets`, `/projects/${projectId}/config#secrets`],
		[`/projects/${projectId}#questions`, `/projects/${projectId}#questions`]
	];
	for (const [from, to] of cases) {
		await page.goto(from);
		await expect(page, from).toHaveURL(to);
	}
});

test('Back after a moved URL leaves the dashboard, with no bounce', async ({ page }) => {
	await page.goto('/docs');
	await page.goto('/settings#machine-nix');
	await expect(page).toHaveURL('/settings/machine-nix');
	await page.goBack();
	await expect(page).toHaveURL('/docs');
	await page.waitForTimeout(500);
	await expect(page).toHaveURL('/docs');
});

test('/terms, /privacy and /refunds render', async ({ page }) => {
	await page.goto('/terms');
	await expect(page.locator('article')).toBeVisible();
	// The acceptable-use rules the platform enforces (DECISIONS I-238..I-240).
	await expect(page.getByRole('heading', { name: 'Acceptable use' })).toBeVisible();
	await expect(page.getByText('mine cryptocurrency, or run anything that does')).toBeVisible();
	await expect(page.getByText(/cannot connect out to\s+port 25/)).toBeVisible();
	// Billing: plans through Polar (DECISIONS I-604).
	await expect(page.getByRole('heading', { name: 'Billing' })).toBeVisible();
	await expect(page.getByText(/Polar, which is\s+the merchant of record/)).toBeVisible();
	await expect(page.getByText(/seven days free/)).toBeVisible();
	await page.goto('/privacy');
	await expect(page.locator('article')).toBeVisible();
	await expect(page.getByText('Draft:')).toBeVisible();
	await expect(page.getByText(/Payments are handled by Polar/)).toBeVisible();
	await expect(page.getByRole('link', { name: "Polar's privacy policy" })).toBeVisible();
	await page.goto('/refunds');
	await expect(page.getByRole('heading', { name: 'Refund policy' })).toBeVisible();
	// Public: the layout must not bounce a signed-out reader to the landing
	// page once it hydrates (it did, 2026-09-27).
	await page.waitForTimeout(1500);
	expect(new URL(page.url()).pathname).toBe('/refunds');
	await expect(page.getByText(/within 14\s+days of that first charge/)).toBeVisible();
	await expect(page.getByText(/A renewal is not refunded for a part of a month/)).toBeVisible();
});

test('/healthz answers 200', async ({ request }) => {
	const res = await request.get('/healthz');
	expect(res.status()).toBe(200);
});

// The primitives gallery is for `vite dev` only (Guide B 16): a production
// build answers it with the root 404, so its sample figures never ship.
test('/design is Not found in a production build', async ({ page }) => {
	await page.goto('/design');
	await expect(page.getByRole('heading', { level: 1, name: 'Not found' })).toBeVisible();
	await expect(page.getByRole('heading', { level: 1, name: 'Design' })).toHaveCount(0);
});
