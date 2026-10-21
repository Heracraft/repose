// docs/workstreams/08-dashboard.md §5.8 and DECISIONS I-289/I-290: the
// billing page sells three plans through Polar, gates them on seats, and
// shows a subscription's status, usage of the plan and its invoices.
import { test, expect } from '@playwright/test';
import {
	fail,
	failNext,
	unfail,
	signIn,
	setBilling,
	resetBilling,
	createProject,
	destroyProject,
	apiURLFromEnv,
	type BillingState
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
	await expect(page.getByTestId('billing-disabled')).toHaveText('Billing is not switched on yet');
	// A state in the kit's empty row, not a stray line (I-837).
	await expect(page.getByTestId('billing-disabled')).toHaveClass(/(^|\s)hk-empty-row(\s|$)/);
	await expect(page.getByRole('button', { name: /Choose/ })).toHaveCount(0);
	// billing_disabled is a 503 the api gives as an answer, not an outage,
	// so no outage bar sits over the page (I-390).
	await expect(page.getByText(/Retrying…/)).toHaveCount(0);
});

test('a failed first load says so and Retry loads the page', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing');
	await expect(page.getByRole('heading', { level: 2, name: 'Solo' })).toBeVisible();
	// Every read fails until unfail: the rail's own read in the root
	// layout (I-841) would otherwise take a one-shot failure.
	await fail('GET', '/billing', 'internal');
	try {
		await page.reload();
		await expect(page.getByRole('alert')).toBeVisible();
	} finally {
		await unfail('GET', '/billing');
	}
	const failed = page.getByRole('alert');
	await failed.getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByRole('heading', { level: 2, name: 'Solo' })).toBeVisible();
	await expect(failed).toHaveCount(0);
});

test('with no plan and seats free, the three plans are shown from GET /billing', async ({
	page
}) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing');
	await expect(page.getByTestId('seats-line')).toHaveText('18 seats left');
	await expect(page.getByText('7 days free, card at checkout, cancel any time.')).toBeVisible();
	const solo = page.getByTestId('plan-solo');
	await expect(solo.getByRole('heading', { name: 'Solo' })).toBeVisible();
	await expect(solo.getByText('$39', { exact: true })).toBeVisible();
	await expect(solo.getByTestId('intro-solo')).toHaveText('For 12 months');
	await expect(solo.locator('.p-then')).toHaveText('For 12 months, then $49');
	await expect(page.getByTestId('intro-plus')).toHaveCount(0);
	await expect(solo.getByText('8 GB running at once')).toBeVisible();
	// The founding price keeps Solo's 250 GB egress (I-672); 100 GB is the disk.
	await expect(solo.getByText('100 GB', { exact: true })).toHaveCount(1);
	await expect(solo.getByText('250 GB', { exact: true })).toHaveCount(1);
	const plus = page.getByTestId('plan-plus');
	await expect(plus.getByRole('heading', { name: 'Plus' })).toBeVisible();
	await expect(plus.getByText('$89', { exact: true })).toBeVisible();
	await expect(plus.getByText('16 GB running at once')).toBeVisible();
	const pro = page.getByTestId('plan-pro');
	await expect(pro.getByRole('heading', { name: 'Pro' })).toBeVisible();
	await expect(pro.getByText('$169', { exact: true })).toBeVisible();
	await expect(pro.getByText('32 GB running at once')).toBeVisible();
	await expect(pro.getByText('500 GB', { exact: true })).toBeVisible();
	await expect(pro.getByText('1 TB', { exact: true })).toBeVisible();
	// No project count: plans sell memory, disk and egress (I-569).
	await expect(
		page.getByRole('list', { name: 'Plans' }).locator('dt', { hasText: /^Projects$/ })
	).toHaveCount(0);
	// h2 under the page's h1: the cells are the page's sections, and an h3
	// here skipped a level.
	await expect(page.getByRole('list', { name: 'Plans' }).locator('h2')).toHaveText([
		'Solo',
		'Plus',
		'Pro'
	]);
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toBeEnabled();
	// With no plan asked for, no button is the solid one.
	await expect(page.locator('[data-testid^="plan-"] .hk-btn')).toHaveCount(0);
	await expect(page.getByRole('main').getByRole('link', { name: 'Refunds' })).toHaveAttribute(
		'href',
		'/refunds'
	);
});

