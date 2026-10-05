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
	await expect(page.getByText('Connect')).toBeVisible();
	await expect(page.getByText('Signals')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Machine' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Usage' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Disk' })).toBeVisible();
	await expect(page.getByText('Events')).toBeVisible();
	await expect(page.getByText('Snapshots', { exact: true })).toBeVisible();
	await expect(page.getByText('Last build')).toBeVisible();
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
	await expect(page.getByRole('heading', { name: 'Billing' })).toBeVisible();
	// The suite runs the fake with billing off unless a spec turns it on.
	await expect(page.getByText('Billing is not switched on yet.')).toBeVisible();
});

test('/settings renders', async ({ page }) => {
	await page.goto('/settings');
	await expect(page.getByText('Timezone')).toBeVisible();
	await expect(page.getByText('Notifications', { exact: true })).toBeVisible();
});

test('/account renders', async ({ page }) => {
	await page.goto('/account');
	await expect(page.getByText('heracraft').first()).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Delete account' })).toBeVisible();
});

test('/terms, /privacy and /refunds render', async ({ page }) => {
	await page.goto('/terms');
	await expect(page.locator('article')).toBeVisible();
	// The acceptable-use rules the platform enforces (DECISIONS I-238..I-240).
	await expect(page.getByRole('heading', { name: 'Acceptable use' })).toBeVisible();
	await expect(page.getByText('mine cryptocurrency, or run anything that does')).toBeVisible();
	await expect(page.getByText(/cannot connect out to\s+port 25/)).toBeVisible();
	// Billing: plans through Paddle (DECISIONS I-289).
	await expect(page.getByRole('heading', { name: 'Billing' })).toBeVisible();
	await expect(page.getByText(/Paddle, which is\s+the merchant of record/)).toBeVisible();
	await expect(page.getByText(/seven days free/)).toBeVisible();
	await page.goto('/privacy');
	await expect(page.locator('article')).toBeVisible();
	await expect(page.getByText('Draft:')).toBeVisible();
	await expect(page.getByText(/Payments are handled by Paddle/)).toBeVisible();
	await expect(page.getByRole('link', { name: "Paddle's privacy policy" })).toBeVisible();
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
