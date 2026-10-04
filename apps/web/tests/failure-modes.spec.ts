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
	if (await page.getByRole('button', { name: 'Stop' }).isVisible()) {
		await page.getByRole('button', { name: 'Stop' }).click();
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
	await expect(refusal).toContainText('Choose a plan at https://repose.herakraft.co/billing');
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
		`would pass the 8 GB of memory Solo gives running machines; ${using.slug} is using it. Stop one or upgrade.`
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
	await page.locator('select.field').selectOption('320');
	await page.getByRole('button', { name: 'Grow' }).click();
	const refusal = page.getByTestId('refusal');
	await expect(refusal).toHaveAttribute('data-reason', 'disk_limit');
	await expect(refusal).toContainText("would pass Solo's 100 GB");
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
	await expect(page.getByText('No capacity right now, try again in a few minutes.')).toBeVisible();
	// capacity is a 503 the api gives as an answer (api.md "Errors"), so
	// the page's banner is the one report and no outage bar joins it (I-393).
	await expect(page.locator('#outage')).toBeEmpty();
});

test('a 5xx from the api shows the persistent bar, which clears once the api recovers', async ({
	page
}) => {
	await fail('GET', '/projects', 'internal');
	await page.goto('/projects');
	// The api answered, so the bar says it is failing, not that it cannot
	// be reached (I-390); the page's banner names the failure once, with
	// no toast repeating it.
	const bar = page.getByText('The API is failing right now. Retrying…');
	await expect(bar).toBeVisible({ timeout: 15_000 });
	// Said inside the live region that was on the page before it (I-393).
	await expect(page.getByRole('status').filter({ has: bar })).toHaveAttribute('id', 'outage');
	await expect(page.getByText('Cannot reach the API')).toHaveCount(0);
	await expect(
		page.getByText('Could not load projects. The API failed on its side; try again shortly.')
	).toHaveCount(1);

	await unfail('GET', '/projects');
	// The next poll is scheduled up to 60s out once unreachable (backoff);
	// reloading forces an immediate re-check rather than waiting it out.
	await page.reload();
	await expect(bar).toHaveCount(0, { timeout: 10_000 });
	// The region stays, empty, for the next outage to be announced in.
	await expect(page.locator('#outage')).toBeAttached();
	await expect(page.locator('#outage')).toBeEmpty();
});