test('?plan=plus makes Plus the solid button and gives it the focus', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing?plan=plus');
	const plus = page.getByRole('button', { name: 'Choose Plus' });
	await expect(plus).toBeFocused();
	await expect(plus).toHaveClass(/(^|\s)hk-btn(\s|$)/);
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toHaveClass(
		/(^|\s)hk-btn-secondary(\s|$)/
	);
});

test('Solo costs $39 for 12 months on a first subscription, then $49; a returning account pays $49', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'solo' });
	await page.goto('/billing');
	const card = page.getByTestId('plan');
	await expect(card.locator('.plan-price .n')).toHaveText('$39');
	await expect(page.getByTestId('intro-until')).toContainText(/^then \$49 a month from \d{4}-/);
	await expect(page.getByTestId('next-charge')).toHaveText('$39.00');
	// The current card in Change plan shows the price the account pays.
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(
		page.getByTestId('change-plan').getByTestId('plan-solo').locator('.p-price .n')
	).toHaveText('$39');
	await setBilling({ mode: 'none', intro_used: true });
	await page.goto('/billing');
	const solo = page.getByTestId('plan-solo');
	await expect(solo.getByText('$49', { exact: true })).toBeVisible();
	await expect(page.getByTestId('intro-solo')).toHaveCount(0);
	await setBilling({ intro_used: false });
});

test('one seat free: Solo can be chosen, Plus and Pro say why not', async ({ page }) => {
	await setBilling({ mode: 'none', seats: { total: 30, held: 29, waiting: 0 } });
	await page.goto('/billing');
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toHaveCount(0);
	await expect(page.getByTestId('plan-plus')).toContainText('Needs 2 seats; 1 free.');
	await expect(page.getByTestId('plan-pro')).toContainText('Needs 4 seats; 1 free.');
});

test('three seats free: Solo and Plus can be chosen, Pro needs four', async ({ page }) => {
	await setBilling({ mode: 'none', seats: { total: 30, held: 27, waiting: 0 } });
	await page.goto('/billing');
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toHaveCount(0);
	await expect(page.getByTestId('plan-pro')).toContainText('Needs 4 seats; 3 free.');
});

test('choosing a plan opens the checkout and, once completed, the plan appears', async ({
	page
}) => {
	await setBilling({ mode: 'none' });
	await page.goto('/billing');
	// The fake's checkout page stands for Polar's: it completes the
	// checkout and sends the browser back on ?checkout=done (I-604).
	const checkout = page.waitForRequest(/\/polar\/checkout\/chk_fake_\d+$/);
	await page.getByRole('button', { name: 'Choose Plus' }).click();
	await checkout;
	await page.waitForURL(/\/billing(\?checkout=done)?$/);
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Plus' })).toBeVisible({
		timeout: 10_000
	});
	await expect(page.getByTestId('plan-status')).toContainText('Trial. First charge of $89 on');
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
	await expect(place).toContainText('41st');
	await expect(place).toContainText('of 41 waiting');
	await expect(place).toContainText('An email goes to dev@example.com when a seat frees.');
	await expect(place).toContainText(/Hold after invite\s*72 h\s*to choose a plan/);
	// The plans stay in view, and none can be bought while waiting.
	const waiting = page.getByRole('button', { name: 'Waiting for a seat' });
	await expect(waiting).toHaveCount(3);
	await expect(waiting.first()).toHaveAttribute('aria-disabled', 'true');
	// Reloading shows the same place: the api remembers it.
	await page.reload();
	await expect(page.getByTestId('waitlist-place')).toContainText('41st');
});

