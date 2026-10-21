// Checklist: "Every failure row in §6 is exercised."
import { test, expect, type Page } from '@playwright/test';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	failNext,
	fail,
	unfail,
	setBilling,
	resetBilling
} from './helpers';

test.beforeEach(async ({ page }) => {
	await resetBilling();
	await signIn(page);
});

test.afterAll(async () => {
	await resetBilling();
});

// A freshly created project starts already running (the fake's create
// path), so the Start tests stop it first. Wait for the page to render its
// action button before looking: an `isVisible()` on a page still loading
// is false, the stop is skipped, and the Start click then waits on a
// button that never appears (CI, 2026-09-20). The one-shot failure rule is
// armed only after the stop so that a failed run leaves nothing behind for
// the next test to trip on.
async function stopIfRunning(page: Page): Promise<void> {
	const action = page.getByRole('button', { name: /^(Start|Stop)$/ });
	await expect(action.first()).toBeVisible({ timeout: 10_000 });
	if (await page.getByRole('button', { name: 'Stop', exact: true }).isVisible()) {
		await page.getByRole('button', { name: 'Stop', exact: true }).click();
	}
	await expect(page.getByRole('button', { name: 'Start', exact: true })).toBeVisible({
		timeout: 10_000
	});
}

// api.md's six payment_required reasons, each as the fake's gate answers
// it (not the error switch, which carries no detail): the api's sentence,
// the link the reason wants, and for plan_limit a Stop for each machine
// named (DECISIONS I-289).
test('Start with no plan shows the refusal with a Choose a plan link and the button stays enabled', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'no-plan-app',
		remote_url: 'github.com/heracraft/no-plan-app'
	});
	await page.goto(`/projects/${p.id}`);
	await stopIfRunning(page);
	await setBilling({ mode: 'none' });

	const startBtn = page.getByRole('button', { name: 'Start', exact: true });
	await startBtn.click();
	const refusal = page.getByTestId('refusal');
	await expect(refusal).toHaveAttribute('data-reason', 'subscription_required');
	await expect(refusal).toContainText('Choose a plan before a machine can start.');
	await expect(refusal.getByRole('link', { name: 'Choose a plan' })).toHaveAttribute(
		'href',
		'/billing'
	);
	await expect(startBtn).toBeEnabled();
});

test('a payment_required without a reason (an older api) still links to Billing', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'old-api-app',
		remote_url: 'github.com/heracraft/old-api-app'
	});
	await page.goto(`/projects/${p.id}`);
	await stopIfRunning(page);
	await failNext('POST', '/projects/:id/start', 'payment_required');
	await page.getByRole('button', { name: 'Start', exact: true }).click();
	const refusal = page.getByTestId('refusal');
	await expect(refusal).toHaveAttribute('data-reason', 'unknown');
	await expect(refusal.getByRole('link', { name: 'Billing' })).toHaveAttribute('href', '/billing');
});

test('plan_limit names the machines using the memory, each with a Stop', async ({ page }) => {
	const api = apiURLFromEnv();
	const using = await createProject(api, {
		name: 'busy-app',
		remote_url: 'github.com/heracraft/busy-app',
		class: 'large'
	});
	const p = await createProject(api, {
		name: 'next-app',
		remote_url: 'github.com/heracraft/next-app'
	});
	await page.goto(`/projects/${p.id}`);
	await stopIfRunning(page);
	// Every other project of the suite is stopped so that busy-app is the
	// one machine holding Solo's 8 GB.
	const list = (await (
		await fetch(`${api}/projects`, { headers: { Authorization: 'Bearer playwright' } })
	).json()) as { id: string; state: string }[];
	for (const q of list) {
		if (q.state === 'running' && q.id !== using.id) {
			await fetch(`${api}/projects/${q.id}/stop`, {
				method: 'POST',
				headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
				body: JSON.stringify({ snapshot: false })
			});
		}
	}
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.getByRole('button', { name: 'Start', exact: true }).click();
	const refusal = page.getByTestId('refusal');
	await expect(refusal).toHaveAttribute('data-reason', 'plan_limit');
	await expect(refusal).toContainText(
		`Your Solo plan runs 8 GB at once and ${using.slug} is using it. Stop it.`
	);
	await expect(refusal.getByRole('link', { name: 'Upgrade' })).toHaveAttribute('href', '/billing');
	await refusal.getByRole('button', { name: `Stop ${using.slug}` }).click();
	await expect(page.getByText(`Stopped ${using.slug}.`)).toBeVisible({
		timeout: 10_000
	});
	await expect(refusal.getByRole('button', { name: /^Stop / })).toHaveCount(0);
	// Now it fits.
	await page.getByRole('button', { name: 'Start', exact: true }).click();
	await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeVisible({
		timeout: 10_000
	});
});

test('disk_limit on a resize is told like a start refusal, with a Change plan link', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'disk-app',
		remote_url: 'github.com/heracraft/disk-app'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByRole('button', { name: /^(Start|Stop)$/ }).first()).toBeVisible();
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.getByRole('button', { name: 'Resize…' }).click();
	await page.locator('select.hk-select').selectOption('320');
	await page.getByRole('button', { name: 'Grow' }).click();
	const refusal = page.getByTestId('refusal');
	await expect(refusal).toHaveAttribute('data-reason', 'disk_limit');
	// A disk is a ceiling; one disk may be at most the plan's whole disk (I-585).
	await expect(refusal).toContainText("one project's disk can be at most 100 GB");
	await expect(refusal.getByRole('link', { name: 'Change plan' })).toHaveAttribute(
		'href',
		'/billing'
	);
});

