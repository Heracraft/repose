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
	import { HOLD_HOURS, fromApi, holdsMemory, ordinal, type PlanFigures } from '$lib/plans';
	import { SUPPORT_MAILTO } from '$lib/links';
	import PageShell from '$lib/components/PageShell.svelte';
	import PlanCells from '$lib/components/PlanCells.svelte';
	import Meter from '$lib/components/Meter.svelte';
	import MemoryBlocks from '$lib/components/MemoryBlocks.svelte';
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

	/** The action under way; every other button waits (aria-disabled, I-766). */
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
	// An exempt account has no plan, so /billing's usage carries no limits;
	// /me's limits are what it works within (the biggest plan's, I-16).
	let limits = $derived({
		memory: billing?.usage.memory_gb || me?.limits?.memory_gb || 0,
		disk: billing?.usage.disk_gb || me?.limits?.disk_gb || 0,
		egress: billing?.usage.egress_included_gb || me?.limits?.egress_gb || 0
	});
	let running = $derived(projects.filter(holdsMemory));
	let runningOver = $derived(!!billing && billing.usage.running_gb > limits.memory);
	let diskOver = $derived(!!billing && limits.disk > 0 && diskHeld > limits.disk);
	let heaviest = $derived(
		projects
			.filter((p) => (p.root_used_bytes ?? 0) > 0)
			.sort((a, b) => (b.root_used_bytes ?? 0) - (a.root_used_bytes ?? 0))
			.slice(0, 3)
	);
	/** "kanali 30 GB, landing-site 12 GB": the largest few, for a note. */
	const most = (rows: string[][]) => rows.map(([n, v]) => `${n} ${v}`).join(', ');
	let projectName = $derived(new Map(projects.map((p) => [p.id, p.name])));
	let period = $derived(sub ? periodDays(sub) : undefined);
	/** The first charges are the introductory price (I-497): the figure is what is charged next. */
	let introCharge = $derived(
		!!sub &&
			!!plan &&
			!!sub.intro_until &&
			!sub.scheduled_plan &&
			sub.next_charge_cents != null &&
			sub.next_charge_cents < plan.price_cents
	);
	/** The plan's price this month: the introductory one while it runs. */
	let priceNow = $derived(
		sub && plan ? (introCharge ? sub.next_charge_cents! : plan.price_cents) : 0
	);
	/** Change plan's cards, the current one at the price the account pays. */
	let changeFigures = $derived(
		figures.map((f) => (f.id === sub?.plan ? { ...f, priceCents: priceNow } : f))
	);

	async function load() {
		// One failure, one report (I-395): an account that did not load is
		// toasted only when the billing call answered, since an outage fails
		// both and the load banner or the outage bar already says so.
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
		if (billing.subscription || accountStatus === 'exempt') void loadUsage(billing.subscription);
		if (billing.subscription) {
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
	async function loadUsage(s: Subscription | null) {
		try {
			projects = await listProjects();
		} catch {
			projects = [];
		}
		if (!s) return;
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
		if (!billing || busy) return;
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
		if (busy) return;
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

	/** Opens or closes Change plan. It opens under the Current plan block,
	 * often below the fold: its heading takes the focus and scrolls into
	 * view. Closing gives the focus back to the button. */
	async function toggleChange() {
		if (busy) return;
		changing = !changing;
		changeError = undefined;
		confirmCancel = false;
		await tick();
		if (!changing) {
			document.getElementById('change-plan-toggle')?.focus();
			return;
		}
		const h = document.getElementById('change-plan-h');
		const still = matchMedia('(prefers-reduced-motion: reduce)').matches;
		h?.focus({ preventScroll: true });
		document
			.getElementById('change-plan')
			?.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' });
	}

	async function changePlan(to: PlanId) {
		if (busy) return;
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
		if (busy) return;
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
		if (busy) return;
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
		if (busy) return;
		busy = what ?? 'portal';
		try {
			const { url } = await billingPortal(what);
			location.href = url;
		} catch (err) {
			busy = undefined;
			toastApiError(err, 'Could not open the billing portal.');
		}
	}

	/** aria-disabled while another action runs (I-766): the button keeps
	 * its focus and its name, and a click does nothing. */
	const waiting = () => (busy ? 'true' : undefined);

	const counts = ['none', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight'];
	const count = (n: number) => counts[n] ?? String(n);
	// Egress reads as the pricing table writes it: 1000 GB is 1 TB.
	const egressFigure = (n: number) => (n >= 1000 && n % 1000 === 0 ? sizeGB(n) : gbs(n));

	/** "Fits one large, or two small." for a plan's memory (docs/PRICING.md):
	 * xl 16 GB, large 8, small 4. */
	function runsAtOnce(memoryGB: number): string {
		const xl = Math.floor(memoryGB / 16);
		const large = Math.floor(memoryGB / 8);
		if (xl > 0) return `Fits ${count(xl)} xl, ${count(large)} large, or any mix.`;
		return `Fits ${count(large)} large, or ${count(Math.floor(memoryGB / 4))} small.`;
	}

	/** Which day of the period today is, of how many. */
	function periodDays(s: Subscription): { day: number; of: number } {
		const start = new Date(s.period_start).getTime();
		const end = new Date(s.period_end).getTime();
		const of = Math.max(1, Math.round((end - start) / 86_400_000));
		const day = Math.min(of, Math.floor((Date.now() - start) / 86_400_000) + 1);
		return { day: Math.max(1, day), of };
	}

	/** The i-th machine's fill among n, as MemoryBlocks draws its run. */
	function runFill(i: number, n: number): string {
		if (n <= 1) return 'var(--hk-accent)';
		if (n === 2) return i === 0 ? 'var(--hk-accent)' : 'var(--hk-text-2)';
		return i < 3 ? `var(--hk-series-${i + 1})` : 'var(--hk-text-2)';
	}

	/** What is charged next and when; undefined once nothing more is. The
	 * api's next_charge_cents prices a scheduled plan from the renewal on. */
	function nextCharge(s: Subscription, p: Plan): { cents: number; on: string } | undefined {
		if (s.cancel_at) return undefined;
		const on = s.status === 'trialing' && s.trial_end ? s.trial_end : s.next_billed_at;
		if (!on) return undefined;
		return { cents: s.next_charge_cents ?? p.price_cents, on };
	}

	const apiPlan = (id: PlanId) => billing?.plans.find((p) => p.id === id);
	/** A downgrade the account does not fit: the api refuses it (over_plan). */
	const doesNotFit = (fp: PlanFigures) =>
		!!billing && (billing.usage.running_gb > fp.memoryGB || diskHeld > fp.diskGB);
</script>

<svelte:head>
	<title>Billing · repose</title>
</svelte:head>

<!-- A plan's button in the picker. Only the plan the visitor came for is
     the solid button; a plan without the seats says why where its button
     would be. -->
{#snippet chooseAction(fp: PlanFigures)}
	{@const p = apiPlan(fp.id)}
	{#if p && !p.available && billing}
		<p class="refused">
			<span class="hk-dot hk-dot--warn" aria-hidden="true"></span>Needs {p.seats} seats; {seatsFree} free.
		</p>
	{:else}
		<button
			type="button"
			id="choose-{fp.id}"
			class={wantPlan === fp.id ? 'hk-btn' : 'hk-btn-secondary'}
			aria-disabled={waiting()}
			onclick={() => choose(fp.id)}
		>
			{busy === `choose-${fp.id}` ? 'Opening checkout…' : `Choose ${fp.name}`}
		</button>
	{/if}
{/snippet}

<!-- While every seat is held, the plans are shown and none can be bought. -->
{#snippet waitingAction(fp: PlanFigures)}
	{#if billing?.waitlist}
		<button type="button" class="hk-btn-secondary" aria-disabled="true">Waiting for a seat</button>
	{:else}
		<p class="refused">
			<span class="hk-dot hk-dot--warn" aria-hidden="true"></span>Needs {fp.seats}
			{fp.seats === 1 ? 'seat' : 'seats'}; none free.
		</p>
	{/if}
{/snippet}

<!-- Change plan: the other plans upgrade or downgrade; the current one
     carries its badge, a scheduled one says from when. A downgrade the
     account does not fit says why on its card and waits. -->
{#snippet changeAction(fp: PlanFigures)}
	{#if sub && plan && fp.id !== sub.plan}
		{#if fp.id === scheduledPlan?.id}
			<p class="refused">From {@render day(sub.period_end)}</p>
		{:else}
			{@const up = fp.seats > plan.seats}
			{@const refused = !up && doesNotFit(fp)}
			<button
				type="button"
				class="hk-btn-secondary"
				aria-disabled={refused ? 'true' : waiting()}
				aria-describedby={refused ? `over-${fp.id}` : undefined}
				data-testid="change-to-{fp.id}"
				onclick={() => !refused && changePlan(fp.id)}
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
{#snippet day(iso: string)}<span class="nw">{dateOnly(iso)}</span>{/snippet}

<!-- The plans for sale: the trial and seats line, the cards, the terms. -->
{#snippet buyLine()}
	<div class="buy-line">
		<p>{billing?.plans[0]?.trial_days ?? 7} days free, card at checkout, cancel any time.</p>
		<p class="seats" data-testid="seats-line">
			{#if holdActive}
				Held for you: 1 seat
			{:else if billing}
				{billing.seats.free}
				{billing.seats.free === 1 ? 'seat' : 'seats'} left
			{/if}
		</p>
	</div>
{/snippet}

{#snippet pricesNote()}
	<p class="terms">
		Prices in USD, before tax. <a href={resolve('/refunds')} class="hk-link">Refunds</a>
	</p>
{/snippet}

<!-- This month: what the account holds against its plan (I-674), each
     cell the Meter's anatomy (label, reading, figure). -->
{#snippet thisMonth()}
	{#if billing}
		<section class="hk-section" aria-labelledby="month-h">
			<div class="hk-section-head">
				<h2 id="month-h" class="hk-h2-app">This month</h2>
				{#if sub && period}
					<p>
						Day {period.day} of {period.of}, {sub.cancel_at ? 'ends' : 'resets'}
						{@render day(sub.period_end)}
					</p>
				{/if}
			</div>
			<div class="month">
				<div
					class="hk-meter cell"
					class:hk-meter--err={runningOver}
					data-testid="meter-running-now"
				>
					<div class="hk-meter-top">
						<span class="hk-meter-label">Memory running</span>
						<span class="hk-meter-value"
							>{gbs(billing.usage.running_gb)} of {gbs(limits.memory)}{#if runningOver}&nbsp;· over{/if}</span
						>
					</div>
					<MemoryBlocks
						label="Memory running"
						segments={running.map((p) => ({
							name: p.name,
							gb: classMemoryGB(p.class),
							frozen: !!frozenSince(p)
						}))}
						limit={limits.memory}
					/>
					{#if running.length}
						<ul class="legend" aria-label="Memory running">
							{#each running as p, i (p.id)}
								<li>
									<!-- The swatch is that machine's run of squares, in MemoryBlocks' fills. -->
									{#if frozenSince(p)}
										<i class="sw sw--ice" aria-hidden="true"></i>
									{:else}
										<i class="sw" style="--fill: {runFill(i, running.length)}" aria-hidden="true"
										></i>
									{/if}
									<a href={resolve('/projects/[id]', { id: p.id })} class="legend-link">{p.name}</a>
									<span class="u-num"
										>{classMemoryGB(p.class)} GB{frozenSince(p) ? ', frozen' : ''}</span
									>
								</li>
							{/each}
							{#if runningOver}
								<li>
									<i class="sw sw--over" aria-hidden="true"></i>Over the plan
									<span class="u-num">{gbs(billing.usage.running_gb - limits.memory)}</span>
								</li>
							{/if}
						</ul>
					{/if}
					<p class="note">{runsAtOnce(limits.memory)}</p>
				</div>
				<div class="cell">
					<Meter label="Disk held" used={diskHeld} limit={limits.disk} />
					{#if !diskOver && heaviest.length && diskHeld >= limits.disk * 0.8}
						<!-- Who holds it, only once it nears the limit. -->
						<p class="note">
							Most: {most(heaviest.map((p) => [p.name, gb(p.root_used_bytes ?? 0)]))}
						</p>
					{/if}
				</div>
				<div class="cell">
					<Meter
						label="Egress this period"
						used={billing.usage.egress_gb}
						limit={limits.egress}
						valueText="{egressFigure(billing.usage.egress_gb)} of {egressFigure(
							limits.egress
						)}{billing.usage.egress_gb > limits.egress ? ' · over' : ''}"
					/>
					{#if billing.usage.overage_cents > 0}
						<p class="note note--over" data-testid="egress-over">
							<span class="hk-dot hk-dot--warn" aria-hidden="true"></span>Over by {gbs(
								Math.round((billing.usage.egress_gb - limits.egress) * 10) / 10
							)}: {money(billing.usage.overage_cents)} on the next invoice at $0.05 a GB.
						</p>
					{:else if egressBy.length && billing.usage.egress_gb >= limits.egress * 0.5}
						<p class="note">
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
			{#if diskOver}
				<div class="hk-banner month-banner" role="status" data-testid="disk-over">
					<p>
						Creating, restoring and forking projects, and growing a disk, are refused until your
						projects hold less.
					</p>
				</div>
			{/if}
		</section>
	{/if}
{/snippet}

<!-- Back from Polar's checkout, the browser may restore this page as it
     left it, still opening the checkout. -->
<svelte:window
	onpageshow={(e) => {
		if (e.persisted) busy = undefined;
	}}
	onkeydown={(e) => {
		// Escape inside Change plan closes it, as the toggle above it does.
		if (e.key !== 'Escape' || !changing) return;
		if (!document.getElementById('change-plan')?.contains(document.activeElement)) return;
		e.preventDefault();
		void toggleChange();
	}}
/>

<PageShell title="Billing">
	{#if billingDisabled}
		<div class="hk-empty-row" data-testid="billing-disabled">
			<h2 class="hk-empty-title">Billing is not switched on yet</h2>
		</div>
	{:else if !billing}
		<LoadState status={loadError ? 'failed' : 'loading'} error={loadError} onretry={load} />
	{:else if settingUp}
		<div class="hk-banner hk-banner--info" data-testid="setting-up" role="status">
			<p>Setting up your plan…</p>
		</div>
	{:else if !sub}
		{#if setupTimedOut}
			<!-- Alone: the payment went through, so no second checkout is offered. -->
			<div class="hk-banner" role="alert" data-testid="setup-timeout">
				<p>
					The plan has not arrived.
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- a mailto, not a route -->
					<a href={SUPPORT_MAILTO} data-testid="support-link">Email us</a> and we will finish it by hand.
				</p>
			</div>
		{:else if accountStatus === 'exempt'}
			<!-- Exempt: no plan to buy, so no cards and no buy buttons; what it
			     holds, against the biggest plan's figures. -->
			<div class="hk-banner hk-banner--ok" role="status">
				<p>This account is billing-exempt.</p>
			</div>
			{@render thisMonth()}
		{:else if holdActive || anyAvailable}
			{#if holdActive && billing.waitlist?.hold_until}
				<div class="hk-banner hk-banner--ok" role="status" data-testid="seat-held">
					<p>
						Your seat is held until <span class="nw">{dateTime(billing.waitlist.hold_until)}</span>
						({timeUntil(billing.waitlist.hold_until)} left). Choose a plan before then.
					</p>
				</div>
			{/if}
			{@render buyLine()}
			<PlanCells plans={figures} introEligible={!!billing.intro_eligible} action={chooseAction} />
			{@render pricesNote()}
		{:else}
			{#if billing.waitlist}
				<div class="place" data-testid="waitlist-place">
					<div class="hk-banner hk-banner--info" role="status">
						<p>
							{ordinal(billing.waitlist.position)}
							of {Math.max(billing.seats.waiting, billing.waitlist.position)} waiting. An email goes to
							<span class="email">{me?.email ?? 'you'}</span> when a seat frees.
						</p>
					</div>
					<dl class="hk-facts hk-facts--row">
						<div>
							<dt>Seats free</dt>
							<dd>{billing.seats.free}</dd>
						</div>
						<div>
							<dt>Hold after invite</dt>
							<dd>{HOLD_HOURS} h to choose a plan</dd>
						</div>
					</dl>
				</div>
			{:else}
				<section class="full" data-testid="full" aria-labelledby="full-h">
					<h2 id="full-h" class="hk-h2-app">repose is full</h2>
					<div class="full-row">
						<p>
							Every seat is taken and {billing.seats.waiting}
							{billing.seats.waiting === 1 ? 'person is' : 'people are'} waiting.
						</p>
						<button
							type="button"
							class="hk-btn"
							aria-disabled={waiting()}
							onclick={joinWaitlist}
							data-testid="join-waitlist"
						>
							{busy === 'waitlist' ? 'Joining…' : 'Join the waitlist'}
						</button>
					</div>
				</section>
			{/if}
			<PlanCells plans={figures} introEligible={!!billing.intro_eligible} action={waitingAction} />
			{@render pricesNote()}
		{/if}
	{:else if plan}
		{#if accountStatus === 'suspended'}
			<div class="hk-banner hk-banner--err" role="alert" data-testid="status-suspended">
				<p>
					Your account is suspended: the payment failed three days ago and your machines were
					stopped. Once the invoice is paid you can start them again; snapshots are kept 30 days.
				</p>
				<button
					type="button"
					class="hk-banner-action"
					aria-disabled={waiting()}
					onclick={() => openPortal('payment_method')}>Update card and pay</button
				>
			</div>
		{:else if sub.status === 'past_due'}
			<!-- docs/PRICING.md "Failed payments": starts are refused from day
			     0 and every machine stops on day 3. The api gives no date of
			     the failure, so the line counts from it. -->
			<!-- Warn, as on /projects and kit section 7: err is for suspended
			     (review r1 sys-4). -->
			<div class="hk-banner" role="alert" data-testid="status-past-due">
				<p>
					Your last payment failed. New starts are refused and running machines keep running. Three
					days after the failure every machine stops, unless the card is updated.
				</p>
				<button
					type="button"
					class="hk-banner-action"
					aria-disabled={waiting()}
					onclick={() => openPortal('payment_method')}>Update card</button
				>
			</div>
		{/if}

		<section class="current" data-testid="plan" aria-labelledby="plan-h">
			<p class="hk-label">Current plan</p>
			<h2 id="plan-h" class="hk-h2 plan-name">{plan.name}</h2>
			<dl class="hk-facts hk-facts--row facts">
				<div>
					<dt>Price</dt>
					<dd>
						<span class="plan-price"
							><span class="n">{price(priceNow)}</span>
							a month</span
						>
						{#if introCharge && sub.intro_until}
							<span class="sub" data-testid="intro-until"
								>then {price(plan.price_cents)} a month from {@render day(sub.intro_until)}</span
							>
						{/if}
					</dd>
				</div>
				{#if sub.cancel_at || sub.status !== 'past_due' || scheduledPlan}
					<div>
						<dt>Status</dt>
						<dd>
							<span data-testid="plan-status">
								{#if sub.cancel_at}
									Cancelled. Ends {@render day(sub.cancel_at)}; machines stop then and snapshots
									stay 30 days.
								{:else if sub.status === 'trialing' && sub.trial_end}
									Trial. First charge of {price(sub.next_charge_cents ?? plan.price_cents)} on {@render day(
										sub.trial_end
									)}.
								{:else if sub.status === 'past_due'}
									<!-- The banner says the payment failed. -->
								{:else}
									<!-- Next charge says when it renews. -->
									Active.
								{/if}
								{#if scheduledPlan}
									Changes to {scheduledPlan.name} on {@render day(sub.period_end)}.
								{/if}
							</span>
							{#if scheduledPlan}
								<button
									type="button"
									class="hk-btn-ghost hk-btn-sm keep"
									data-testid="change-keep"
									aria-disabled={waiting()}
									onclick={() => changePlan(sub.plan)}
								>
									{busy === `plan-${sub.plan}` ? 'Saving…' : `Keep ${plan.name}`}
								</button>
							{/if}
						</dd>
					</div>
				{/if}
				<!-- A trial's Status already names its first charge. A failed
				     payment's banner is the charge that matters, so no renewal
				     shows beside it (review r1 R1-A5). -->
				{#if nextCharge(sub, plan) && sub.status !== 'trialing' && sub.status !== 'past_due' && accountStatus !== 'suspended'}
					{@const next = nextCharge(sub, plan)!}
					<div>
						<dt>Next charge</dt>
						<dd>
							<span data-testid="next-charge">{money(next.cents)}</span> on {@render day(next.on)}
						</dd>
					</div>
				{/if}
			</dl>
			<div class="actions">
				{#if sub.cancel_at}
					<button
						type="button"
						id="resume-plan"
						class="hk-btn"
						aria-disabled={waiting()}
						onclick={resume}
					>
						{busy === 'resume' ? 'Resuming…' : 'Resume plan'}
					</button>
				{:else}
					<button
						type="button"
						id="change-plan-toggle"
						class="hk-btn-secondary"
						aria-expanded={changing}
						aria-controls="change-plan"
						aria-disabled={waiting()}
						onclick={toggleChange}>Change plan</button
					>
				{/if}
				<button
					type="button"
					class="hk-btn-secondary"
					aria-disabled={waiting()}
					onclick={() => openPortal()}
					>{busy === 'portal' ? 'Opening…' : 'Card and receipts'}</button
				>
			</div>
		</section>

		{#if changing}
			<section
				id="change-plan"
				class="hk-section"
				data-testid="change-plan"
				aria-labelledby="change-plan-h"
			>
				<div class="hk-section-head">
					<h2 id="change-plan-h" class="hk-h2-app" tabindex="-1">Change plan</h2>
					<p>
						Upgrades apply now, prorated. Downgrades apply at renewal on {@render day(
							sub.period_end
						)}.
					</p>
				</div>
				<PlanCells
					plans={changeFigures}
					current={sub.plan}
					compare={{ runningGB: billing.usage.running_gb, diskHeldGB: diskHeld }}
					action={changeAction}
					level={3}
				/>
				{#if changeError}
					<div
						class="hk-banner hk-banner--err change-error"
						role="alert"
						data-testid="change-error"
					>
						<p>{changeError}</p>
					</div>
				{/if}
			</section>
		{/if}

		{@render thisMonth()}

		<section class="hk-section" aria-labelledby="invoices-h">
			<div class="hk-section-head">
				<h2 id="invoices-h" class="hk-h2-app">Invoices</h2>
				{#if invoicePages > 1}
					<nav class="pager" aria-label="Invoice pages">
						<span class="pos"
							>{invoicePage * INVOICES_PER_PAGE + 1}–{Math.min(
								invoices.length,
								(invoicePage + 1) * INVOICES_PER_PAGE
							)} of {invoices.length}</span
						>
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm"
							disabled={invoicePage === 0}
							onclick={() => (invoicePage -= 1)}>Newer</button
						>
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm"
							disabled={invoicePage >= invoicePages - 1}
							onclick={() => (invoicePage += 1)}>Older</button
						>
					</nav>
				{/if}
			</div>
			{#if invoices.length === 0}
				<p class="hk-empty-row">No invoices yet.</p>
			{:else}
				<table class="hk-table invoices" aria-label="Invoices">
					<thead>
						<tr>
							<th scope="col" class="c-date">Date</th>
							<th scope="col" class="c-num">Number</th>
							<th scope="col" class="c-status">Status</th>
							<th scope="col" class="c-amount">Amount</th>
							<th scope="col" class="c-link"><span class="hk-sr">Invoice</span></th>
						</tr>
					</thead>
					<tbody>
						{#each shownInvoices as inv (inv.id)}
							<tr>
								<td class="c-date">{dateOnly(inv.created_at)}</td>
								<td class="c-num">{inv.number ?? ''}</td>
								<td class="c-status">
									{#if inv.status === 'paid'}
										paid
									{:else}
										<span class="hk-badge hk-badge--outline">{inv.status.replaceAll('_', ' ')}</span
										>
									{/if}
								</td>
								<td class="c-amount">
									{money(inv.amount_cents)}
									{#if inv.tax_cents > 0}
										<span class="tax">tax {money(inv.tax_cents)}</span>
									{/if}
								</td>
								<td class="c-link">
									{#if inv.pdf_url}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Polar's invoice PDF, not an app route -->
										<a href={inv.pdf_url} class="hk-link">PDF</a>
									{:else if inv.hosted_url}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- Polar's hosted invoice, not an app route -->
										<a href={inv.hosted_url} class="hk-link">View</a>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</section>

		{#if !sub.cancel_at}
			<!-- The page's one destructive action, last (Guide B 1.4). The
			     documented two-step (I-393): the button turns into the
			     question, and Keep it brings it back with the focus on it.
			     One panel at a time: opening the question closes Change plan
			     and the reverse. -->
			<section class="cancel" aria-label="Cancel plan">
				{#if confirmCancel}
					<div class="hk-confirm" data-testid="confirm-cancel">
						<p>
							Cancel the plan? It ends on {@render day(
								sub.status === 'trialing' && sub.trial_end ? sub.trial_end : sub.period_end
							)}. Machines stop then and snapshots are kept 30 days after. Nothing is charged after
							that.
						</p>
						<div class="hk-confirm-actions">
							<button
								type="button"
								class="hk-btn-danger hk-btn-sm"
								aria-disabled={waiting()}
								onclick={cancel}
							>
								{busy === 'cancel' ? 'Cancelling…' : 'Cancel plan'}
							</button>
							<!-- Focus lands on the safe choice when the question opens. -->
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
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
						class="hk-btn-text-danger"
						aria-disabled={waiting()}
						onclick={() => {
							if (busy) return;
							changing = false;
							confirmCancel = true;
						}}>Cancel plan</button
					>
				{/if}
			</section>
		{/if}
	{/if}
</PageShell>

<style>
	.nw {
		white-space: nowrap;
	}

	/* The plans for sale. */
	.buy-line {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.25rem 1.5rem;
		margin-bottom: 1rem;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
	}
	.buy-line p {
		margin: 0;
	}
	.buy-line .seats {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.terms {
		margin: 1.25rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	/* A plan that cannot be had here, where its button would be: one line,
	   the button's height, so the three feet stay level. */
	.refused {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.25rem;
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.refused {
			min-height: 2.75rem;
		}
	}

	/* Seats full, and the place on the list. */
	.full {
		margin-bottom: 2rem;
	}
	.full-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem 1.5rem;
		margin-top: 0.875rem;
		padding: 1rem 0;
		border-top: 1px solid var(--hk-hairline);
		border-bottom: 1px solid var(--hk-hairline);
	}
	.full-row p {
		flex: 1 1 24rem;
		margin: 0;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
	}
	.place {
		margin-bottom: 2rem;
	}
	.place .hk-banner {
		margin-bottom: 0.5rem;
	}
	.email {
		overflow-wrap: anywhere;
	}

	/* The current plan: a label, the name, its facts in one row, two actions. */
	.current {
		margin-bottom: 0;
	}
	.plan-name {
		margin: 0.25rem 0 0;
	}
	.facts {
		margin-top: 1.25rem;
		grid-template-columns: repeat(2, minmax(0, 1fr));
	}
	@media (min-width: 900px) {
		.facts {
			grid-template-columns: repeat(4, minmax(0, 1fr));
			gap: 1rem 2rem;
		}
	}
	@media (max-width: 479.98px) {
		.facts {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	.facts dd {
		text-wrap: pretty;
	}
	.facts .sub {
		display: block;
		margin-top: 0.125rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.keep {
		display: flex;
		margin: 0.375rem 0 0 -0.75rem;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
		margin-top: 1.5rem;
	}
	/* On a phone the two actions share one row at equal width. */
	@media (max-width: 639.98px) {
		.actions {
			display: grid;
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.actions > button {
			min-width: 0;
			padding-inline: 0.5rem;
		}
	}

	.change-error {
		margin: 1rem 0 0;
	}

	/* This month: three cells between two hairlines. */
	.month {
		display: grid;
		gap: 1.5rem;
		padding: 1.25rem 0;
		border-top: 1px solid var(--hk-hairline);
		border-bottom: 1px solid var(--hk-hairline);
	}
	@media (min-width: 900px) {
		.month {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: 2.5rem;
		}
	}
	.cell {
		min-width: 0;
	}
	.note {
		margin: 0.75rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	.note--over {
		color: var(--hk-text-2);
	}
	.note .hk-dot {
		margin-right: 0.5rem;
		vertical-align: 0.0625rem;
	}
	.month-banner {
		margin: 1rem 0 0;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		margin: 0.75rem 0 0;
		padding: 0;
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
		background: var(--fill);
	}
	.sw--ice {
		box-shadow: inset 0 0 0 1px var(--hk-text-3);
		background: repeating-linear-gradient(135deg, var(--hk-text-3) 0 1px, transparent 1px 3px);
	}
	.sw--over {
		box-shadow:
			0 0 0 1px var(--hk-warn-dot),
			inset 0 0 0 1px var(--hk-warn-dot);
		background: transparent;
	}
	.legend-link {
		color: var(--hk-text);
		text-decoration: underline;
		text-decoration-color: transparent;
		text-underline-offset: 3px;
		transition: text-decoration-color var(--hk-dur-fast) var(--hk-ease);
	}
	.legend-link:hover {
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
	}
	.u-num {
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	@media (forced-colors: active) {
		.sw {
			forced-color-adjust: none;
			background: CanvasText;
		}
		.sw--ice {
			box-shadow: none;
			background: GrayText;
		}
	}

	/* Invoices. */
	.pager {
		display: flex;
		align-items: center;
		gap: 0.25rem;
	}
	.pager .pos {
		margin-right: 0.5rem;
		font-variant-numeric: tabular-nums;
	}
	.invoices {
		table-layout: fixed;
	}
	.c-date {
		width: 16%;
		white-space: nowrap;
	}
	.c-num {
		width: 22%;
	}
	td.c-num {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	.c-status {
		width: 14%;
	}
	td.c-status {
		color: var(--hk-text-2);
	}
	.c-amount {
		width: 28%;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}
	.tax {
		display: block;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.c-link {
		width: 20%;
		text-align: right;
	}
	/* A row's lone link and a project's name under Memory running take a
	   thumb's height without growing their rows. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.c-link a,
		.legend-link {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.75rem;
		}
		.c-link a {
			padding-inline: 0.5rem;
			margin-inline: -0.5rem;
		}
	}
	/* Below 640px a row is two lines: date and amount, then number and
	   status; the link stays right. */
	@media (max-width: 639.98px) {
		.invoices thead {
			display: none;
		}
		.invoices tr {
			display: grid;
			grid-template-columns: auto auto minmax(0, 1fr) auto;
			grid-template-areas:
				'date date amount link'
				'num status amount link';
			column-gap: 0.5rem;
			padding: 0.75rem 0;
			border-top: 1px solid var(--hk-hairline);
		}
		.invoices tbody tr:last-child {
			border-bottom: 1px solid var(--hk-hairline);
		}
		.invoices td {
			display: block;
			width: auto;
			padding: 0;
			border: 0;
		}
		.invoices td.c-date {
			grid-area: date;
		}
		.invoices td.c-amount {
			grid-area: amount;
		}
		.invoices td.c-link {
			grid-area: link;
			align-self: center;
			padding-left: 0.75rem;
		}
		.invoices td.c-num {
			grid-area: num;
			color: var(--hk-text-3);
		}
		.invoices td.c-status {
			grid-area: status;
			font-size: 0.8125rem;
			color: var(--hk-text-3);
		}
		.invoices td.c-status::before {
			content: '· ';
		}
	}

	.cancel {
		margin-top: 2.75rem;
	}
	.cancel .hk-btn-text-danger {
		margin-left: -0.625rem;
	}
</style>