test('an invited user sees the held seat and the plans', async ({ page }) => {
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
	await expect(page.getByRole('button', { name: 'Choose Plus' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Choose Pro' })).toHaveCount(0);
	// The held seat counts as free, so the counts agree with the banner.
	await expect(page.getByTestId('seats-line')).toHaveText('Held for you: 1 seat');
	await expect(page.getByTestId('plan-plus')).toContainText('Needs 2 seats; 1 free.');
});

test('a trial shows the first charge date and the usage bars, and no project count', async ({
	page
}) => {
	await setBilling({ mode: 'trial', plan: 'solo', egress_gb: 300 });
	await page.goto('/billing');
	await expect(page.getByTestId('plan-status')).toContainText('Trial. First charge of $39 on');
	// The big figure is what the trial's first charge is, not the list price.
	await expect(page.getByTestId('plan').locator('.plan-price .n')).toHaveText('$39');
	await expect(page.getByTestId('intro-until')).toContainText('then $49 a month from');
	await expect(page.getByTestId('meter-disk-held')).toContainText('of 100 GB');
	const egress = page.getByTestId('meter-egress-this-period');
	// A Solo trial inside the founding price keeps the plan's 250 GB (I-672).
	await expect(egress).toContainText('300 GB of 250 GB');
	// The overage is said once, under its meter.
	await expect(page.getByTestId('egress-over')).toHaveText(
		'Over by 50 GB: $2.50 on the next invoice at $0.05 a GB.'
	);
	// Plans sell no project count (I-569): no meter, no row on the cards.
	await expect(page.getByTestId('projects-count')).toHaveCount(0);
	await expect(page.locator('dt', { hasText: /^Projects$/ })).toHaveCount(0);
});

test('the egress meter reads a thousand GB as 1 TB and This month marks the day of the period', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'pro', egress_gb: 42 });
	await page.goto('/billing');
	const egress = page.getByTestId('meter-egress-this-period');
	await expect(egress).toContainText('42 GB of 1 TB');
	// The period is the section's caption (I-837): the day and the reset.
	const month = page.locator('section', { has: page.getByRole('heading', { name: 'This month' }) });
	await expect(month.getByText(/^Day \d+ of \d+, resets \d{4}-\d{2}-\d{2}$/)).toBeVisible();
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
	// What is refused is a banner under the meters, one sentence (I-837).
	await expect(page.getByTestId('disk-over')).toHaveText(
		'Creating, restoring and forking projects, and growing a disk, are refused until your projects hold less.'
	);
	await setBilling({ disk_held_gb: 37.5 });
	await page.reload();
	await expect(disk).toContainText('37.5 GB of 100 GB');
	await expect(page.getByTestId('disk-over')).toHaveCount(0);
});

// Over on egress and disk (review r3 B1, B3): the next charge carries the
// overage, the egress note gives only the rate, and the projects that hold
// the most disk stay named while it is over.
test('over the plan, Next charge adds the egress and the disk names who holds the most', async ({
	page
}) => {
	const heavy = await createProject(apiURLFromEnv(), {
		name: 'heavy-app',
		remote_url: 'github.com/heracraft/heavy-app'
	});
	await setBilling({ mode: 'active', plan: 'solo', egress_gb: 312.4, disk_held_gb: 140 });
	await page.goto('/billing');
	await expect(page.getByTestId('next-charge')).toHaveText('$39.00');
	await expect(page.getByTestId('next-charge').locator('..')).toContainText(
		'$39.00 + $3.12 egress on'
	);
	await expect(page.getByTestId('egress-over')).toHaveText('Over by 62.4 GB at $0.05 a GB.');
	await expect(page.getByTestId('disk-over')).toBeVisible();
	await expect(
		page
			.getByTestId('meter-disk-held')
			.locator('..')
			.locator('.note', { hasText: /^Most: / })
	).toBeVisible();
	await destroyProject(apiURLFromEnv(), heavy.id);
});

test('over its limits at 390, Solo keeps every meter inside This month and the page narrow', async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await setBilling({ mode: 'active', plan: 'solo', disk_held_gb: 180, egress_gb: 412.7 });
	await page.goto('/billing');
	await expect(page.getByTestId('meter-disk-held')).toContainText('180 GB of 100 GB · over');
	await expect(page.getByTestId('meter-egress-this-period')).toContainText(
		'412.7 GB of 250 GB · over'
	);
	const fit = await page.evaluate(() => {
		const section = document.getElementById('month-h')!.closest('section')!.getBoundingClientRect();
		const meters = ['meter-running-now', 'meter-disk-held', 'meter-egress-this-period'].map(
			(id) => document.querySelector(`[data-testid="${id}"]`)!
		);
		const inside = meters.every((m) =>
			[m, ...m.querySelectorAll('*')].every((el) => {
				const r = el.getBoundingClientRect();
				return r.left >= section.left - 0.5 && r.right <= section.right + 0.5;
			})
		);
		return {
			inside,
			// The body's width leaves out the stable scrollbar gutter, which
			// the root's clientWidth counts on a desktop browser: the page
			// scrolls sideways only when its scroll width passes the body's.
			scrollWidth: document.documentElement.scrollWidth,
			bodyWidth: document.body.clientWidth
		};
	});
	expect(fit.inside).toBe(true);
	expect(fit.scrollWidth).toBeLessThanOrEqual(fit.bodyWidth);
	await setBilling({ disk_held_gb: -1, egress_gb: 0 });
});

