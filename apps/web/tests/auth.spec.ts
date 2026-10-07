import { test, expect } from '@playwright/test';
import { signIn } from './helpers';

test('sign-in, callback and sign-out round trip', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();

	await signIn(page);
	await expect(page).toHaveURL(/\/projects/);
	await expect(page.getByRole('link', { name: 'repose' })).toBeVisible();

	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
});

// DECISIONS I-330: a signed-in visitor can read the landing page; it offers
// the dashboard where a signed-out one sees sign-in.
test('a signed-in visitor stays on the landing page, which links to the dashboard', async ({
	page
}) => {
	await signIn(page);
	await page.goto('/');
	const nav = page.getByRole('navigation', { name: 'Main' });
	await expect(nav.getByRole('link', { name: 'Dashboard' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Sign in' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Get started' })).toHaveCount(0);
	// Give a stray redirect time to fire before asserting it did not.
	await page.waitForTimeout(500);
	await expect(page).toHaveURL('/');

	await page.getByRole('link', { name: 'Open the dashboard' }).first().click();
	await expect(page).toHaveURL('/projects');
});

// DECISIONS I-568: the dashboard's header links to the docs, where a
// signed-in visitor lands after sign-in.
test('the dashboard header links to the docs', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await signIn(page);
	await page.goto('/projects');
	const docs = page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Docs' });
	await expect(docs).toBeVisible();
	await expect(docs).toHaveAttribute('href', '/docs');
});

// DECISIONS I-331: sign-out leaves the signed-in page as it is until the
// browser goes to Logto's end-session page; it never renders the landing
// page in between.
test('sign-out does not flash the landing page before leaving for Logto', async ({ page }) => {
	await signIn(page);
	const seen: string[] = [];
	await page.exposeFunction('reposeSawLanding', () => seen.push('landing-rendered'));
	await page.evaluate(() => {
		new MutationObserver(() => {
			if (document.querySelector('.hero-h')) {
				(window as unknown as { reposeSawLanding: () => void }).reposeSawLanding();
			}
		}).observe(document.body, { childList: true, subtree: true });
	});
	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
	// The observer lived only in the signed-in document; it saw no landing.
	expect(seen).not.toContain('landing-rendered');
});

test('visiting a protected route while signed out redirects to the landing page', async ({
	page
}) => {
	await page.goto('/settings');
	await expect(page).toHaveURL('/');
});

test('a cancelled or failed sign-in shows a toast and stays on the landing page', async ({
	page
}) => {
	await page.goto('/?error=access_denied');
	await expect(page.getByText('Sign-in was cancelled or failed; try again.')).toBeVisible();
	await expect(page).toHaveURL('/');
});

test('terms and privacy are reachable while signed out', async ({ page }) => {
	await page.goto('/terms');
	await expect(page).toHaveURL('/terms');
	await page.goto('/privacy');
	await expect(page).toHaveURL('/privacy');
});
