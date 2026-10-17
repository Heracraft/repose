<script lang="ts">
	import { onMount, tick } from 'svelte';
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
		billingInvoices,
		listProjects,
		getUsage
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { reachability } from '$lib/api/reachability.svelte';
	import {
		money,
		price,
		gb,
		gbs,
		sizeGB,
		dateOnly,
		dateTime,
		timeUntil,
		classMemoryGB
	} from '$lib/format';
	import { fromApi, holdsMemory, ordinal, type PlanFigures } from '$lib/plans';
	import PageShell from '$lib/components/PageShell.svelte';
	import PlanCells from '$lib/components/PlanCells.svelte';
	import MemoryBlocks from '$lib/components/MemoryBlocks.svelte';
	import UsageRing from '$lib/components/UsageRing.svelte';
	import { frozenSince } from '$lib/frozen';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { Billing, Invoice, Me, Plan, PlanId, Project, Subscription } from '$lib/api/types';

	let me = $state<Me | undefined>(undefined);
	let billing = $state<Billing | undefined>(undefined);
	let billingDisabled = $state(false);
	let loadError = $state<string | undefined>(undefined);
	let invoices = $state<Invoice[]>([]);
	/** The account's projects, for what runs and what is held under the meters. */
	let projects = $state<Project[]>([]);
	/** This period's egress by project, largest first; empty when the api has none. */
	let egressBy = $state<{ id: string; gb: number }[]>([]);
	/** The plan the visitor came for (the landing's "Start Plus" sends ?plan=plus). */
	let wantPlan = $state<PlanId | undefined>(undefined);

	// When Polar's checkout sends the browser back on ?checkout=done, the
	// subscription is still on its way by webhook: "Setting up your plan"
	// polls until it is there (I-604).
	let settingUp = $state(false);
	let setupTimedOut = $state(false);

	let busy = $state<string | undefined>(undefined);
	let changing = $state(false);
	let changeError = $state<string | undefined>(undefined);
	let confirmCancel = $state(false);

	/** Invoices six to a page, newest first, paged here: Polar's list is short. */
	const INVOICES_PER_PAGE = 6;
	let invoicePage = $state(0);
	let invoicePages = $derived(Math.max(1, Math.ceil(invoices.length / INVOICES_PER_PAGE)));
	let shownInvoices = $derived(
		invoices.slice(invoicePage * INVOICES_PER_PAGE, (invoicePage + 1) * INVOICES_PER_PAGE)
	);

	let sub = $derived(billing?.subscription ?? null);
	let plan = $derived<Plan | undefined>(
		billing && sub ? billing.plans.find((p) => p.id === sub.plan) : undefined
	);
	let figures = $derived<PlanFigures[]>(billing ? billing.plans.map(fromApi) : []);
	let scheduledPlan = $derived<Plan | undefined>(
		billing && sub?.scheduled_plan
			? billing.plans.find((p) => p.id === sub.scheduled_plan)
			: undefined
	);
	let holdActive = $derived(
		!!billing?.waitlist?.hold_until && new Date(billing.waitlist.hold_until).getTime() > Date.now()
	);
	/** Free seats as this user can count them: the fleet's, plus the one
	 * their invitation holds, so the counts agree with the held-seat banner. */
	let seatsFree = $derived(billing ? billing.seats.free + (holdActive ? 1 : 0) : 0);
	let anyAvailable = $derived(!!billing?.plans.some((p) => p.available));
	let accountStatus = $derived(me?.billing.status);
	// What the projects hold, which the plan's disk counts (I-585); an api
	// older than that sends only disk_allocated_gb.
	let diskHeld = $derived(
		billing ? (billing.usage.disk_held_gb ?? billing.usage.disk_allocated_gb) : 0
	);
	let running = $derived(projects.filter(holdsMemory));
	let heaviest = $derived(
		projects
			.filter((p) => (p.root_used_bytes ?? 0) > 0)
			.sort((a, b) => (b.root_used_bytes ?? 0) - (a.root_used_bytes ?? 0))
			.slice(0, 3)
	);
	/** "kanali 30 GB, landing-site 12 GB": the largest few, for a note. */
	const most = (rows: string[][]) => rows.map(([n, v]) => `${n} ${v}`).join(', ');
	let projectName = $derived(new Map(projects.map((p) => [p.id, p.name])));
	let egOver = $derived(!!billing && billing.usage.egress_gb > billing.usage.egress_included_gb);
	let period = $derived(sub ? periodDays(sub) : undefined);
	/** The first charges are the introductory price (I-497): the big figure is what is charged next. */
	let introCharge = $derived(
		!!sub &&
			!!plan &&
			!!sub.intro_until &&
			sub.next_charge_cents != null &&
			sub.next_charge_cents < plan.price_cents
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
			void loadUsage(billing.subscription);
			try {
				invoices = await billingInvoices();
				invoicePage = Math.min(invoicePage, Math.max(0, invoicePages - 1));
			} catch (err) {
				toastApiError(err, 'Could not load invoices.');
			}
		}
	}

	/** The projects under the meters and this period's egress by project.
	 * Both are extras on a page that stands without them, so a failure
	 * leaves them out and says nothing. */
	async function loadUsage(s: Subscription) {
		try {
			projects = await listProjects();
		} catch {
			projects = [];
		}
		try {
			const rows = await getUsage(dateOnly(s.period_start), dateOnly(new Date().toISOString()));
			const sums: Record<string, number> = {};
			for (const r of rows) sums[r.project_id] = (sums[r.project_id] ?? 0) + r.egress_gb;
			egressBy = Object.entries(sums)
				.filter(([, v]) => v > 0)
				.sort((a, b) => b[1] - a[1])
				.slice(0, 3)
				.map(([id, v]) => ({ id, gb: v }));
		} catch {
			egressBy = [];
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
		const asked = new URL(location.href).searchParams.get('plan');
		if (asked === 'solo' || asked === 'plus' || asked === 'pro') wantPlan = asked;
		await load();
		returnedFromCheckout();
		// The plan the visitor came for takes the focus, so Enter buys it.
		if (wantPlan && billing && !billing.subscription && !settingUp) {
			void focusAfterRender(`choose-${wantPlan}`);
		}
	});

	async function choose(planId: PlanId) {
		if (!billing) return;
		busy = `choose-${planId}`;
		try {
			// Polar's hosted checkout (I-604); it sends the browser back to
			// /billing?checkout=done. Busy stays set while the page leaves.
			const { url } = await billingCheckout(planId);
			window.location.assign(url);
		} catch (err) {
			busy = undefined;
			if (err instanceof ApiError && err.code === 'waitlisted') {
				// The seat went while the page was open: the api put the
				// user on the list, and the page shows that place.
				await load();
			} else if (err instanceof ApiError) {
				toastApiError(err);
			} else {
				toast.error(err instanceof Error ? err.message : 'Could not open the checkout.');
			}
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

	/** Opens or closes Change plan. The section sits under the plan card,
	 * often below the fold, so opening it scrolls it into view; the focus
	 * stays on the button. */
	async function toggleChange() {
		changing = !changing;
		changeError = undefined;
		confirmCancel = false;
		if (!changing) return;
		await tick();
		const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
		document
			.getElementById('change-plan')
			?.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'nearest' });
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
	// Egress reads as the pricing table writes it: 1000 GB is 1 TB.
	const egressFigure = (n: number) => (n >= 1000 && n % 1000 === 0 ? sizeGB(n) : gbs(n));

	/** "one large, or two small" for a plan's memory (docs/PRICING.md):
	 * xl 16 GB, large 8, small 4. */
	function runsAtOnce(p: Plan): string {
		const xl = Math.floor(p.memory_gb / 16);
		const large = Math.floor(p.memory_gb / 8);
		if (xl > 0) return `${count(xl)} xl, ${count(large)} large, or any mix`;
		return `${count(large)} large, or ${count(Math.floor(p.memory_gb / 4))} small`;
	}

	/** Which day of the period today is, of how many. */
	function periodDays(s: Subscription): { day: number; of: number; elapsed: number } {
		const start = new Date(s.period_start).getTime();
		const end = new Date(s.period_end).getTime();
		const of = Math.max(1, Math.round((end - start) / 86_400_000));
		const elapsed = Math.min(1, Math.max(0, (Date.now() - start) / Math.max(1, end - start)));
		const day = Math.min(of, Math.floor((Date.now() - start) / 86_400_000) + 1);
		return { day: Math.max(1, day), of, elapsed };
	}

	/** What is charged next and when; undefined once nothing more is. */
	function nextCharge(s: Subscription, p: Plan): { cents: number; on: string } | undefined {
		if (s.cancel_at) return undefined;
		const on = s.status === 'trialing' && s.trial_end ? s.trial_end : s.next_billed_at;
		if (!on) return undefined;
		return { cents: s.next_charge_cents ?? p.price_cents, on };
	}

	const apiPlan = (id: PlanId) => billing?.plans.find((p) => p.id === id);
	const newest = (list: Invoice[]) =>
		list.reduce<Invoice | undefined>(
			(a, b) => (!a || new Date(b.created_at) > new Date(a.created_at) ? b : a),
			undefined
		);
</script>

<svelte:head>
	<title>Billing — repose</title>
</svelte:head>

<!-- A plan's button in the picker. Only the plan the visitor came for is
     the solid button; a plan without the seats says why where its button
     would be, so the three buttons still end on one line. -->
{#snippet chooseAction(fp: PlanFigures)}
	{@const p = apiPlan(fp.id)}
	{#if p && !p.available && billing}
		<p class="needs">Needs {p.seats} seats; {seatsFree} free.</p>
	{:else}
		<button
			type="button"
			id="choose-{fp.id}"
			class={wantPlan === fp.id ? 'btn' : 'btn-quiet'}
			disabled={!!busy}
			onclick={() => choose(fp.id)}
		>
			{busy === `choose-${fp.id}` ? 'Opening checkout…' : `Choose ${fp.name}`}
		</button>
	{/if}
{/snippet}

<!-- While every seat is held, the plans are shown and none can be bought. -->
{#snippet waitingAction(fp: PlanFigures)}
	{#if billing?.waitlist}
		<button type="button" class="btn-quiet" aria-disabled="true">Waiting for a seat</button>
	{:else}
		<p class="needs">Needs {fp.seats} {fp.seats === 1 ? 'seat' : 'seats'}; none free.</p>
	{/if}
{/snippet}

<!-- Change plan: the other plans upgrade or downgrade; the current one is
     marked, and offers to stay on it while a downgrade is scheduled. -->
{#snippet changeAction(fp: PlanFigures)}
	{#if sub && plan}
		{#if fp.id === sub.plan}
			{#if scheduledPlan}
				<button type="button" class="btn" disabled={!!busy} onclick={() => changePlan(sub.plan)}>
					{busy === `plan-${sub.plan}` ? 'Saving…' : `Keep ${plan.name}`}
				</button>
			{:else}
				<p class="needs">Your plan</p>
			{/if}
		{:else if fp.id === scheduledPlan?.id}
			<p class="needs">From {@render day(sub.period_end)}</p>
		{:else}
			{@const up = fp.seats > plan.seats}
			<button
				type="button"
				class={up ? 'btn' : 'btn-quiet'}
				disabled={!!busy}
				data-testid="change-to-{fp.id}"
				onclick={() => changePlan(fp.id)}
			>
				{#if busy === `plan-${fp.id}`}
					{up ? 'Upgrading…' : 'Scheduling…'}
				{:else}
					{up ? 'Upgrade' : 'Downgrade'} to {fp.name}
				{/if}
			</button>
		{/if}
	{/if}
{/snippet}

<!-- A date never splits at its hyphens. -->
{#snippet day(iso: string)}<span class="date">{dateOnly(iso)}</span>{/snippet}

{#snippet pricesNote()}
	<p class="mt-6 text-xs text-ink-muted">
		Prices in USD before tax; Polar adds the tax for your country at checkout.
		<a href={resolve('/refunds')} class="link">Refunds</a>.
	</p>
{/snippet}

<!-- Back from Polar's checkout, the browser may restore this page as it
     left it, still opening the checkout. -->
<svelte:window
	onpageshow={(e) => {
		if (e.persisted) busy = undefined;
	}}
/>

<PageShell title="Billing">
	{#if billingDisabled}
		<section class="card" data-testid="billing-disabled">
			<h2 class="text-xl font-semibold">Billing is not switched on yet</h2>
		</section>
	{:else if !billing}
		<LoadState status={loadError ? 'failed' : 'loading'} error={loadError} onretry={load} />
	{:else if settingUp}
		<div class="card" data-testid="setting-up" aria-live="polite">
			<h2 class="text-xl font-semibold">Setting up your plan</h2>
			<p class="mt-2 text-sm text-ink-muted">
				Your payment went through. The plan appears here in a few seconds.
			</p>
		</div>
	{:else if !sub}
		{#if setupTimedOut}
			<div class="banner banner--warn">
				The plan is taking longer than usual to arrive. Refresh in a minute; if it is still not
				here, the payment went through and support will sort it out.
			</div>
		{/if}
		{#if accountStatus === 'exempt'}
			<div class="banner banner--ok">This account is billing-exempt. No plan is needed.</div>
		{/if}
		{#if holdActive && billing.waitlist?.hold_until}
			<div class="banner banner--ok" data-testid="seat-held">
				Your seat is held until <span class="date">{dateTime(billing.waitlist.hold_until)}</span>
				({timeUntil(billing.waitlist.hold_until)} left). Choose a plan before then.
			</div>
		{/if}
		{#if setupTimedOut || holdActive || anyAvailable}
			<div class="mb-4 flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
				<p class="text-sm">
					{billing.plans[0]?.trial_days ?? 7} days free, card at checkout, cancel any time.
				</p>
				<p class="font-mono text-compact text-ink-muted tabular-nums" data-testid="seats-line">
					{#if holdActive}
						Held for you: 1 seat
					{:else}
						{billing.seats.free}
						{billing.seats.free === 1 ? 'seat' : 'seats'} left
					{/if}
				</p>
			</div>
			<PlanCells plans={figures} introEligible={!!billing.intro_eligible} action={chooseAction} />
		{:else}
			<div class="waiting" data-testid="full">
				{#if billing.waitlist}
					<!-- The place reads as a stat strip, as the plan's figures do on
					     /projects: a serif figure, the queue's length beside it. -->
					<div class="stat-row wait-strip" data-testid="waitlist-place">
						<div class="stat">
							<span class="stat-label">Place on the waitlist</span>
							<span class="stat-value"
								>{ordinal(billing.waitlist.position)}
								<span class="wait-of"
									>of {Math.max(billing.seats.waiting, billing.waitlist.position)} waiting</span
								></span
							>
							<span class="stat-sub"
								>An email goes to <span class="email">{me?.email ?? 'you'}</span> when a seat frees.</span
							>
						</div>
						<div class="stat">
							<span class="stat-label">Seats free</span>
							<span class="stat-value">{billing.seats.free}</span>
						</div>
						<div class="stat">
							<span class="stat-label">Hold after invite</span>
							<span class="stat-value">72 h</span>
							<span class="stat-sub">to choose a plan</span>
						</div>
					</div>
				{:else}
					<h2 class="text-xl font-semibold">repose is full</h2>
					<p class="mt-2 text-sm text-ink-muted">
						Every seat is taken and {billing.seats.waiting}
						{billing.seats.waiting === 1 ? 'person is' : 'people are'} waiting. A seat frees when a plan
						ends.
					</p>
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
			<PlanCells plans={figures} introEligible={!!billing.intro_eligible} action={waitingAction} />
		{/if}
		{@render pricesNote()}
	{:else if plan}
		{#if accountStatus === 'suspended'}
			<div class="banner banner--error alert-row" data-testid="status-suspended">
				<p>
					Your account is suspended: the payment failed three days ago and your machines were
					stopped. Once the invoice is paid you can start them again; snapshots are kept 30 days.
				</p>
				<button
					type="button"
					class="btn btn--sm shrink-0"
					disabled={!!busy}
					onclick={() => openPortal('payment_method')}>Update card and pay</button
				>
			</div>
		{:else if sub.status === 'past_due'}
			<!-- docs/PRICING.md "Failed payments": starts are refused from day
			     0 and every machine stops on day 3. The api gives no date of
			     the failure, so the line counts from it. -->
			<div class="banner banner--error alert-row" data-testid="status-past-due">
				<p>
					<span class="font-medium">Your last payment failed.</span>
					New starts are refused and running machines keep running. Three days after the failure every
					machine stops, unless the card is updated.
				</p>
				<button
					type="button"
					class="btn btn--sm shrink-0"
					disabled={!!busy}
					onclick={() => openPortal('payment_method')}>Update card</button
				>
			</div>
		{/if}

		<section class="card plan-card" data-testid="plan">
			<!-- The plan, its price and its state, with the two things a
			     subscriber comes here to do. -->
			<div class="plan-head">
				<div class="min-w-0">
					<h2 class="plan-name">{plan.name}</h2>
					<p class="plan-price">
						<span class="n">{price(introCharge ? sub.next_charge_cents! : plan.price_cents)}</span>
						<span class="per">/ month</span>
					</p>
					{#if introCharge && sub.intro_until}
						<p class="mt-1.5 font-mono text-compact text-ink-muted" data-testid="intro-until">
							then {price(plan.price_cents)} a month from {@render day(sub.intro_until)}
						</p>
					{/if}
					<p class="mt-3 text-sm text-ink-muted" data-testid="plan-status">
						{#if sub.cancel_at}
							Cancelled. Ends {@render day(sub.cancel_at)}; machines stop then and snapshots stay 30
							days.
						{:else if sub.status === 'trialing' && sub.trial_end}
							<!-- The price it becomes stands under the big figure (I-497). -->
							Trial. First charge of {price(sub.next_charge_cents ?? plan.price_cents)} on {@render day(
								sub.trial_end
							)}.
						{:else if sub.status === 'past_due'}
							Payment past due.
						{:else if sub.next_billed_at}
							Active. Renews {@render day(sub.next_billed_at)}.
						{:else}
							Active.
						{/if}
						{#if scheduledPlan}
							Changes to {scheduledPlan.name} on {@render day(sub.period_end)}.
						{/if}
					</p>
				</div>
				<div class="plan-actions">
					{#if sub.cancel_at}
						<button type="button" id="resume-plan" class="btn" disabled={!!busy} onclick={resume}>
							{busy === 'resume' ? 'Resuming…' : 'Resume plan'}
						</button>
					{:else}
						<button
							type="button"
							class="btn-quiet"
							aria-expanded={changing}
							aria-controls="change-plan"
							disabled={!!busy}
							onclick={toggleChange}>Change plan</button
						>
					{/if}
					<button
						type="button"
						class="btn-quiet"
						disabled={!!busy}
						onclick={() => openPortal()}
						aria-label={busy === 'portal' ? undefined : 'Manage card and receipts'}
						>{#if busy === 'portal'}Opening…{:else}<span class="max-sm:hidden"
								>Manage card and receipts</span
							><span class="sm:hidden">Card and receipts</span>{/if}</button
					>
				</div>
			</div>

			<!-- What the plan is holding now (I-674): memory as the plan's own
			     squares, filled by each running machine; disk and egress as a
			     figure with a small ring. Then the period it is billed for. -->
			<div class="usage">
				<div class="mem" data-testid="meter-running-now">
					<div class="u-head">
						<span class="u-label">Running now</span>
						<span class="u-fig" class:u-over={billing.usage.running_gb > billing.usage.memory_gb}
							><span class="u-n">{gbs(billing.usage.running_gb)}</span>
							<span class="u-of">
								of {gbs(
									billing.usage.memory_gb
								)}{#if billing.usage.running_gb > billing.usage.memory_gb}&nbsp;· over{/if}</span
							></span
						>
					</div>
					<div class="mem-pic">
						<MemoryBlocks
							label="Running now"
							segments={running.map((p) => ({
								name: p.name,
								gb: classMemoryGB(p.class),
								frozen: !!frozenSince(p)
							}))}
							limit={billing.usage.memory_gb}
						/>
					</div>
					{#if running.length}
						<ul class="legend" aria-label="Running now">
							{#each running as p, i (p.id)}
								<li>
									<i
										class="sw"
										class:sw--alt={i % 2 === 1}
										class:sw--ice={!!frozenSince(p)}
										aria-hidden="true"
									></i>
									<a href={resolve('/projects/[id]', { id: p.id })} class="hover:underline"
										>{p.name}</a
									>
									<span class="u-num">{classMemoryGB(p.class)} GB</span>
									{#if frozenSince(p)}<span class="frost-word">frozen</span>{/if}
								</li>
							{/each}
						</ul>
					{/if}
					<p class="u-note">{runsAtOnce(plan)}</p>
				</div>

				<div class="u-rows">
					<div class="u-row" data-testid="meter-disk-held">
						<UsageRing
							label="Disk held"
							used={diskHeld}
							limit={billing.usage.disk_gb}
							format={gbs}
						/>
						<span class="u-label">Disk held</span>
						<span class="u-fig" class:u-over={diskHeld > billing.usage.disk_gb}
							><span class="u-n">{gbs(diskHeld)}</span>
							<span class="u-of">
								of {gbs(billing.usage.disk_gb)}{#if diskHeld > billing.usage.disk_gb}&nbsp;· over{/if}</span
							></span
						>
						{#if diskHeld > billing.usage.disk_gb}
							<p class="u-note u-note--warn">
								Creating, restoring and forking projects, and growing a disk, are refused until your
								projects hold less. A deleted file stops counting within a day, or when its machine
								stops.
							</p>
						{:else if heaviest.length && diskHeld >= billing.usage.disk_gb * 0.8}
							<!-- Who holds it, only once it nears the limit. -->
							<p class="u-note">
								Most: {most(heaviest.map((p) => [p.name, gb(p.root_used_bytes ?? 0)]))}
							</p>
						{/if}
					</div>
					<div class="u-row" data-testid="meter-egress-this-period">
						<UsageRing
							label="Egress this period"
							used={billing.usage.egress_gb}
							limit={billing.usage.egress_included_gb}
							format={egressFigure}
						/>
						<span class="u-label">Egress this period</span>
						<span class="u-fig" class:u-over={egOver}
							><span class="u-n">{egressFigure(billing.usage.egress_gb)}</span>
							<span class="u-of">
								of {egressFigure(billing.usage.egress_included_gb)}{#if egOver}&nbsp;· over{/if}</span
							></span
						>
						{#if billing.usage.overage_cents > 0}
							<p class="u-note u-note--warn">
								Over by {gbs(billing.usage.egress_gb - billing.usage.egress_included_gb)}: {money(
									billing.usage.overage_cents
								)} on the next invoice at $0.05 a GB.
							</p>
						{:else if egressBy.length && billing.usage.egress_gb >= billing.usage.egress_included_gb * 0.5}
							<p class="u-note">
								Most: {most(
									egressBy.map((e) => [
										projectName.get(e.id) ?? 'a destroyed project',
										gbs(Math.round(e.gb * 10) / 10)
									])
								)}
							</p>
						{/if}
					</div>
				</div>
			</div>

			<dl class="facts">
				{#if period}
					<div>
						<dt>Period</dt>
						<dd>{@render day(sub.period_start)} to {@render day(sub.period_end)}</dd>
						<dd class="fact-sub">Day {period.day} of {period.of}</dd>
					</div>
				{/if}
				<div>
					<dt>Next charge</dt>
					{#if nextCharge(sub, plan)}
						{@const next = nextCharge(sub, plan)!}
						<dd>
							<span data-testid="next-charge">{money(next.cents)}</span> on {@render day(next.on)}
						</dd>
						{#if billing.usage.overage_cents > 0}
							<dd class="fact-sub">plus {money(billing.usage.overage_cents)} egress so far</dd>
						{/if}
					{:else}
						<dd>None</dd>
						{#if sub.cancel_at}<dd class="fact-sub">Plan ends {@render day(sub.cancel_at)}</dd>{/if}
					{/if}
				</div>
				<div>
					<dt>Latest invoice</dt>
					{#if newest(invoices)}
						{@const inv = newest(invoices)!}
						<dd>{money(inv.amount_cents)}, {inv.status.replaceAll('_', ' ')}</dd>
						<dd class="fact-sub">{@render day(inv.created_at)}</dd>
					{:else}
						<dd class="text-ink-muted">None yet</dd>
					{/if}
				</div>
			</dl>

			{#if !sub.cancel_at}
				<div class="plan-foot">
					<!-- The documented two-step (DESIGN-LANGUAGE.md, "Confirmation"):
					     the button turns into the question, and Keep it brings it
					     back with the focus on it (I-393). One panel at a time:
					     opening the question closes Change plan and the reverse. -->
					{#if confirmCancel}
						<div class="text-sm" data-testid="confirm-cancel">
							<p class="text-ink-muted">
								Cancel the plan? It ends on {@render day(
									sub.status === 'trialing' && sub.trial_end ? sub.trial_end : sub.period_end
								)}. Machines run until then and stop at it; snapshots are kept 30 days after.
								Nothing is charged after that.
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
					{:else}
						<button
							type="button"
							id="cancel-plan"
							class="btn-ghost-danger -ml-2"
							disabled={!!busy}
							onclick={() => {
								changing = false;
								confirmCancel = true;
							}}>Cancel plan</button
						>
					{/if}
				</div>
			{/if}
		</section>

		<!-- The plans at page width under the card, so each cell has room
		     for its squares, its figures and its button. -->
		{#if changing}
			<section
				id="change-plan"
				class="mt-8"
				data-testid="change-plan"
				aria-labelledby="change-plan-h"
			>
				<div class="mb-4 flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
					<h2 id="change-plan-h" class="text-xl font-semibold">Change plan</h2>
					<p class="text-sm text-ink-muted">
						Upgrades apply now, prorated. Downgrades apply at renewal on {@render day(
							sub.period_end
						)}.
					</p>
				</div>
				{#if scheduledPlan}
					<p class="mb-4 text-sm text-ink-muted" data-testid="change-keep">
						{scheduledPlan.name} is scheduled for {@render day(sub.period_end)}.
					</p>
				{/if}
				<PlanCells
					plans={figures}
					current={sub.plan}
					compare={{ runningGB: billing.usage.running_gb, diskHeldGB: diskHeld }}
					action={changeAction}
					level={3}
				/>
				{#if changeError}
					<p class="field-error" data-testid="change-error">{changeError}</p>
				{/if}
			</section>
		{/if}

		<section class="mt-12" aria-labelledby="invoices-h">
			<div class="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-1">
				<h2 id="invoices-h" class="text-xl font-semibold">Invoices</h2>
				<p class="text-xs text-ink-muted">
					Receipts and tax: Polar. <a href={resolve('/refunds')} class="link">Refunds</a>
				</p>
			</div>
			{#if invoices.length === 0}
				<p class="mt-3 text-sm text-ink-muted">No invoices yet.</p>
			{:else}
				<table class="table mt-3" aria-label="Invoices">
					<thead>
						<tr>
							<th scope="col">Date</th>
							<th scope="col" class="max-sm:hidden">Number</th>
							<th scope="col">Status</th>
							<th scope="col" class="text-right">Amount</th>
							<th scope="col" class="text-right"><span class="sr-only">Invoice</span></th>
						</tr>
					</thead>
					<tbody>
						{#each shownInvoices as inv (inv.id)}
							<tr>
								<td class="whitespace-nowrap tabular-nums">{dateOnly(inv.created_at)}</td>
								<td class="font-mono text-compact max-sm:hidden">{inv.number ?? ''}</td>
								<td>
									<span class="badge {inv.status === 'paid' ? '' : 'badge--warn'}"
										>{inv.status.replaceAll('_', ' ')}</span
									>
								</td>
								<td class="text-right tabular-nums">
									{money(inv.amount_cents)}
									{#if inv.tax_cents > 0}
										<span class="block text-xs text-ink-muted">tax {money(inv.tax_cents)}</span>
									{/if}
								</td>
								<td class="text-right">
									{#if inv.pdf_url}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Polar's invoice PDF, not an app route -->
										<a href={inv.pdf_url} class="link">PDF</a>
									{:else if inv.hosted_url}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Polar's hosted invoice, not an app route -->
										<a href={inv.hosted_url} class="link">View</a>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
				{#if invoicePages > 1}
					<nav class="mt-3 flex items-center justify-end gap-3" aria-label="Invoice pages">
						<span class="font-mono text-compact text-ink-muted tabular-nums"
							>{invoicePage * INVOICES_PER_PAGE + 1}–{Math.min(
								invoices.length,
								(invoicePage + 1) * INVOICES_PER_PAGE
							)} of {invoices.length}</span
						>
						<button
							type="button"
							class="btn-quiet btn--sm"
							disabled={invoicePage === 0}
							onclick={() => (invoicePage -= 1)}>Newer</button
						>
						<button
							type="button"
							class="btn-quiet btn--sm"
							disabled={invoicePage >= invoicePages - 1}
							onclick={() => (invoicePage += 1)}>Older</button
						>
					</nav>
				{/if}
			{/if}
		</section>
	{/if}
</PageShell>

<style>
	/* A plan that cannot be had here, where its button would be: the
	   button's box, quiet, with the reason in it. */
	.needs {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 2.375rem;
		padding: 0.25rem 0.75rem;
		border: 1px dashed var(--rule-strong);
		border-radius: var(--radius-sm);
		font-size: 0.8125rem;
		text-align: center;
		color: var(--ink-muted);
	}
	.waiting {
		margin-bottom: 1.5rem;
	}
	.email {
		overflow-wrap: anywhere;
	}
	/* The queue's length beside the place, in the strip's .plan-of
	   style (PlanStrip.svelte). */
	.wait-of {
		margin-left: 0.125rem;
		font-family: var(--font-sans);
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: 0;
		color: var(--ink-muted);
	}
	/* On a phone the cells stack: label and figure on one line, as
	   the plan strip's cells do. */
	@media (width < 40rem) {
		.wait-strip .stat {
			display: grid;
			grid-template-columns: 1fr auto;
			align-items: baseline;
			column-gap: 1rem;
			padding: 0.75rem 1rem;
		}
		.wait-strip .stat-value {
			margin-top: 0;
			font-size: var(--text-lg);
		}
		.wait-strip .stat-sub {
			grid-column: 1 / -1;
		}
	}
	.alert-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem 1.5rem;
	}
	.alert-row p {
		flex: 1 1 24rem;
	}

	/* The current plan: one card in bands split by hairlines. */
	.plan-card {
		padding: 0;
	}
	.plan-head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1.25rem 2rem;
		padding: 1.5rem;
	}
	.plan-name {
		font-family: var(--font-display);
		font-size: 1.75rem;
		font-weight: 700;
		line-height: 1;
		letter-spacing: -0.01em;
		text-transform: uppercase;
	}
	.plan-price {
		display: flex;
		align-items: baseline;
		gap: 0.375rem;
		margin-top: 0.625rem;
	}
	.plan-price .n {
		font-family: var(--font-display);
		font-size: 2rem;
		font-weight: 700;
		line-height: 1;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
	}
	.plan-price .per {
		font-size: 0.8125rem;
		color: var(--ink-muted);
	}
	.plan-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
	}
	/* On a phone the two actions share one row at equal width, so they
	   read as a pair, not a ragged stack. */
	@media (width < 40rem) {
		.plan-actions {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(8.5rem, 1fr));
			width: 100%;
		}
		.plan-actions > :global(button) {
			width: 100%;
			padding-inline: 0.5rem;
		}
	}
	.plan-foot {
		padding: 0.875rem 1.5rem;
		border-top: 1px solid var(--rule);
	}
	.date {
		white-space: nowrap;
	}

	/* Usage: memory with its squares on the left, disk and egress as two
	   quiet rows on the right. */
	.usage {
		display: grid;
		border-top: 1px solid var(--rule);
	}
	.mem {
		min-width: 0;
		padding: 1.5rem;
	}
	.u-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
	}
	.u-label {
		min-width: 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	/* The figure wraps only between the number and its "of LIMIT". */
	.u-fig {
		min-width: 0;
		font-size: 0.9375rem;
		font-variant-numeric: tabular-nums;
	}
	.u-n,
	.u-of {
		white-space: nowrap;
	}
	.mem .u-fig {
		font-family: var(--font-display);
		font-size: 1.5rem;
		font-weight: 600;
		letter-spacing: -0.01em;
	}
	.u-of {
		margin-left: 0;
		font-family: var(--font-sans);
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: 0;
		color: var(--ink-muted);
	}
	.u-over .u-of {
		color: var(--color-amber-800);
	}
	.mem-pic {
		margin-top: 1rem;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem 1.25rem;
		margin-top: 1rem;
		list-style: none;
		font-size: 0.8125rem;
	}
	.legend li {
		display: inline-flex;
		align-items: center;
		gap: 0.4375rem;
	}
	/* The swatch is a square of the picture, so the legend reads as part of it. */
	.sw {
		width: 10px;
		height: 10px;
		border-radius: var(--radius-xs);
		background: var(--accent);
	}
	.sw.sw--ice {
		background: color-mix(in srgb, var(--frost) 22%, var(--surface));
		box-shadow: inset 0 0 0 1px var(--frost);
	}
	.frost-word {
		font-size: var(--text-xs);
		color: var(--frost);
	}
	.sw--alt {
		background: color-mix(in srgb, var(--accent) 55%, var(--surface));
	}
	.u-num {
		font-family: var(--font-mono);
		font-size: var(--text-compact);
		font-variant-numeric: tabular-nums;
		color: var(--ink-muted);
	}
	.u-note {
		margin-top: 0.75rem;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}
	.u-note--warn {
		color: var(--color-amber-800);
	}
	.u-rows {
		display: grid;
		align-content: center;
		min-width: 0;
		border-top: 1px solid var(--rule);
	}
	.u-row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		align-items: center;
		column-gap: 0.75rem;
		padding: 1.125rem 1.5rem;
	}
	.u-row .u-fig {
		text-align: right;
	}
	.u-row + .u-row {
		border-top: 1px solid var(--rule);
	}
	.u-row .u-note {
		grid-column: 2 / -1;
		margin-top: 0.375rem;
		line-height: 1.5;
	}
	/* On a phone the figure takes its own line under the label, the ring
	   beside both, and the note under the figure. */
	@media (width < 40rem) {
		.u-row {
			align-items: start;
		}
		.u-row > :global(.uring) {
			grid-row: 1 / span 2;
			align-self: start;
			margin-top: 0.125rem;
		}
		.u-row .u-fig {
			grid-column: 2 / -1;
			margin-top: 0.125rem;
			text-align: left;
		}
	}
	@media (prefers-color-scheme: dark) {
		.u-over .u-of,
		.u-note--warn {
			color: var(--color-amber-300);
		}
	}

	/* The billing period's facts in one quiet band: small labels, plain
	   figures, no second big price. */
	.facts {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
		gap: 1rem 2rem;
		padding: 1.125rem 1.5rem;
		border-top: 1px solid var(--rule);
		background: var(--page);
	}
	.facts dt {
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}
	.facts dd {
		margin-top: 0.25rem;
		font-size: 0.875rem;
		font-variant-numeric: tabular-nums;
	}
	.facts .fact-sub {
		margin-top: 0.125rem;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}
	@media (min-width: 1024px) {
		.usage {
			grid-template-columns: minmax(0, 1fr) minmax(0, 26rem);
		}
		.u-rows {
			border-top: 0;
			border-left: 1px solid var(--rule);
		}
	}
</style>
