// docs/workstreams/08-dashboard.md §5.8 and DECISIONS I-289/I-290: the
// billing page sells three plans through Polar, gates them on seats, and
// shows a subscription's status, usage of the plan and its invoices.
import { test, expect } from '@playwright/test';
import {
	failNext,
	signIn,
	setBilling,
	resetBilling,
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
	await expect(page.getByTestId('billing-disabled')).toHaveText('Billing is not switched on yet');
	// A state in a card, not a stray line.
	await expect(page.getByTestId('billing-disabled')).toHaveClass(/\bcard\b/);
	await expect(page.getByRole('button', { name: /Choose/ })).toHaveCount(0);
	// billing_disabled is a 503 the api gives as an answer, not an outage,
	// so no outage bar sits over the page (I-390).
	await expect(page.getByText(/Retrying…/)).toHaveCount(0);
});

test('a failed first load says so and Retry loads the page', async ({ page }) => {
	await setBilling({ mode: 'none' });
	// Loaded once first: the projects page that signIn lands on reads
	// GET /billing for its plan strip (I-664) and could take the one
	// failure; after this the reload is the only request for it.
	await page.goto('/billing');
	await expect(page.getByRole('heading', { level: 2, name: 'Solo' })).toBeVisible();
	await failNext('GET', '/billing', 'internal');
	await page.reload();
	const failed = page.getByRole('alert');
	await expect(failed).toBeVisible();
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
	await expect(solo.getByTestId('intro-solo')).toHaveText('$39 for 12 months');
	await expect(solo.getByText('then $49 from month 13')).toBeVisible();
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
	await expect(page.locator('[data-testid^="plan-"] .btn')).toHaveCount(0);
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
	await expect(plus).toHaveClass(/\bbtn\b/);
	await expect(page.getByRole('button', { name: 'Choose Solo' })).toHaveClass(/btn-quiet/);
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
	await expect(egress).toContainText('Over by 50 GB: $2.50 on the next invoice at $0.05 a GB.');
	// Plans sell no project count (I-569): no meter, no row on the cards.
	await expect(page.getByTestId('projects-count')).toHaveCount(0);
	await expect(page.locator('dt', { hasText: /^Projects$/ })).toHaveCount(0);
});

test('the egress meter reads a thousand GB as 1 TB and the facts mark the day of the period', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'pro', egress_gb: 42 });
	await page.goto('/billing');
	const egress = page.getByTestId('meter-egress-this-period');
	await expect(egress).toContainText('42 GB of 1 TB');
	await expect(page.locator('.facts').getByText(/^Day \d+ of \d+$/)).toBeVisible();
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

test('over its limits at 390, Solo keeps every usage row inside the plan card', async ({
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
		const card = document.querySelector('[data-testid="plan"]')!.getBoundingClientRect();
		const rows = [...document.querySelectorAll('[data-testid="plan"] .u-row')];
		const inside = rows.every((row) =>
			[row, ...row.querySelectorAll('*')].every((el) => {
				const r = el.getBoundingClientRect();
				return r.left >= card.left - 0.5 && r.right <= card.right + 0.5;
			})
		);
		return {
			rows: rows.length,
			inside,
			// The body's width leaves out the stable scrollbar gutter, which
			// the root's clientWidth counts on a desktop browser: the page
			// scrolls sideways only when its scroll width passes the body's.
			scrollWidth: document.documentElement.scrollWidth,
			bodyWidth: document.body.clientWidth
		};
	});
	expect(fit.rows).toBe(2);
	expect(fit.inside).toBe(true);
	expect(fit.scrollWidth).toBeLessThanOrEqual(fit.bodyWidth);
	await setBilling({ disk_held_gb: -1, egress_gb: 0 });
});

test('an active plan shows its renewal, receipts through Polar, and invoices with PDF links', async ({
	page
}) => {
	await setBilling({ mode: 'active', plan: 'plus' });
	await page.goto('/billing');
	await expect(page.getByTestId('plan-status')).toContainText('Active. Renews');
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
	await page.getByRole('button', { name: /Manage card and receipts/ }).click();
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
	// What runs now is set against each smaller plan, in amber.
	await expect(page.getByTestId('change-plan').getByTestId('plan-solo')).toContainText(
		'Running 16 GB, holds 8 GB'
	);
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
	// What runs is listed under the meter, each with its size.
	await expect(page.getByRole('list', { name: 'Running now' })).toContainText('pro-a');
	await page.getByRole('button', { name: 'Change plan' }).click();
	await expect(page.getByTestId('change-to-solo')).toHaveText('Downgrade to Solo');
	await expect(page.getByTestId('change-to-plus')).toHaveText('Downgrade to Plus');
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
	await expect(page.getByTestId('plan-status')).toContainText('Active. Renews');
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
});
