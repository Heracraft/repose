// docs/workstreams/08-dashboard.md §5.8 and DECISIONS I-289/I-290: the
// billing page sells three plans through Paddle, gates them on seats, and
// shows a subscription's status, usage of the plan and its invoices.
import { test, expect } from '@playwright/test';
import {
	failNext,
	signIn,
	setBilling,
	resetBilling,
	installPaddleStub,
	createProject,
	apiURLFromEnv
} from './helpers';

test.afterAll(async () => {
	await resetBilling();
});

/** Stops every running machine of the fake's one account: other specs
 * leave some running, and the plan gate measures them. */
async function stopAll(): Promise<void> {
	const api = apiURLFromEnv();
	const list = (await (
		await fetch(`${api}/projects`, { headers: { Authorization: 'Bearer playwright' } })
	).json()) as { id: string; state: string }[];
	for (const p of list) {
		if (p.state === 'running') {
			await fetch(`${api}/projects/${p.id}/stop`, {
				method: 'POST',
				headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
				body: JSON.stringify({ snapshot: false })
			});
		}
	}
}

test.beforeEach(async ({ page }) => {
	await resetBilling();
	await signIn(page);
});

test('with billing off the page says so and sells nothing', async ({ page }) => {
	await page.goto('/billing');
	await expect(page.getByTestId('billing-disabled')).toHaveText('Billing is not switched on yet.');
	await expect(page.getByRole('button', { name: /Choose/ })).toHaveCount(0);
	// billing_disabled is a 503 the api gives as an answer, not an outage,
	// so no outage bar sits over the page (I-390).
	await expect(page.getByText(/Retrying…/)).toHaveCount(0);
});

test('a failed first load says so and Retry loads the page', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await failNext('GET', '/billing', 'internal');
	await page.goto('/billing');
	const failed = page.getByRole('alert');
	await expect(failed).toBeVisible();
	await failed.getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByRole('heading', { level: 2, name: 'Solo' })).toBeVisible();
	await expect(failed).toHaveCount(0);
});

test('with no plan and seats free, the three plan cards are shown from GET /billing', async ({
	page
}) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing');
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left.');
	const solo = page.getByTestId('plan-solo');
	await expect(solo.getByRole('heading', { name: 'Solo' })).toBeVisible();
	await expect(solo.getByText('$20')).toBeVisible();
	await expect(solo.getByTestId('intro-solo')).toHaveText(
		'For 3 months, then $29 and 250 GB egress'
	);
	await expect(page.getByTestId('intro-plus')).toHaveCount(0);
	await expect(solo.getByText('8 GB: one large, or two small')).toBeVisible();
	// The introductory offer's egress allowance (I-497).
	await expect(solo.getByText('100 GB', { exact: true })).toHaveCount(2);
	await expect(solo.getByText('7 days free, card at checkout, cancel any time.')).toBeVisible();
	const plus = page.getByTestId('plan-plus');
	await expect(plus.getByRole('heading', { name: 'Plus' })).toBeVisible();
	await expect(plus.getByText('$59')).toBeVisible();
	await expect(plus.getByText('16 GB: one xl, two large, or any mix')).toBeVisible();
	const pro = page.getByTestId('plan-pro');
	await expect(pro.getByRole('heading', { name: 'Pro' })).toBeVisible();
	await expect(pro.getByText('$99')).toBeVisible();
	await expect(pro.getByText('32 GB: two xl, four large, or any mix')).toBeVisible();
	await expect(pro.getByText('500 GB', { exact: true })).toBeVisible();
	await expect(pro.getByText('1 TB', { exact: true })).toBeVisible();
	// No project count: plans sell memory, disk and egress (I-569).
	await expect(
		page.getByRole('list', { name: 'Plans' }).locator('dt', { hasText: /^Projects$/ })
	).toHaveCount(0);
	// h2 under the page's h1: the cards are the page's sections, and an h3
	// here skipped a level.
	await expect(page.getByRole('list', { name: 'Plans' }).locator('h2')).toHaveText([
		'Solo',
		'Plus',
		'Pro'
	]);
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toBeEnabled();
	await expect(page.getByRole('link', { name: 'Refunds' })).toHaveAttribute('href', '/refunds');
});

test('Solo costs $20 for three months on a first subscription, then $29; a returning account pays $29', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.goto('/billing');
	await expect(page.getByTestId('plan-status')).toContainText(
		/Active\. Renews .+ at \$20; \$29 a month from /
	);
	await setBilling({ mode: 'none', intro_used: true });
	await page.goto('/billing');
	const solo = page.getByTestId('plan-solo');
	await expect(solo.getByText('$29')).toBeVisible();
	await expect(page.getByTestId('intro-solo')).toHaveCount(0);
	await setBilling({ intro_used: false });
});