test('an active plan shows its renewal, receipts through Polar, and invoices with PDF links', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'plus' });
	await page.goto('/billing');
	await expect(page).toHaveTitle('Billing · repose');
	// Status says Active once; Next charge carries the renewal date.
	await expect(page.getByTestId('plan-status')).toHaveText('Active.');
	await expect(page.getByTestId('plan')).toContainText(
		/Next charge\s*\$89\.00 on \d{4}-\d{2}-\d{2}/
	);
	await expect(page.getByTestId('plan')).not.toContainText('Latest invoice');
	const list = page.getByRole('table', { name: 'Invoices' });
	await expect(list.getByText('$89.00')).toBeVisible();
	await expect(list.getByText('REPOSE-0001', { exact: false })).toBeVisible();
	await expect(list.getByRole('link', { name: 'PDF' })).toHaveAttribute(
		'href',
		'https://sandbox-api.polar.sh/v1/orders/ord_fake_000001/invoice.pdf'
	);
	await page.route('https://sandbox.polar.sh/**', (route) =>
		route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Polar portal</h1>' })
	);
	await page.getByRole('button', { name: 'Card and receipts' }).click();
	await page.waitForURL(
		'https://sandbox.polar.sh/repose/portal?customer_session_token=polar_cst_fake'
	);
	await expect(page.getByRole('heading', { name: 'Polar portal' })).toBeVisible();
});

