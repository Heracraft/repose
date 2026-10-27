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
	await expect(page.getByRole('link', { name: 'Start a free week' })).toHaveAttribute(
		'href',
		'/signin'
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

test('an off-origin return path does not survive the round trip', async ({ page }) => {
	await page.goto('/signin?return=https%3A%2F%2Fevil.example%2F');
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await page.waitForURL(/\/projects/);
	expect(new URL(page.url()).origin).toBe(new URL(BASE_URL).origin);
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
