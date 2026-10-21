import { test, expect } from '@playwright/test';
import { signIn } from './helpers';

test('sign-in, callback and sign-out round trip', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();

	await signIn(page);
	await expect(page).toHaveURL(/\/projects/);
	// The logo goes home from the dashboard too (I-655).
	const logo = page.getByRole('link', { name: 'repose, home' });
	await expect(logo).toBeVisible();
	await expect(logo).toHaveAttribute('href', '/');

	// Sign out is in the Account menu (I-655).
	await page.getByRole('button', { name: 'Account', exact: true }).click();
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
	const nav = page.getByRole('navigation', { name: 'Site' });
	await expect(nav.getByRole('link', { name: 'Dashboard' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Sign in' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Get started' })).toHaveCount(0);
	// Give a stray redirect time to fire before asserting it did not.
	await page.waitForTimeout(500);
	await expect(page).toHaveURL('/');

	await page.getByRole('link', { name: 'Open the dashboard' }).first().click();
	await expect(page).toHaveURL('/projects');
});

// Landing C10 (I-837): "Start a free week" under the plans signs in and
// comes back to Billing, where the plans are; the hero's Get started
// still comes back to Projects.
test('Start a free week signs in and comes back to Billing', async ({ page }) => {
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
	await pricing.getByRole('button', { name: 'Start a free week' }).click();
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await expect(page).toHaveURL('/billing');
	expect(await page.evaluate(() => sessionStorage.getItem('repose-return-to'))).toBeNull();
});

// DECISIONS I-568, I-655: the dashboard's header is the site header, which
// links to the docs at every width (design.spec.ts checks the phones).
test('the dashboard header links to the docs', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await signIn(page);
	await page.goto('/projects');
	const docs = page.getByRole('navigation', { name: 'Site' }).getByRole('link', { name: 'Docs' });
	await expect(docs).toBeVisible();
	await expect(docs).toHaveAttribute('href', '/docs');
});

// The Account menu holds who is signed in and Sign out, and below 640px
// the header's Docs and Feedback; the places are in the rail or strip at
// every width, so Settings and Billing are not in it (DECISIONS I-841).
for (const width of [1440, 390]) {
	test(`the Account menu holds the email and Sign out at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await signIn(page);
		await page.goto('/projects');
		await page.getByRole('button', { name: 'Account', exact: true }).click();
		const panel = page.locator('.account-panel');
		await expect(panel).toBeVisible();
		await expect(panel.locator('.account-email')).toContainText('@');
		const items = panel.locator('a, button').filter({ visible: true });
		await expect(items).toHaveText(width < 640 ? ['Docs', 'Feedback', 'Sign out'] : ['Sign out']);
		await expect(panel.getByRole('link', { name: 'Settings' })).toHaveCount(0);
		await expect(panel.getByRole('link', { name: 'Billing' })).toHaveCount(0);
	});
}

// The Account button sits in the sticky header; Escape returns focus to it
// without scrolling the page to the top (review r4).
for (const width of [1440, 390]) {
	test(`closing the Account menu with Escape keeps the scroll at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 400 });
		await signIn(page);
		await page.goto('/settings');
		await expect(page.getByRole('heading', { level: 2, name: 'Delete account' })).toBeVisible();
		await page.waitForLoadState('networkidle');
		// Well inside the page, so a late change in its height cannot move it.
		await page.evaluate(() => window.scrollTo(0, 400));
		const y = await page.evaluate(() => window.scrollY);
		expect(y).toBe(400);
		const account = page.getByRole('button', { name: 'Account', exact: true });
		// A mouse click where the button is: locator.click() scrolls a sticky
		// element "into view" itself and would move the page first.
		const box = (await account.boundingBox())!;
		await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
		await expect(page.locator('.account-panel')).toBeVisible();
		expect(await page.evaluate(() => window.scrollY)).toBe(y);
		await page.keyboard.press('Escape');
		await expect(page.locator('.account-panel')).toBeHidden();
		await expect(account).toBeFocused();
		expect(await page.evaluate(() => window.scrollY)).toBe(y);
	});
}

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
	await page.getByRole('button', { name: 'Account', exact: true }).click();
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