test('past due shows the failed payment and the card link; suspended says what happened', async ({
	page
}) => {
	await setBilling({ mode: 'past_due', plan: 'solo' });
	await page.goto('/billing');
	await expect(page.getByTestId('status-past-due')).toContainText('Your last payment failed.');
	// Polar has no deep link to the card: the button opens the portal (I-604).
	await page.route('https://sandbox.polar.sh/**', (route) =>
		route.fulfill({ status: 200, contentType: 'text/html', body: '<h1>Update card</h1>' })
	);
	await page.getByRole('button', { name: 'Update card' }).click();
	await page.waitForURL(/^https:\/\/sandbox\.polar\.sh\/repose\/portal\?/);

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
	// The plans open at page width in their own section under the plan
	// card, not squeezed inside it.
	const change = page.getByTestId('change-plan');
	await expect(change.getByRole('heading', { level: 2, name: 'Change plan' })).toBeVisible();
	await expect(page.getByTestId('plan').getByTestId('change-plan')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Change plan' })).toHaveAttribute(
		'aria-expanded',
		'true'
	);
	await expect(change).toContainText(
		'Upgrades apply now, prorated. Downgrades apply at renewal on'
	);
	await expect(change.getByTestId('plan-solo')).toContainText('Current');
	await expect(change.getByTestId('plan-plus')).toContainText('$89');
	// From Solo both other plans are upgrades.
	await expect(page.getByTestId('change-to-plus')).toHaveText('Upgrade to Plus');
	await expect(page.getByTestId('change-to-pro')).toHaveText('Upgrade to Pro');
	await expect(page.getByRole('button', { name: /Downgrade/ })).toHaveCount(0);
	await page.getByRole('button', { name: 'Upgrade to Plus' }).click();
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Plus' })).toBeVisible();

	// From Plus, one of each.
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toHaveText('Downgrade to Solo');
	await expect(page.getByTestId('change-to-pro')).toHaveText('Upgrade to Pro');
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
	// What runs now is set against each smaller plan, and a downgrade the
	// account does not fit is not offered: its button waits (I-837).
	await expect(page.getByTestId('change-plan').getByTestId('plan-solo')).toContainText(
		'Running 16 GB, allows 8 GB'
	);
	const down = page.getByRole('button', { name: 'Downgrade to Solo' });
	await expect(down).toHaveAttribute('aria-disabled', 'true');
	let asked = false;
	page.on('request', (r) => {
		if (r.method() === 'POST' && r.url().endsWith('/billing/plan')) asked = true;
	});
	// Forced: Playwright waits for an aria-disabled button to enable.
	await down.click({ force: true });
	await expect(page.getByTestId('change-error')).toHaveCount(0);
	expect(asked).toBe(false);
	// A refusal the page could not foresee (a machine started elsewhere)
	// is the api's sentence, under the plans.
	await failNext('POST', '/billing/plan', 'conflict');
	await page.getByRole('button', { name: 'Upgrade to Pro' }).click();
	await expect(page.getByTestId('change-error')).toBeVisible();
	await expect(page.getByTestId('plan').getByRole('heading', { name: 'Plus' })).toBeVisible();
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
		'Fits two xl, four large, or any mix.'
	);
	// Under the meter, used against the plan in words; the split per
	// project is on /projects.
	const split = page.getByTestId('meter-running-now').getByTestId('memory-split');
	await expect(split).toContainText('24 GB running');
	await expect(split).toContainText('8 GB left');
	await expect(split).not.toContainText('pro-a');
	await expect(page.getByRole('meter', { name: 'Memory running' })).toHaveAttribute(
		'aria-valuetext',
		'24 GB of 32 GB: 24 GB running, 8 GB left'
	);
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toHaveText('Downgrade to Solo');
	await expect(page.getByTestId('change-to-plus')).toHaveText('Downgrade to Plus');
	await expect(page.getByRole('button', { name: /Upgrade/ })).toHaveCount(0);
	// 24 GB runs and Plus allows 16: the card says so and its button waits.
	await expect(page.getByTestId('change-plan').getByTestId('plan-plus')).toContainText(
		'Running 24 GB, allows 16 GB'
	);
	const refused = page.getByRole('button', { name: 'Downgrade to Plus' });
	await expect(refused).toHaveAttribute('aria-disabled', 'true');
	// The reason on the card is the button's description.
	await expect(refused).toHaveAccessibleDescription('Running 24 GB, allows 16 GB');
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
	// The renewal charges Plus, not Pro.
	await expect(page.getByTestId('next-charge')).toHaveText('$89.00');
	// With Plus scheduled, the Status fact offers to keep Pro (I-837), and
	// Change plan still lists Solo and says from when Plus applies.
	await expect(page.getByTestId('change-keep')).toHaveText('Keep Pro');
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toHaveText('Downgrade to Solo');
	await expect(page.getByTestId('change-to-plus')).toHaveCount(0);
	await expect(page.getByTestId('change-plan').getByTestId('plan-plus')).toContainText('From ');
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
	await expect(page.getByTestId('plan-status')).toHaveText('Active.');
	// Resume plan is gone; focus goes to Cancel plan, not <body>.
	await expect(page.getByRole('button', { name: 'Cancel plan' })).toBeFocused();
});

// Last in the file: the fake keeps an invoice list once one is set.
test('invoices come six to a page, newest first, with Newer and Older', async ({ page }) => {
	const invoices = Array.from({ length: 14 }, (_, i) => {
		const day = new Date(Date.UTC(2026, 9, 11) - i * 30 * 86_400_000).toISOString();
		return {
			id: `ord_page_${i}`,
			number: `REPOSE-${String(14 - i).padStart(4, '0')}`,
			status: 'paid',
			currency: 'USD',
			amount_cents: 5900,
			subtotal_cents: 5900,
			tax_cents: 0,
			created_at: day,
			period_start: day,
			period_end: day,
			pdf_url: `https://example.com/${i}.pdf`
		};
	});
	await setBilling({ mode: 'active', plan: 'plus', invoices });
	await page.goto('/billing');
	const table = page.getByRole('table', { name: 'Invoices' });
	const pages = page.getByRole('navigation', { name: 'Invoice pages' });
	await expect(table.locator('tbody tr')).toHaveCount(6);
	await expect(table.locator('tbody tr').first()).toContainText('REPOSE-0014');
	await expect(pages).toContainText('1–6 of 14');
	await expect(pages.getByRole('button', { name: 'Newer' })).toBeDisabled();
	await pages.getByRole('button', { name: 'Older' }).click();
	await expect(pages).toContainText('7–12 of 14');
	await expect(table.locator('tbody tr').first()).toContainText('REPOSE-0008');
	await pages.getByRole('button', { name: 'Older' }).click();
	await expect(pages).toContainText('13–14 of 14');
	await expect(table.locator('tbody tr')).toHaveCount(2);
	await expect(pages.getByRole('button', { name: 'Older' })).toBeDisabled();
	await pages.getByRole('button', { name: 'Newer' }).click();
	await expect(pages).toContainText('7–12 of 14');
	await setBilling({ invoices: [] });
});