test('egress_limit, past_due and suspended each show the sentence and the right link', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'gated-app',
		remote_url: 'github.com/heracraft/gated-app'
	});
	await page.goto(`/projects/${p.id}`);
	await stopIfRunning(page);
	const startBtn = page.getByRole('button', { name: 'Start', exact: true });
	const refusal = page.getByTestId('refusal');

	// A first Solo subscription keeps the plan's 250 GB at the founding price (I-672).
	await setBilling({ mode: 'active', plan: 'solo', egress_gb: 1000 });
	await startBtn.click();
	await expect(refusal).toHaveAttribute('data-reason', 'egress_limit');
	await expect(refusal).toContainText(
		"egress this period is 1000 GB, four times Solo's 250 GB allowance."
	);
	await expect(refusal.getByRole('link', { name: 'See usage' })).toHaveAttribute(
		'href',
		'/billing'
	);

	await setBilling({ mode: 'past_due', plan: 'solo', egress_gb: 0 });
	await startBtn.click();
	await expect(refusal).toHaveAttribute('data-reason', 'past_due');
	await expect(refusal).toContainText('Your last payment failed.');
	await expect(refusal.getByRole('link', { name: 'Update card' })).toHaveAttribute(
		'href',
		'/billing'
	);

	await setBilling({ mode: 'suspended', plan: 'solo' });
	await startBtn.click();
	await expect(refusal).toHaveAttribute('data-reason', 'suspended');
	await expect(refusal).toContainText('Your account is suspended');
	await expect(refusal.getByRole('link', { name: 'Pay the invoice' })).toHaveAttribute(
		'href',
		'/billing'
	);
});

test('capacity on Start shows the documented message', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'capacity-app',
		remote_url: 'github.com/heracraft/capacity-app'
	});
	await page.goto(`/projects/${p.id}`);
	await stopIfRunning(page);
	await failNext('POST', '/projects/:id/start', 'capacity');
	await page.getByRole('button', { name: 'Start', exact: true }).click();
	await expect(page.getByText('No capacity right now.')).toBeVisible();
	// capacity is a 503 the api gives as an answer (api.md "Errors"), so
	// the page's banner is the one report and no outage bar joins it (I-393).
	await expect(page.locator('#outage')).toBeEmpty();
});

test('a 5xx from the api shows the persistent bar, which clears once the api recovers', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await fail('GET', '/projects', 'internal');
	// The rule is lifted whatever happens here: left on, it fails every
	// later spec that lists projects (CI on b2524af4).
	const bar = page.getByText('The API is failing right now. Retrying…');
	try {
		await page.goto('/projects');
		// The api answered, so the bar says it is failing, not that it cannot
		// be reached (I-390); the page's banner names the failure once, with
		// no toast repeating it.
		await expect(bar).toBeVisible({ timeout: 15_000 });
		// Said inside the live region that was on the page before it (I-393).
		await expect(page.getByRole('status').filter({ has: bar })).toHaveAttribute('id', 'outage');
		await expect(page.getByText('Cannot reach the API')).toHaveCount(0);
		// The strip sits in the page's column: the short page still ends at
		// the window's foot, footer included (review r4).
		expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBe(900);
		await expect(
			page.getByText('Could not load projects. The API failed on its side; try again shortly.')
		).toHaveCount(1);
	} finally {
		await unfail('GET', '/projects');
	}
	// The next poll is scheduled up to 60s out once unreachable (backoff);
	// reloading forces an immediate re-check rather than waiting it out.
	await page.reload();
	await expect(bar).toHaveCount(0, { timeout: 10_000 });
	// The region stays, empty, for the next outage to be announced in.
	await expect(page.locator('#outage')).toBeAttached();
	await expect(page.locator('#outage')).toBeEmpty();
});

test('a questions answer arriving after a failed list leaves the bar up', async ({ page }) => {
	// The first load asks for the questions beside the list (I-693). Their
	// answer, held until after the list's 500, is a side read: it must not
	// clear the bar the list raised (I-393; CI on b2524af4).
	await page.route(/\/questions\?state=pending$/, async (route) => {
		await new Promise((r) => setTimeout(r, 1_500));
		await route.continue();
	});
	await fail('GET', '/projects', 'internal');
	try {
		const answered = page.waitForResponse(/\/questions\?state=pending$/);
		await page.goto('/projects');
		const bar = page.getByText('The API is failing right now. Retrying…');
		await expect(bar).toBeVisible({ timeout: 15_000 });
		await answered;
		await page.waitForTimeout(500);
		await expect(bar).toBeVisible();
	} finally {
		await unfail('GET', '/projects');
	}
});

// The root boundary (AppBoundary): a page that throws while it renders
// shows the 500 page with Reload as its one primary, in the row of ways
// back and at their size. On /projects there is no "Back to Projects".
test('a /projects that throws while it renders offers Reload and Docs, at one size', async ({
	page
}) => {
	await page.route('**/v1/projects/destroyed**', (r) =>
		r.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
	);
	await page.goto('/projects');
	await expect(page.getByRole('heading', { level: 1, name: 'Something went wrong' })).toBeVisible();
	const reload = page.getByRole('button', { name: 'Reload' });
	const docs = page.locator('main#main').getByRole('link', { name: 'Docs' });
	await expect(reload).toBeVisible();
	await expect(docs).toBeVisible();
	await expect(page.getByRole('link', { name: 'Back to Projects' })).toHaveCount(0);
	const r = (await reload.boundingBox())!;
	const d = (await docs.boundingBox())!;
	// One row: their centres line up.
	expect(Math.abs(r.y + r.height / 2 - (d.y + d.height / 2))).toBeLessThan(2);
});
