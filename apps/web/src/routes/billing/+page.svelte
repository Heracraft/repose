<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		getMe,
		getBilling,
		billingCheckout,
		billingJoinWaitlist,
		billingChangePlan,
		billingCancel,
		billingResume,
		billingPortal,
		billingInvoices
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { reachability } from '$lib/api/reachability.svelte';
	import { openCheckout, pageTheme } from '$lib/paddle';
	import { money, price, gbs, dateOnly, dateTime, timeUntil } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import Meter from '$lib/components/Meter.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { Billing, Invoice, Me, Plan, PlanId, Subscription } from '$lib/api/types';

	let me = $state<Me | undefined>(undefined);
	let billing = $state<Billing | undefined>(undefined);
	let billingDisabled = $state(false);
	let loadError = $state<string | undefined>(undefined);
	let invoices = $state<Invoice[]>([]);

	// After Paddle's overlay reports the checkout done (or the user comes
	// back on ?checkout=done), the subscription is still on its way by
	// webhook: "Setting up your plan" polls until it is there (I-289).
	let settingUp = $state(false);
	let setupTimedOut = $state(false);

	let busy = $state<string | undefined>(undefined);
	let changing = $state(false);
	let changeError = $state<string | undefined>(undefined);
	let confirmCancel = $state(false);

	let sub = $derived(billing?.subscription ?? null);
	let plan = $derived<Plan | undefined>(
		billing && sub ? billing.plans.find((p) => p.id === sub.plan) : undefined
	);
	// Every plan but the current one, in the api's order (solo, plus, pro):
	// more seats is an upgrade, fewer a downgrade (docs/PRICING.md).
	let otherPlans = $derived<Plan[]>(
		billing && sub ? billing.plans.filter((p) => p.id !== sub.plan) : []
	);
	let scheduledPlan = $derived<Plan | undefined>(
		billing && sub?.scheduled_plan
			? billing.plans.find((p) => p.id === sub.scheduled_plan)
			: undefined
	);
	let holdActive = $derived(
		!!billing?.waitlist?.hold_until && new Date(billing.waitlist.hold_until).getTime() > Date.now()
	);
	let anyAvailable = $derived(!!billing?.plans.some((p) => p.available));
	// The three plan cards need the list width; everything else reads at
	// the form's.
	let showCards = $derived(
		!!billing && !sub && !settingUp && (setupTimedOut || holdActive || anyAvailable)
	);
	let accountStatus = $derived(me?.billing.status);
	// What the projects hold, which the plan's disk counts (I-585); an api
	// older than that sends only disk_allocated_gb.
	let diskHeld = $derived(
		billing ? (billing.usage.disk_held_gb ?? billing.usage.disk_allocated_gb) : 0
	);

	async function load() {
		// One failure, one report (DESIGN-LANGUAGE "Toasts", I-395): an
		// account that did not load is toasted only when the billing call
		// answered, since an outage fails both and the load banner or the
		// outage bar already says so.
		let meError: unknown;
		const reportMe = () => {
			if (meError !== undefined && reachability.ok) {
				toastApiError(meError, 'Could not load the account.');
			}
		};
		try {
			me = await getMe();
		} catch (err) {
			meError = err;
		}
		try {
			billing = await getBilling();
			billingDisabled = false;
			loadError = undefined;
			reportMe();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'billing_disabled') {
				billingDisabled = true;
				reportMe();
			} else if (billing) {
				// A refresh after an action: what is on screen stays, and the
				// toast says the refresh failed.
				toastApiError(err, 'Could not load billing.');
			} else {
				loadError = loadErrorText(err, 'Could not load billing.');
			}
			return;
		}
		if (billing.subscription) {
			try {
				invoices = await billingInvoices();
			} catch (err) {
				toastApiError(err, 'Could not load invoices.');
			}
		}
	}

	async function waitForSubscription() {
		settingUp = true;
		setupTimedOut = false;
		for (let i = 0; i < 30; i++) {
			await new Promise((r) => setTimeout(r, 2000));
			try {
				billing = await getBilling();
			} catch {
				// A blip while the webhook lands; the next round asks again.
			}
			if (billing?.subscription) {
				settingUp = false;
				void load();
				return;
			}
		}
		settingUp = false;
		setupTimedOut = true;
	}

	function returnedFromCheckout() {
		const url = new URL(location.href);
		if (url.searchParams.get('checkout') !== 'done') return;
		url.searchParams.delete('checkout');
		history.replaceState(history.state, '', url.pathname + url.search + url.hash);
		if (!billing?.subscription) void waitForSubscription();
	}

	onMount(async () => {
		await load();
		returnedFromCheckout();
	});

	async function choose(planId: PlanId) {
		if (!billing) return;
		busy = `choose-${planId}`;
		try {
			const checkout = await billingCheckout(planId);
			await openCheckout({
				transactionId: checkout.transaction_id,
				clientToken: checkout.client_token,
				environment: checkout.environment,
				theme: pageTheme(),
				successUrl: `${location.origin}${resolve('/billing')}?checkout=done`,
				onCompleted: () => void waitForSubscription()
			});
		} catch (err) {
			if (err instanceof ApiError && err.code === 'waitlisted') {
				// The seat went while the page was open: the api put the
				// user on the list, and the page shows that place.
				await load();
			} else if (err instanceof ApiError) {
				toastApiError(err);
			} else {
				toast.error(err instanceof Error ? err.message : 'Could not open the checkout.');
			}
		} finally {
			busy = undefined;
		}
	}

	async function joinWaitlist() {
		busy = 'waitlist';
		try {
			await billingJoinWaitlist();
			await load();
		} catch (err) {
			toastApiError(err, 'Could not join the waitlist.');
		} finally {
			busy = undefined;
		}
	}

	async function changePlan(to: PlanId) {
		busy = `plan-${to}`;
		changeError = undefined;
		try {
			await billingChangePlan(to);
			changing = false;
			await load();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'conflict') changeError = err.message;
			else toastApiError(err, 'Could not change the plan.');
		} finally {
			busy = undefined;
		}
	}

	async function cancel() {
		busy = 'cancel';
		try {
			await billingCancel();
			confirmCancel = false;
			await load();
			// Cancel plan is gone once the plan is cancelled; Resume plan
			// takes its place and the focus with it.
			void focusAfterRender('resume-plan', 'cancel-plan');
		} catch (err) {
			toastApiError(err, 'Could not cancel the plan.');
		} finally {
			busy = undefined;
		}
	}

	async function resume() {
		busy = 'resume';
		try {
			await billingResume();
			await load();
			// The mirror of cancel(): Resume plan is gone once cancel_at
			// clears, so Cancel plan takes the focus.
			void focusAfterRender('cancel-plan');
		} catch (err) {
			toastApiError(err, 'Could not resume the plan.');
		} finally {
			busy = undefined;
		}
	}

	async function openPortal(what?: 'payment_method') {
		busy = what ?? 'portal';
		try {
			const { url } = await billingPortal(what);
			location.href = url;
		} catch (err) {
			busy = undefined;
			toastApiError(err, 'Could not open the billing portal.');
		}
	}

	const counts = ['none', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight'];
	const count = (n: number) => counts[n] ?? String(n);
	// Egress allowances read as the pricing table writes them: 1000 GB is 1 TB.
	const allowance = (gb: number) =>
		gb >= 1000 && gb % 1000 === 0 ? `${gb / 1000} TB` : `${gb} GB`;

	/** "one large, or two small" for a plan's memory (docs/PRICING.md):
	 * xl 16 GB, large 8, small 4. */
	function runsAtOnce(p: Plan): string {
		const xl = Math.floor(p.memory_gb / 16);
		const large = Math.floor(p.memory_gb / 8);
		if (xl > 0) return `${count(xl)} xl, ${count(large)} large, or any mix`;
		return `${count(large)} large, or ${count(Math.floor(p.memory_gb / 4))} small`;
	}

	/** "Trial. First charge of $20 on 12 Oct; $29 a month from 12 Jan." (I-497) */
	function trialLine(sub: Subscription, plan: Plan): string {
		const first = `Trial. First charge of ${price(sub.next_charge_cents ?? plan.price_cents)} on ${dateOnly(sub.trial_end!)}`;
		return sub.intro_until
			? `${first}; ${price(plan.price_cents)} a month from ${dateOnly(sub.intro_until)}.`
			: `${first}.`;
	}

	/** The renewal while the introductory price runs, "" otherwise (I-497). */
	function introRenewal(sub: Subscription, plan: Plan): string {
		if (!sub.intro_until || !sub.next_billed_at || sub.next_charge_cents == null) return '';
		if (sub.next_charge_cents >= plan.price_cents) return '';
		return `Active. Renews ${dateOnly(sub.next_billed_at)} at ${price(sub.next_charge_cents)}; ${price(plan.price_cents)} a month from ${dateOnly(sub.intro_until)}.`;
	}

	/** The plan's introductory price applies to this user's checkout (I-497). */
	const intro = (p: Plan) => !!billing?.intro_eligible && !!p.intro_months && !!p.intro_price_cents;
</script>

<svelte:head>
	<title>Billing — repose</title>
</svelte:head>

{#snippet planCards(plans: Plan[])}
	<ul class="plans" aria-label="Plans">
		{#each plans as p (p.id)}
			<li class="card flex flex-col" data-testid="plan-{p.id}">
				<div class="flex items-baseline justify-between gap-3">
					<h2 class="text-xl font-semibold">{p.name}</h2>
					<p class="text-sm">
						<span class="font-display text-2xl font-semibold tabular-nums"
							>{price(intro(p) ? p.intro_price_cents! : p.price_cents)}</span
						>
						<span class="text-ink-muted"> a month</span>
					</p>
				</div>
				{#if intro(p)}
					<p class="mt-1 text-right text-sm text-balance text-ink-muted" data-testid="intro-{p.id}">
						For {p.intro_months} months, then {price(p.price_cents)} and {allowance(p.egress_gb)} egress
					</p>
				{/if}
				<dl class="mt-4 space-y-1.5 text-sm tabular-nums">
					<div class="flex justify-between gap-4">
						<dt class="whitespace-nowrap text-ink-muted">Running at once</dt>
						<dd class="text-right text-balance">{p.memory_gb} GB: {runsAtOnce(p)}</dd>
					</div>
					<div class="flex justify-between gap-4">
						<dt class="whitespace-nowrap text-ink-muted">Disk</dt>
						<dd>{p.disk_gb} GB</dd>
					</div>
					<div class="flex justify-between gap-4">
						<dt class="whitespace-nowrap text-ink-muted">Egress a month</dt>
						<dd>{allowance(intro(p) && p.intro_egress_gb ? p.intro_egress_gb : p.egress_gb)}</dd>
					</div>
				</dl>
				<p class="mt-4 text-sm text-pretty text-ink-muted">
					{p.trial_days} days free, card at checkout, cancel any time.
				</p>
				<!-- The note sits above the button so the buttons line up across
				     the cards whether or not one has a note. -->
				<div class="mt-auto pt-5">
					{#if !p.available && billing}
						<p class="mb-2 text-xs text-ink-muted">
							Needs {p.seats} seats; {billing.seats.free} free.
						</p>
					{/if}
					<button
						type="button"
						class="btn w-full"
						disabled={!!busy || !p.available}
						onclick={() => choose(p.id)}
					>
						{busy === `choose-${p.id}` ? 'Opening checkout…' : `Choose ${p.name}`}
					</button>
				</div>
			</li>
		{/each}
	</ul>
{/snippet}

<PageShell title="Billing" width={showCards ? 'list' : 'form'}>
	{#if billingDisabled}
		<p class="text-sm text-ink-muted" data-testid="billing-disabled">
			Billing is not switched on yet.
		</p>
	{:else if !billing}
		<LoadState status={loadError ? 'failed' : 'loading'} error={loadError} onretry={load} />
	{:else if settingUp}
		<div class="card" data-testid="setting-up" aria-live="polite">
			<h2 class="text-xl font-semibold">Setting up your plan</h2>
			<p class="mt-2 text-sm text-ink-muted">
				Your payment went through. The plan appears here in a few seconds.
			</p>
		</div>
	{:else if setupTimedOut && !sub}
		<div class="banner banner--warn">
			The plan is taking longer than usual to arrive. Refresh in a minute; if it is still not here,
			the payment went through and support will sort it out.
		</div>
		{@render planCards(billing.plans)}
	{:else if !sub}
		{#if accountStatus === 'exempt'}
			<div class="banner banner--ok">This account is billing-exempt. No plan is needed.</div>
		{/if}
		{#if holdActive && billing.waitlist?.hold_until}
			<div class="banner banner--ok" data-testid="seat-held">
				Your seat is held until {dateTime(billing.waitlist.hold_until)} ({timeUntil(
					billing.waitlist.hold_until
				)} left). Choose a plan before then.
			</div>
			{@render planCards(billing.plans)}
		{:else if anyAvailable}
			<p class="mb-5 text-sm text-ink-muted tabular-nums" data-testid="seats-line">
				{billing.seats.free}
				{billing.seats.free === 1 ? 'seat' : 'seats'} left.
			</p>
			{@render planCards(billing.plans)}
		{:else}
			<div class="card" data-testid="full">
				<h2 class="text-xl font-semibold">repose is full</h2>
				<p class="mt-2 text-sm text-ink-muted">
					Every seat is taken and {billing.seats.waiting}
					{billing.seats.waiting === 1 ? 'person is' : 'people are'} waiting. A seat frees when a plan
					ends.
				</p>
				{#if billing.waitlist}
					<p class="mt-4 text-sm" data-testid="waitlist-place">
						You're number <span class="font-semibold tabular-nums">{billing.waitlist.position}</span
						>
						on the waitlist. We'll email
						{me?.email ?? 'you'} when a seat frees; you'll have 72 hours to choose a plan.
					</p>
				{:else}
					<button
						type="button"
						class="btn mt-4"
						disabled={!!busy}
						onclick={joinWaitlist}
						data-testid="join-waitlist"
					>
						{busy === 'waitlist' ? 'Joining…' : 'Join the waitlist'}
					</button>
				{/if}
			</div>
		{/if}
		<p class="mt-8 text-xs text-ink-muted">
			Prices in USD before tax; Paddle adds the tax for your country at checkout.
			<a href={resolve('/refunds')} class="link">Refunds</a>.
		</p>
	{:else if plan}
		{#if accountStatus === 'suspended'}
			<div class="banner banner--error" data-testid="status-suspended">
				Your account is suspended: the payment failed three days ago and your machines were stopped.
				Once the invoice is paid you can start them again; snapshots are kept 30 days.
				<button
					type="button"
					class="link mt-2 block"
					disabled={!!busy}
					onclick={() => openPortal('payment_method')}>Update card and pay</button
				>
			</div>
		{:else if sub.status === 'past_due'}
			<div class="banner banner--error" data-testid="status-past-due">
				Your last payment failed. Machines already running keep running; new starts wait until the
				card is updated, and after three days every machine is stopped.
				<button
					type="button"
					class="link mt-2 block"
					disabled={!!busy}
					onclick={() => openPortal('payment_method')}>Update card</button
				>
			</div>
		{/if}

		<section class="card" data-testid="plan">
			<div class="flex flex-wrap items-baseline justify-between gap-3">
				<h2 class="text-xl font-semibold">{plan.name}</h2>
				<p class="text-sm">
					<span class="font-display text-2xl font-semibold tabular-nums"
						>{price(plan.price_cents)}</span
					>
					<span class="text-ink-muted"> a month</span>
				</p>
			</div>
			<p class="mt-2 text-sm text-ink-muted" data-testid="plan-status">
				{#if sub.cancel_at}
					Cancelled. Ends {dateOnly(sub.cancel_at)}; machines stop then and snapshots stay 30 days.
				{:else if sub.status === 'trialing' && sub.trial_end}
					{trialLine(sub, plan)}
				{:else if sub.status === 'active' && introRenewal(sub, plan)}
					{introRenewal(sub, plan)}
				{:else if sub.status === 'past_due'}
					Payment past due since {dateOnly(sub.period_start)}.
				{:else if sub.next_billed_at}
					Active. Renews {dateOnly(sub.next_billed_at)}.
				{:else}
					Active.
				{/if}
				{#if scheduledPlan}
					Changes to {scheduledPlan.name} on {dateOnly(sub.period_end)}.
				{/if}
			</p>

			<div class="mt-6 space-y-5">
				<Meter
					label="Running now"
					used={billing.usage.running_gb}
					limit={billing.usage.memory_gb}
					format={gbs}
					note={runsAtOnce(plan)}
				/>
				<Meter
					label="Disk held"
					used={diskHeld}
					limit={billing.usage.disk_gb}
					format={gbs}
					note={diskHeld > billing.usage.disk_gb
						? 'Creating, restoring and forking projects, and growing a disk, wait until your projects hold less. A deleted file stops counting within a day, or when its machine stops.'
						: undefined}
				/>
				<Meter
					label="Egress this period"
					used={billing.usage.egress_gb}
					limit={billing.usage.egress_included_gb}
					format={gbs}
					note={billing.usage.overage_cents > 0
						? `Over by ${gbs(billing.usage.egress_gb - billing.usage.egress_included_gb)}: ${money(billing.usage.overage_cents)} on the next invoice at $0.05 a GB.`
						: undefined}
				/>
			</div>

			<div class="mt-6 flex flex-wrap gap-x-5 gap-y-2 border-t pt-4 border-rule">
				{#if sub.cancel_at}
					<button type="button" id="resume-plan" class="btn" disabled={!!busy} onclick={resume}>
						{busy === 'resume' ? 'Resuming…' : 'Resume plan'}
					</button>
				{:else}
					<button
						type="button"
						class="btn-quiet"
						disabled={!!busy}
						onclick={() => {
							changing = !changing;
							changeError = undefined;
							confirmCancel = false;
						}}>Change plan</button
					>
					<!-- The documented two-step (DESIGN-LANGUAGE.md, "Confirmation"):
					     the button turns into the question below, and Keep it
					     brings it back with the focus on it (I-393). One panel at a
					     time: opening the question closes Change plan and the reverse,
					     so the question sits right under the row, not under the plan
					     list. -->
					{#if !confirmCancel}
						<button
							type="button"
							id="cancel-plan"
							class="btn-ghost-danger"
							disabled={!!busy}
							onclick={() => {
								changing = false;
								confirmCancel = true;
							}}>Cancel plan</button
						>
					{/if}
				{/if}
				<button
					type="button"
					class="btn-ghost ml-auto px-0"
					disabled={!!busy}
					onclick={() => openPortal()}>Manage card and receipts →</button
				>
			</div>

			{#if changing && otherPlans.length}
				<div class="mt-4 rounded-sm border text-sm border-rule" data-testid="change-plan">
					{#if scheduledPlan}
						<div class="change-row p-4" data-testid="change-keep">
							<p>
								{scheduledPlan.name} is scheduled for {dateOnly(sub.period_end)}. Keep {plan.name}
								instead?
							</p>
							<button
								type="button"
								class="btn mt-3"
								disabled={!!busy}
								onclick={() => changePlan(sub.plan)}
							>
								{busy === `plan-${sub.plan}` ? 'Saving…' : `Keep ${plan.name}`}
							</button>
						</div>
					{/if}
					{#each otherPlans.filter((p) => p.id !== scheduledPlan?.id) as other (other.id)}
						{@const up = other.seats > plan.seats}
						<div class="change-row p-4" data-testid="change-to-{other.id}">
							<p>
								{up ? 'Upgrade' : 'Downgrade'} to <span class="font-semibold">{other.name}</span>
								({price(other.price_cents)} a month:
								{other.memory_gb} GB of memory for running machines, {other.disk_gb} GB disk,
								{allowance(other.egress_gb)} egress).
								{#if up}
									Takes effect at once; Paddle prorates the rest of this period.
								{:else}
									Takes effect at the renewal on {dateOnly(sub.period_end)}; what runs and what your
									projects hold have to fit it first.
								{/if}
							</p>
							<button
								type="button"
								class="{up ? 'btn' : 'btn-quiet'} mt-3"
								disabled={!!busy}
								onclick={() => changePlan(other.id)}
							>
								{#if busy === `plan-${other.id}`}
									{up ? 'Upgrading…' : 'Scheduling…'}
								{:else}
									{up ? 'Upgrade' : 'Downgrade'} to {other.name}
								{/if}
							</button>
						</div>
					{/each}
					{#if changeError}
						<p class="field-error px-4 pb-4" data-testid="change-error">{changeError}</p>
					{/if}
				</div>
			{/if}

			{#if confirmCancel}
				<div class="mt-4 text-sm" data-testid="confirm-cancel">
					<p class="text-ink-muted">
						Cancel the plan? It ends on {dateOnly(
							sub.status === 'trialing' && sub.trial_end ? sub.trial_end : sub.period_end
						)}. Machines run until then and stop at it; snapshots are kept 30 days after. Nothing is
						charged after that.
					</p>
					<div class="mt-2 flex items-center gap-2">
						<button type="button" class="btn-danger btn--sm" disabled={!!busy} onclick={cancel}>
							{busy === 'cancel' ? 'Cancelling…' : 'Cancel plan'}
						</button>
						<!-- Focus lands on the safe choice when the question opens. -->
						<button
							type="button"
							class="btn-ghost"
							use:focusOnMount
							onclick={() => {
								confirmCancel = false;
								void focusAfterRender('cancel-plan');
							}}>Keep it</button
						>
					</div>
				</div>
			{/if}
		</section>

		<div class="form-section">
			<h2 class="text-xl font-semibold">Invoices</h2>
			{#if invoices.length === 0}
				<p class="mt-2 text-sm text-ink-muted">No invoices yet.</p>
			{:else}
				<ul class="mt-2" aria-label="Invoices">
					{#each invoices as inv (inv.id)}
						<li
							class="row flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-sm tabular-nums"
						>
							<span>
								{dateOnly(inv.created_at)}
								{#if inv.number}· {inv.number}{/if}
								· <span class="badge">{inv.status}</span>
							</span>
							<span>
								{money(inv.amount_cents)}
								{#if inv.tax_cents > 0}
									<span class="text-ink-muted">(tax {money(inv.tax_cents)})</span>
								{/if}
								{#if inv.pdf_url}
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Paddle's invoice PDF, not an app route -->
									<a href={inv.pdf_url} class="link ml-2">PDF</a>
								{:else if inv.hosted_url}
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Paddle's hosted invoice, not an app route -->
									<a href={inv.hosted_url} class="link ml-2">View</a>
								{/if}
							</span>
						</li>
					{/each}
				</ul>
			{/if}
			<p class="mt-4 text-xs text-ink-muted">
				Paddle is the merchant of record: receipts and tax come from Paddle.
				<a href={resolve('/refunds')} class="link">Refunds</a>.
			</p>
		</div>
	{/if}
</PageShell>

<style>
	/* Three plan cards side by side from 900px (the page is list-wide while
	   they show), one under the other below. */
	.plans {
		display: grid;
		gap: 1rem;
		list-style: none;
		margin: 0;
		padding: 0;
	}
	@media (min-width: 900px) {
		.plans {
			grid-template-columns: repeat(3, minmax(0, 1fr));
		}
	}
	.change-row + .change-row {
		border-top: 1px solid var(--rule);
	}
</style>