// A plan that was paid for and never arrived is the one state where help
// is genuinely owed: the card is charged and no machine can start. The
// line used to end on "support will sort it out", naming nobody the
// reader could reach (I-767).
test('a plan that never arrives points at the support address', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await page.clock.install();
	await page.goto('/billing?checkout=done');
	await expect(page.getByTestId('setting-up')).toBeVisible();

	// Thirty rounds of 2s, and the webhook never lands. Each round awaits
	// a real GET /billing, so the clock is stepped a round at a time
	// rather than jumped: one jump fires the timers and leaves thirty
	// fetches unresolved.
	for (let i = 0; i < 32; i++) {
		await page.clock.runFor(2100);
		await page.waitForTimeout(20);
	}

	const banner = page.getByText('The plan has not arrived.');
	await expect(banner).toBeVisible();
	await expect(page.getByTestId('support-link')).toHaveAttribute(
		'href',
		'mailto:support@herakraft.co'
	);
	// Not the feedback board, which is for feedback.
	await expect(page.getByTestId('support-link')).not.toHaveAttribute('href', /fider/);
	// The payment went through: the banner stands alone, with no second checkout.
	await expect(page.locator('[id^="choose-"]')).toHaveCount(0);
	await expect(page.getByRole('list', { name: 'Plans' })).toHaveCount(0);
});