test('one seat free: Solo can be chosen, Plus and Pro say why not', async ({ page }) => {
	await setBilling({ mode: 'none', seats: { total: 30, held: 29, waiting: 0 } });
	await page.goto('/billing');
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeDisabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toBeDisabled();
	await expect(page.getByTestId('plan-plus')).toContainText('Needs 2 seats; 1 free.');
	await expect(page.getByTestId('plan-pro')).toContainText('Needs 4 seats; 1 free.');
});

test('three seats free: Solo and Plus can be chosen, Pro needs four', async ({ page }) => {
	await setBilling({ mode: 'none', seats: { total: 30, held: 27, waiting: 0 } });
	await page.goto('/billing');
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toBeDisabled();
	await expect(page.getByTestId('plan-pro')).toContainText('Needs 4 seats; 3 free.');
});

test('choosing a plan opens the checkout and, once completed, the plan appears', async ({
	page
}) => {
	await setBilling({ mode: 'none' });
	await installPaddleStub(page);
	await page.goto('/billing');
	await page.getByRole('button', { name: 'Choose Plus' }).click();
	// The stub completed the transaction; the page polls GET /billing.
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Plus' })).toBeVisible({
		timeout: 10_000
	});
	const opened = await page.evaluate(() => window.__reposePaddleOpened);
	expect(opened).toMatch(/^txn_fake_/);
	await expect(page.getByTestId('plan-status')).toContainText('Trial. First charge of $59 on');
	await expect(page.getByTestId('meter-running-now')).toContainText('0 GB of 16 GB');
	await expect(page.getByText('No invoices yet.')).toBeVisible();
});

test('coming back on ?checkout=done waits for the plan', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing?checkout=done');
	await expect(page.getByTestId('setting-up')).toContainText('Setting up your plan');
	expect(new URL(page.url()).searchParams.get('checkout')).toBeNull();
	// The webhook lands while the page polls.
	await setBilling({ mode: 'trial', plan: 'solo' });
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Solo' })).toBeVisible({
		timeout: 10_000
	});
});

test('when repose is full the page offers the waitlist and then shows the place', async ({
	page
}) => {
	await setBilling({ mode: 'none', seats: { total: 30, held: 30, waiting: 40 } });
	await page.goto('/billing');
	const full = page.getByTestId('full');
	await expect(full.getByRole('heading', { name: 'repose is full' })).toBeVisible();
	await expect(full).toContainText('Every seat is taken and 40 people are waiting.');
	await expect(page.getByRole('button', { name: /Choose/ })).toHaveCount(0);
	await page.getByTestId('join-waitlist').click();
	const place = page.getByTestId('waitlist-place');
	await expect(place).toContainText("You're number 41 on the waitlist.");
	await expect(place).toContainText(
		"We'll email dev@example.com when a seat frees; you'll have 72 hours to choose a plan."
	);
	// Reloading shows the same place: the api remembers it.
	await page.reload();
	await expect(page.getByTestId('waitlist-place')).toContainText('number 41');
});

test('an invited user sees the held seat and the plan cards', async ({ page }) => {
	await setBilling({
		mode: 'none',
		seats: { total: 30, held: 30, waiting: 12 },
		waitlist: { position: 1, invited: true, hold_hours: 70 }
	});
	await page.goto('/billing');
	await expect(page.getByTestId('seat-held')).toContainText('Your seat is held until');
	await expect(page.getByTestId('seat-held')).toContainText('(2 days left)');
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	// Plus needs two seats and Pro four; the hold is one.
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeDisabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toBeDisabled();
});

test('a trial shows the first charge date and the usage bars, and no project count', async ({
	page
}) => {
	await setBilling({ mode: 'trial', plan: 'solo', egress_gb: 300 });
	await page.goto('/billing');
	await expect(page.getByTestId('plan-status')).toContainText('Trial. First charge of $20 on');
	await expect(page.getByTestId('plan-status')).toContainText('; $29 a month from');
	await expect(page.getByTestId('meter-disk-held')).toContainText('of 100 GB');
	const egress = page.getByTestId('meter-egress-this-period');
	// A Solo trial on a first subscription has the offer's 100 GB (I-497).
	await expect(egress).toContainText('300 GB of 100 GB');
	await expect(egress).toContainText('Over by 200 GB: $10.00 on the next invoice at $0.05 a GB.');
	// Plans sell no project count (I-569): no meter, no row on the cards.
	await expect(page.getByTestId('projects-count')).toHaveCount(0);
	await expect(page.locator('dt', { hasText: /^Projects$/ })).toHaveCount(0);
});

