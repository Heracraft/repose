import { test, expect } from '@playwright/test';
import { BASE_URL } from './fixtures';
import { signIn } from './helpers';

test('sign-in, callback and sign-out round trip', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('link', { name: 'Get started' })).toBeVisible();

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
	await expect(page.getByRole('link', { name: 'Get started' })).toBeVisible();
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
	await expect(page.getByRole('link', { name: 'Sign in' })).toHaveCount(0);
	await expect(page.getByRole('link', { name: 'Get started' })).toHaveCount(0);
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
	await pricing.getByRole('link', { name: 'Start a free week' }).click();
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
	await expect(page.getByRole('link', { name: 'Get started' })).toBeVisible();
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

// DECISIONS I-864, I-878: the call to action is a link to a server route
// that starts the OIDC flow, so a press that lands before the bundle has
// hydrated navigates instead of being swallowed, and middle-click,
// open-in-new-tab and copy-link-address work.
test('every sign-in surface is a link to /signin', async ({ page, request }) => {
	// The floor is the prerendered HTML, before a script has run at all.
	// SvelteKit writes these hrefs relative (paths.relative), so from / the
	// landing's are "./signin".
	const prerendered = await (await request.get('/')).text();
	expect(prerendered).toMatch(/href="\.?\/signin"/);
	expect(prerendered).not.toContain('Get started</button');

	await page.goto('/');
	const cta = page.getByRole('link', { name: 'Get started' });
	await expect(cta).toHaveAttribute('href', '/signin');
	await expect(
		page.getByRole('navigation', { name: 'Site' }).getByRole('link', { name: 'Sign in' })
	).toHaveAttribute('href', '/signin');
	// Start a free week comes back to /billing (I-837), so its href names
	// that return path; /signin refuses anything not on the allow-list.
	await expect(page.getByRole('link', { name: 'Start a free week' })).toHaveAttribute(
		'href',
		'/signin?return=%2Fbilling'
	);
});

// The body carries data-sveltekit-preload-data="hover", and a hover that
// started a flow would burn a cookie and a Logto state on every pass of
// the pointer. The router only preloads routes it has a page for, and
// /signin is an endpoint, so it leaves this one alone.
test('hovering the call to action starts no flow', async ({ page, context }) => {
	await page.goto('/');
	await page.getByRole('link', { name: 'Get started' }).hover();
	await page.waitForTimeout(500);
	const cookies = await context.cookies();
	expect(cookies.find((c) => c.name === 'repose_signin')).toBeUndefined();
});

test('/signin redirects to Logto and keeps the PKCE verifier in an httpOnly cookie', async ({
	request
}) => {
	const res = await request.get('/signin', { maxRedirects: 0 });
	expect(res.status()).toBe(303);
	expect(res.headers()['cache-control']).toBe('no-store');

	const location = new URL(res.headers()['location']);
	expect(location.pathname).toBe('/oidc/auth');
	expect(location.origin).toBe(new URL(process.env.PUBLIC_LOGTO_ENDPOINT ?? '').origin);
	expect(location.searchParams.get('code_challenge_method')).toBe('S256');
	expect(location.searchParams.get('code_challenge')).toMatch(/^[A-Za-z0-9_-]{43}$/);
	expect(location.searchParams.get('state')).toBeTruthy();

	const setCookie = res
		.headersArray()
		.filter((h) => h.name.toLowerCase() === 'set-cookie')
		.map((h) => h.value)
		.find((v) => v.startsWith('repose_signin='));
	expect(setCookie).toBeTruthy();
	expect(setCookie).toContain('HttpOnly');
	expect(setCookie).toContain('Secure');
	expect(setCookie).toContain('SameSite=Lax');
	expect(setCookie).toContain('Path=/callback');
	expect(setCookie).toContain('Max-Age=600');
	// The verifier is not in the redirect: only its S256 challenge is.
	expect(res.headers()['location']).not.toContain('code_verifier');
});

test('a sign-in started on the server completes and lands on the dashboard', async ({ page }) => {
	// Straight to the route, as a visitor whose click beat hydration gets
	// there. The exchange then happens on the server, with the verifier
	// from the cookie, and the page adopts the session.
	await page.goto('/signin');
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await page.waitForURL(/\/projects/);
	await expect(page.getByRole('button', { name: 'Account', exact: true })).toBeVisible();
});

test('a return path that is not on the allow-list does not survive the round trip', async ({
	page
}) => {
	await page.goto('/signin?return=https%3A%2F%2Fevil.example%2F');
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await page.waitForURL(/\/projects/);
	expect(new URL(page.url()).origin).toBe(new URL(BASE_URL).origin);
});

test('a return path that is on the allow-list is honoured', async ({ page }) => {
	await page.goto('/signin?return=%2Fbilling');
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await expect(page).toHaveURL('/billing');
});

// The point of the server route: the whole sign-in redirect round trip,
// from the call to action to Logto and back, with no script anywhere.
// Before I-878 the call to action was a <button> whose only handler came
// with the bundle, so this click did nothing at all.
test.describe('with JavaScript disabled', () => {
	test.use({ javaScriptEnabled: false });

	test('the call to action still starts sign-in', async ({ page, context }) => {
		await page.goto('/');
		await page.getByRole('link', { name: 'Get started' }).click();

		await page.waitForURL(/\/oidc\/auth/);
		await expect(page.getByRole('heading', { name: /fake Logto/ })).toBeVisible();

		// The fake's approval is a plain form submit, so it needs no script.
		await page.getByRole('button', { name: 'Continue as heracraft' }).click();
		await page.waitForURL(/\/callback\?/);

		// The dashboard itself is a client-rendered SPA, so this is where a
		// scriptless visitor stops, and the page says so rather than
		// waiting for ever.
		await expect(page.getByText('The dashboard needs JavaScript.')).toBeVisible();
		await expect(page.getByText('Signing in…')).toBeHidden();

		// One flow per cookie: /callback spends it whether or not the
		// exchange worked, so no code can be put through it twice.
		const cookies = await context.cookies();
		expect(cookies.find((c) => c.name === 'repose_signin')).toBeUndefined();
	});
});
