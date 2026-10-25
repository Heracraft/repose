// The live suite's signed-out half: what a visitor to the deployed
// dashboard sees, and the handover to the real Logto. Runs with no
// credentials, so anyone can run it against any deploy.
//
// Closes, against production rather than against internal/fakes/api:
//   08 §9 "Landing page contains the install command, pricing table
//          matching PRICING.md, and links to terms and privacy"
//   08 §9 "Sign-in ... against the real Logto" (the redirect half; the
//          GitHub half is account.spec.ts, which needs a session)
//   08 §6 "Logto sign-in fails or is cancelled" -> back to landing, toast
//   14    the privacy policy's process-sample boundary and the Anthropic
//          hosted-use statement are the text that is actually published
import { test, expect } from '@playwright/test';
import { LIVE_URL } from './live';

test('/healthz answers 200 ok', async ({ request }) => {
	const res = await request.get('/healthz');
	expect(res.status()).toBe(200);
	expect((await res.text()).trim()).toBe('ok');
});

test('landing renders with the install command and the sign-in button', async ({
	page
}, testInfo) => {
	await page.goto('/');
	await expect(
		page.getByRole('heading', { level: 1, name: 'Your dev environment, replicated in the cloud' })
	).toBeVisible();
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
	await expect(
		page.getByText('curl -fsSL https://repose.herakraft.co/install.sh | sh').first()
	).toBeVisible();
	// 08 §9's landing row asks for a screenshot; it rides in the Playwright
	// report rather than as a committed binary.
	await testInfo.attach('landing.png', {
		body: await page.screenshot({ fullPage: true }),
		contentType: 'image/png'
	});
});

// docs/PRICING.md "Plans" (DECISIONS I-289): the three plans and the seats
// line. A change to either file that is not matched in the other fails
// here.
test('landing pricing matches PRICING.md', async ({ page }) => {
	await page.goto('/');
	const pricing = page.locator('section', { has: page.getByRole('heading', { name: 'Pricing' }) });
	await expect(pricing.getByText('Launch prices. Seven days free.')).toBeVisible();
	// Solo shows $39 while founding places last, then $49 (I-672). The run
	// line and the disk and egress line are two lines of PlanCells' card.
	const plans: Array<[string, string | RegExp, string, string]> = [
		['solo', /\$(39|49)/, '8 GB running at once', '100 GB disk · 250 GB egress'],
		['plus', '$89', '16 GB running at once', '250 GB disk · 500 GB egress'],
		['pro', '$169', '32 GB running at once', '500 GB disk · 1 TB egress']
	];
	await expect(pricing.locator('[data-testid^="plan-"]')).toHaveCount(3);
	for (const [id, price, run, specs] of plans) {
		const cell = pricing.getByTestId(`plan-${id}`);
		await expect(cell.locator('.p-price .n')).toHaveText(price);
		await expect(cell.locator('.p-run')).toHaveText(run);
		await expect(cell.locator('.p-specs')).toHaveText(specs);
	}
	await expect(pricing.getByText('per hour')).toHaveCount(0);
	// GET /public/seats (I-290): one of the two sentences, or nothing when
	// the api is unreachable; never a third wording.
	const seats = page.getByTestId('seats-line');
	if ((await seats.count()) > 0) {
		await expect(seats).toHaveText(
			/^(\d+ seats? left|Full for now\. \d+ waiting; join the list and you're emailed when a seat frees\.)$/
		);
	}
});

test('landing links to terms and privacy, and both render', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('link', { name: 'Terms' }).click();
	await expect(page).toHaveURL(/\/terms$/);
	await expect(page.locator('article')).toBeVisible();

	await page.goto('/');
	await page.getByRole('link', { name: 'Privacy' }).click();
	await expect(page).toHaveURL(/\/privacy$/);
	await expect(page.locator('article')).toBeVisible();
});

// docs/CHECKLIST.md, release: "Privacy policy and terms published,
// containing the process-sample boundary verbatim and the Anthropic
// hosted-use statement". test/isolation/policy_test.go pins the same
// sentences in the repository; this pins them in what is served, which is
// the half a reader of the policy actually gets. Whitespace is normalised
// because the renderer rewraps.
const SAMPLE_BOUNDARY =
	'We sample the processes running in your environment once a minute and ' +
	'record their names, CPU time, memory use and network bytes. We never ' +
	'record command-line arguments, environment variables, file paths, file ' +
	'contents, terminal contents, or the prompts you give to any agent.';

const HOSTED_USE = [
	'run inside your environment under your own account',
	'does not hold, proxy, or resell those credentials',
	"responsible for complying with each provider's terms"
];

const squash = (s: string) => s.replace(/\s+/g, ' ').trim();

test('the published privacy policy carries the process-sample boundary verbatim', async ({
	page
}) => {
	await page.goto('/privacy');
	const article = squash((await page.locator('article').innerText()) ?? '');
	expect(article).toContain(SAMPLE_BOUNDARY);
	expect(article).toContain('never copy, store or proxy your Claude Code credentials');
});

test('the published terms carry the hosted-use statement', async ({ page }) => {
	await page.goto('/terms');
	const article = squash((await page.locator('article').innerText()) ?? '');
	for (const phrase of HOSTED_USE) {
		expect(article, `terms lack ${phrase}`).toContain(phrase);
	}
});