test('the disk meter counts what the projects hold and says what is refused while it is over', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'solo', disk_held_gb: 37.5 });
	await page.goto('/billing');
	const disk = page.getByTestId('meter-disk-held');
	await expect(disk).toContainText('37.5 GB of 100 GB');
	await expect(disk).not.toContainText('over');
	await setBilling({ disk_held_gb: 112.4 });
	await page.reload();
	await expect(disk).toContainText('112.4 GB of 100 GB · over');
	await expect(disk).toContainText(
		'Creating, restoring and forking projects, and growing a disk, are refused until your projects hold less.'
	);
});

test('an active plan shows its renewal, receipts through Paddle, and invoices with PDF links', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'plus' });
	await page.goto('/billing');
	await expect(page.getByTestId('plan-status')).toContainText('Active. Renews');
	const list = page.getByRole('list', { name: 'Invoices' });
	await expect(list.getByText('$59.00')).toBeVisible();
	await expect(list.getByText('REPOSE-0001', { exact: false })).toBeVisible();
	await expect(list.getByRole('link', { name: 'PDF' })).toHaveAttribute(
		'href',
		'https://checkout.paddle.com/invoice/txn_fake_inv_000001.pdf'
	);
	await page.route('https://customer-portal.paddle.com/**', (route) =>
		route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Paddle portal</h1>' })
	);
	await page.getByRole('button', { name: /Manage card and receipts/ }).click();
	await page.waitForURL('https://customer-portal.paddle.com/cpl_fake');
	await expect(page.getByRole('heading', { name: 'Paddle portal' })).toBeVisible();
});

test('past due shows the failed payment and the card link; suspended says what happened', async ({
	page
}) => {
	await setBilling({ mode: 'past_due', plan: 'solo' });
	await page.goto('/billing');
	await expect(page.getByTestId('status-past-due')).toContainText('Your last payment failed.');
	await page.route('https://customer-portal.paddle.com/**', (route) =>
		route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Update card</h1>' })
	);
	await page.getByRole('button', { name: 'Update card' }).click();
	await page.waitForURL(/update-payment-method$/);

	await setBilling({ mode: 'suspended', plan: 'solo' });
	await page.goto('/billing');
	await expect(page.getByTestId('status-suspended')).toContainText('Your account is suspended');
	await expect(page.getByTestId('status-suspended')).toContainText('snapshots are kept 30 days');
});

