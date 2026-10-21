// Checklist: "Every route in 5.2 exists and renders with the fake API."
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

let projectId: string;
let projectName: string;

test.beforeAll(async () => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'routes-app',
		remote_url: 'github.com/heracraft/routes-app'
	});
	projectId = p.id;
	projectName = p.name;
});

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('/projects renders the projects list', async ({ page }) => {
	await page.goto('/projects');
	await expect(page.getByRole('link', { name: projectName, exact: true })).toBeVisible();
});

test('/projects/[id] renders the project detail cards', async ({ page }) => {
	await page.goto(`/projects/${projectId}`);
	await expect(page.getByRole('heading', { name: projectName })).toBeVisible();
	// The cards of I-664: the work in the main column, the machine beside it.
	for (const name of [
		'Events',
		'Usage',
		'Connect',
		'Machine',
		'Agents',
		'Details',
		'Snapshots',
		'Destroy'
	]) {
		await expect(page.getByRole('heading', { level: 2, name, exact: true })).toBeVisible();
	}
});

test('/projects/[id]/config renders the Menu and Nix tabs', async ({ page }) => {
	await page.goto(`/projects/${projectId}/config`);
	await expect(page.getByRole('tab', { name: 'Menu' })).toBeVisible();
	await expect(page.getByRole('tab', { name: 'Nix' })).toBeVisible();
});

test('/projects/[id]/secrets renders the secrets page', async ({ page }) => {
	await page.goto(`/projects/${projectId}/secrets`);
	await expect(page.getByText('Add a secret')).toBeVisible();
});

test('/billing renders', async ({ page }) => {
	await page.goto('/billing');
	await expect(page.getByRole('heading', { name: 'Billing', exact: true })).toBeVisible();
	// The suite runs the fake with billing off unless a spec turns it on.
	await expect(page.getByRole('heading', { name: 'Billing is not switched on yet' })).toBeVisible();
});

test('/settings renders', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Timezone')).toBeVisible();
	await expect(page.getByText('Notifications', { exact: true })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Account', exact: true })).toBeVisible();
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

	await page.goto('/account#machine-nix');
	await expect(page).toHaveURL('/settings#machine-nix');
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