test('Get started hands over to the real Logto', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Get started' }).click();
	// Logto's /oidc/auth 303s to its own sign-in experience with the app id.
	await page.waitForURL(/\/sign-in/, { timeout: 30_000 });
	const url = new URL(page.url());
	expect(url.origin).not.toBe(LIVE_URL);
	expect(url.searchParams.get('app_id')).toBeTruthy();
	// The sign-in experience itself must offer GitHub (the connector of
	// DECISIONS R2-7); the click through it is the owner's, in
	// account.spec.ts's captured state.
	await expect(page.getByText(/GitHub/i).first()).toBeVisible({ timeout: 30_000 });
});

// 08 §6 row 1: Logto bounces back to `/` with an `error` parameter when
// sign-in is refused before a code is issued.
test('a refused sign-in returns to the landing page with a toast', async ({ page }) => {
	await page.goto('/?error=access_denied&error_description=user+cancelled');
	await expect(page.getByText('Sign-in was cancelled or failed; try again.')).toBeVisible();
	// The error parameter is cleaned out of the address bar.
	await expect(page).toHaveURL(`${LIVE_URL}/`);
});

// 08 §6 row 1 again, on the other path: a callback that carries no usable
// code fails handleSignInCallback and lands back on `/`.
test('a bad callback fails closed to the landing page', async ({ page }) => {
	await page.goto('/callback?code=not-a-real-code&state=nonsense');
	await expect(page).toHaveURL(`${LIVE_URL}/`, { timeout: 30_000 });
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
});

test('a signed-out visitor is sent away from a signed-in route', async ({ page }) => {
	await page.goto('/projects');
	await expect(page).toHaveURL(`${LIVE_URL}/`, { timeout: 30_000 });
	await expect(page.getByRole('button', { name: 'Get started' })).toBeVisible();
});

// 08 §5.1: "The dashboard has no +server.ts routes except /healthz." The
// grep proves it in the source; this proves the deploy behaves that way.
test('the deploy serves no api of its own', async ({ request }) => {
	for (const path of ['/api', '/v1/me', '/projects/x/secrets.json']) {
		const res = await request.get(path, { failOnStatusCode: false });
		expect([200, 404].includes(res.status()), `${path} answered ${res.status()}`).toBeTruthy();
		if (res.status() === 200) {
			// SvelteKit's SPA fallback serves the app shell for unknown
			// paths; what must never come back is data.
			expect(await res.text()).toContain('<!doctype html>');
		}
	}
});

// The dashboard's api client reads `json.error.{code,message}` and turns it
// into an ApiError the pages switch on (payment_required, capacity,
// rate_limited, not_found). Every test of that mapping runs against
// internal/fakes/api, so nothing checks that the deployed api still sends
// that envelope — which is the shape of bug I-79 was: a whole class of
// browser-to-api behaviour that passed against the fake and could not work
// in production. These two assert against the real api instead.
const API_URL = (process.env.REPOSE_LIVE_API_URL ?? 'https://api.repose.herakraft.co').replace(
	/\/$/,
	''
);

test('the live api refuses a bad token in the envelope the client parses', async ({ request }) => {
	for (const path of ['/v1/me', '/v1/projects', '/v1/catalog', '/v1/usage']) {
		const res = await request.get(`${API_URL}${path}`, {
			headers: { Authorization: 'Bearer not-a-real-token' },
			failOnStatusCode: false
		});
		expect(res.status(), path).toBe(401);
		const body = await res.json();
		expect(body.error?.code, path).toBe('unauthenticated');
		expect(typeof body.error?.message, path).toBe('string');
	}
});

// I-79: the dashboard and the api are different origins, and without these
// headers the browser blocks the first fetch of every session.
test('the live api sends the CORS headers the dashboard needs', async ({ request }) => {
	const preflight = await request.fetch(`${API_URL}/v1/me`, {
		method: 'OPTIONS',
		headers: {
			Origin: LIVE_URL,
			'Access-Control-Request-Method': 'GET',
			'Access-Control-Request-Headers': 'authorization'
		},
		failOnStatusCode: false
	});
	expect(preflight.status()).toBe(204);
	const h = preflight.headers();
	expect(h['access-control-allow-origin']).toBe('*');
	expect(h['access-control-allow-headers']).toContain('Authorization');
	expect(h['access-control-allow-methods']).toContain('GET');

	// And on the response itself, not only on the preflight.
	const real = await request.get(`${API_URL}/v1/me`, {
		headers: { Origin: LIVE_URL },
		failOnStatusCode: false
	});
	expect(real.headers()['access-control-allow-origin']).toBe('*');
});

// The landing page's install command is only true if the script is
// actually served there (DECISIONS I-98 put it in the build's static
// output). 08 §9's landing row and docs/CHECKLIST.md's release row both
// rest on it, and the failure mode is silent: the page keeps printing a
// command that 404s.
test('the install command on the landing page resolves to the script', async ({ request }) => {
	const res = await request.get('/install.sh');
	expect(res.status()).toBe(200);
	const body = await res.text();
	expect(body.startsWith('#!/bin/sh')).toBeTruthy();
	// The two things a reader of the landing page is about to pipe into a
	// shell: it installs `repose`, and it is the documented URL's script.
	expect(body).toContain('repose');
	expect(body).toContain('https://repose.herakraft.co/install.sh');
});