test('upgrading takes effect at once; a downgrade the machines do not fit is refused with over_plan', async ({
	page
}) => {
	await stopAll();
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.goto('/billing');
	await page.getByRole('button', { name: 'Change plan' }).click();
	// From Solo both other plans are upgrades.
	await expect(page.getByTestId('change-to-plus')).toContainText('Upgrade to Plus ($59 a month');
	await expect(page.getByTestId('change-to-plus')).toContainText('Takes effect at once');
	await expect(page.getByTestId('change-to-pro')).toContainText('Upgrade to Pro ($99 a month');
	await expect(page.getByRole('button', { name: /Downgrade/ })).toHaveCount(0);
	await page.getByRole('button', { name: 'Upgrade to Plus' }).click();
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Plus' })).toBeVisible();

	// From Plus, one of each.
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toContainText('Downgrade to Solo ($29 a month');
	await expect(page.getByTestId('change-to-pro')).toContainText('Upgrade to Pro ($99 a month');
	await page.getByRole('button', { name: 'Change plan' }).click();

	// Two large machines running: 16 GB, Plus's whole allowance and twice Solo's.
	const api = apiURLFromEnv();
	await createProject(api, {
		name: 'over-a',
		remote_url: 'github.com/heracraft/over-a',
		class: 'large'
	});
	await createProject(api, {
		name: 'over-b',
		remote_url: 'github.com/heracraft/over-b',
		class: 'large'
	});
	await page.reload();
	await expect(page.getByTestId('meter-running-now')).toContainText('16 GB of 16 GB');
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-plan')).toContainText('Downgrade to Solo ($29 a month');
	await page.getByRole('button', { name: 'Downgrade to Solo' }).click();
	const err = page.getByTestId('change-error');
	// The api's sentence, the disk counted by what the projects hold (I-585).
	await expect(err).toContainText(
		'your account does not fit the Solo plan yet: 16 GB running (it allows 8)'
	);
	await expect(err).toContainText('GB of disk held by your projects (it allows 100)');
	await expect(err).toContainText(
		'stop machines, or destroy projects or delete files in them, first'
	);
});

test('on Pro both other plans are downgrades, and Plus is refused while three large machines run', async ({
	page
}) => {
	await stopAll();
	await setBilling({ mode: 'active', plan: 'pro' });
	const api = apiURLFromEnv();
	for (const n of ['pro-a', 'pro-b', 'pro-c']) {
		await createProject(api, { name: n, remote_url: `github.com/heracraft/${n}`, class: 'large' });
	}
	await page.goto('/billing');
	await expect(page.getByTestId('meter-running-now')).toContainText('24 GB of 32 GB');
	await expect(page.getByTestId('meter-running-now')).toContainText(
		'two xl, four large, or any mix'
	);
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toContainText('Downgrade to Solo ($29 a month');
	await expect(page.getByTestId('change-to-plus')).toContainText('Downgrade to Plus ($59 a month');
	await expect(page.getByTestId('change-to-plus')).toContainText('Takes effect at the renewal');
	await expect(page.getByRole('button', { name: /Upgrade/ })).toHaveCount(0);
	await page.getByRole('button', { name: 'Downgrade to Plus' }).click();
	const err = page.getByTestId('change-error');
	await expect(err).toContainText(
		'your account does not fit the Plus plan yet: 24 GB running (it allows 16)'
	);
	await stopAll();
});

test('a downgrade that fits is scheduled for the renewal and can be undone', async ({ page }) => {
	// The seat count and plan are set directly; the fake's own projects
	// (created by other specs) are what the gate measures, so this test
	// stops them all first through the api.
	await stopAll();
	await setBilling({ mode: 'active', plan: 'pro' });
	await page.goto('/billing');
	await page.getByRole('button', { name: 'Change plan' }).click();
	await page.getByRole('button', { name: 'Downgrade to Plus' }).click();
	await expect(page.getByTestId('plan-status')).toContainText('Changes to Plus on');
	// With Plus scheduled, the box offers to keep Pro and still lists Solo.
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-keep')).toContainText('Plus is scheduled for');
	await expect(page.getByTestId('change-to-solo')).toContainText('Downgrade to Solo');
	await expect(page.getByTestId('change-to-plus')).toHaveCount(0);
	await page.getByRole('button', { name: 'Keep Pro' }).click();
	await expect(page.getByTestId('plan-status')).not.toContainText('Changes to Plus');
});

test('cancelling asks first, then shows the end date and a Resume that undoes it', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.goto('/billing');
	// One panel at a time: the question closes Change plan and the reverse.
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-plan')).toBeVisible();
	await page.getByRole('button', { name: 'Cancel plan' }).click();
	await expect(page.getByTestId('change-plan')).toHaveCount(0);
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('confirm-cancel')).toHaveCount(0);
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-plan')).toHaveCount(0);
	await page.getByRole('button', { name: 'Cancel plan' }).click();
	const confirm = page.getByTestId('confirm-cancel');
	await expect(confirm).toContainText('It ends on');
	await expect(confirm).toContainText('snapshots are kept 30 days after');
	// The documented two-step (I-393): the button turns into the question,
	// focus goes to Keep it, and Keep it gives it back to Cancel plan.
	await expect(confirm.getByRole('button', { name: 'Keep it' })).toBeFocused();
	await expect(page.getByRole('button', { name: 'Cancel plan' })).toHaveCount(1);
	await page.getByRole('button', { name: 'Keep it' }).click();
	await expect(page.getByTestId('confirm-cancel')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Cancel plan' })).toBeFocused();
	await page.getByRole('button', { name: 'Cancel plan' }).click();
	await page.getByTestId('confirm-cancel').getByRole('button', { name: 'Cancel plan' }).click();
	await expect(page.getByTestId('plan-status')).toContainText('Cancelled. Ends');
	await expect(page.getByRole('button', { name: 'Change plan' })).toHaveCount(0);
	await page.getByRole('button', { name: 'Resume plan' }).click();
	await expect(page.getByTestId('plan-status')).toContainText('Active. Renews');
	// Resume plan is gone; focus goes to Cancel plan, not <body>.
	await expect(page.getByRole('button', { name: 'Cancel plan' })).toBeFocused();
});