// I-841: the plan lives in the rail's foot from 1024px, one state line per
// billing mode, and below 1024px as one line under the strip only when
// something needs doing. /billing is the plan's own page and shows neither.
test.describe('the plan outside /billing', () => {
	const calm = { seats: { total: 30, held: 12, waiting: 0 }, waitlist: { position: 0 } };
	const full = { total: 30, held: 30, waiting: 5 };
	const day = /\d{4}-\d{2}-\d{2}/;
	/** [mode, state, foot's name line, foot's state line, phone's line link or none] */
	const rows: [string, BillingState, string | undefined, RegExp, string | undefined][] = [
		['exempt', { mode: 'exempt' }, undefined, /^\d+(\.\d)? GB$/, undefined],
		['none', { mode: 'none' }, 'No plan', /^Choose a plan$/, 'Choose a plan'],
		[
			'waitlist',
			{ mode: 'none', seats: full, waitlist: { position: 3 } },
			'No plan',
			/^On the waitlist$/,
			'Billing'
		],
		['full', { mode: 'none', seats: full }, 'No plan', /^repose is full$/, 'Billing'],
		[
			'held',
			{ mode: 'none', waitlist: { position: 1, invited: true } },
			'No plan',
			/^Seat held$/,
			'Billing'
		],
		[
			'trial',
			{ mode: 'trial', plan: 'solo' },
			'Solo',
			new RegExp(`^Trial until ${day.source}$`),
			undefined
		],
		[
			'active',
			{ mode: 'active', plan: 'pro' },
			'Pro',
			/^\d+ GB of 32 GB running( · over)?$/,
			undefined
		],
		[
			'cancelled',
			{ mode: 'active', plan: 'pro', cancelled: true },
			'Pro',
			new RegExp(`^Ends ${day.source}$`),
			undefined
		],
		[
			'scheduled',
			{ mode: 'active', plan: 'pro', scheduled_plan: 'plus' },
			'Pro',
			new RegExp(`^Changes to Plus ${day.source}$`),
			undefined
		],
		['past_due', { mode: 'past_due', plan: 'pro' }, 'Pro', /^Payment failed$/, 'Update card'],
		['suspended', { mode: 'suspended', plan: 'pro' }, 'Pro', /^Suspended$/, 'Update card and pay']
	];

	for (const [mode, state, name, line, phone] of rows) {
		test(`${mode}: the rail foot and the phone's plan line`, async ({ page }) => {
			await setBilling({ ...calm, scheduled_plan: '', cancelled: false, ...state });
			await page.setViewportSize({ width: 1440, height: 900 });
			await page.goto('/projects');
			const foot = page.getByTestId('plan-strip');
			await expect(foot).toBeVisible();
			const lines = foot.locator(':scope > *');
			await expect(lines.first()).toHaveText(mode === 'exempt' ? 'Running' : 'Plan');
			// The state line leads with the err dot's space when it has one.
			if (name) await expect(foot).toContainText(name);
			await expect(lines.last()).toHaveText(new RegExp(`^\\s*${line.source.slice(1)}`));
			// A state that needs the user links to /billing; the calm ones are text.
			const link = foot.getByRole('link');
			if (phone) await expect(link).toHaveAttribute('href', '/billing');
			else await expect(link).toHaveCount(0);

			await page.setViewportSize({ width: 390, height: 844 });
			await expect(foot).toBeHidden();
			const strip = page.getByRole('navigation', { name: 'Dashboard' });
			await expect(strip.getByRole('link', { name: 'Billing' })).toBeInViewport();
			const planLine = page.locator('.plan-line');
			if (phone) {
				await expect(planLine.getByRole('link', { name: phone, exact: true })).toHaveAttribute(
					'href',
					'/billing'
				);
			} else {
				await expect(planLine.getByRole('link')).toHaveCount(0);
			}

			// The plan's own page shows neither.
			await page.goto('/billing');
			await expect(page.getByRole('heading', { level: 1, name: 'Billing' })).toBeVisible();
			await expect(page.locator('.plan-line a')).toHaveCount(0);
			await page.setViewportSize({ width: 1440, height: 900 });
			await expect(page.getByTestId('plan-strip')).toHaveCount(0);
		});
	}

	test('billing off: no foot and no line', async ({ page }) => {
		await page.goto('/projects');
		await expect(page.getByRole('heading', { level: 1, name: 'Projects' })).toBeVisible();
		await page.waitForLoadState('networkidle');
		await expect(page.getByTestId('plan-strip')).toHaveCount(0);
		await expect(page.locator('.plan-line a')).toHaveCount(0);
	});

	// /billing hands its own reads to the shared store, so the foot says
	// what the page just did without waiting for the rail's 60s read.
	test('cancelling on /billing shows in the rail foot on the next page', async ({ page }) => {
		await setBilling({
			...calm,
			mode: 'active',
			plan: 'solo',
			cancelled: false,
			scheduled_plan: ''
		});
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/projects');
		const foot = page.getByTestId('plan-strip');
		await expect(foot).toContainText(/running/);
		const rail = page.getByRole('navigation', { name: 'Dashboard' });
		await rail.getByRole('link', { name: 'Billing' }).click();
		await expect(page).toHaveURL('/billing');
		await page.getByRole('button', { name: 'Cancel plan' }).click();
		await page.getByTestId('confirm-cancel').getByRole('button', { name: 'Cancel plan' }).click();
		await expect(page.getByTestId('plan-status')).toContainText('Cancelled. Ends');
		await rail.getByRole('link', { name: 'Projects', exact: true }).click();
		await expect(page).toHaveURL('/projects');
		await expect(foot).toContainText(new RegExp(`Ends ${day.source}`), { timeout: 2000 });
		// Resume, back on /billing, reaches it the same way.
		await rail.getByRole('link', { name: 'Billing' }).click();
		await page.getByRole('button', { name: 'Resume plan' }).click();
		await expect(page.getByTestId('plan-status')).toHaveText('Active.');
		await rail.getByRole('link', { name: 'Projects', exact: true }).click();
		await expect(foot).toContainText(/GB of 8 GB running/, { timeout: 2000 });
	});
});
