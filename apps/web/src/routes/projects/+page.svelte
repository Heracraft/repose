<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { getBilling, getMe, listDestroyed, listProjects, listQuestions } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { withoutBillingURL } from '$lib/refusal';
	import { PollFailure } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import {
		classMemoryGB,
		dateTime,
		gb,
		normalizeRemoteDisplay,
		relativeTime,
		tempLeft,
		uptime
	} from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { abuseStopReason } from '$lib/abuse';
	import { bootFallbackReason } from '$lib/bootfail';
	import { DISK_FULL_PERCENT, diskPercent } from '$lib/disk';
	import { DESTROYED_PAGE } from '$lib/destroyed';
	import RecentlyDestroyed from '$lib/components/RecentlyDestroyed.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import PlanStrip from '$lib/components/projects/PlanStrip.svelte';
	import WaitingCard from '$lib/components/questions/WaitingCard.svelte';
	import { pendingQuestions } from '$lib/questions.svelte';
	import AgentChips from '$lib/components/projects/AgentChips.svelte';
	import RecentActivity from '$lib/components/projects/RecentActivity.svelte';
	import { runRowAction, type RowAction } from '$lib/components/projects/actions';
	import {
		FILTERS,
		FILTER_LABELS,
		PROJECTS_SHOWN,
		TOOLBAR_FROM,
		atRest,
		byHeldOrder,
		holdsMemory,
		idSetKey,
		listSearch,
		matchesFilter,
		matchesQuery,
		orderProjects,
		readFilter,
		readQuery,
		type Filter
	} from '$lib/projectList';
	import type { Billing, DestroyedProject, Me, Project, Question } from '$lib/api/types';
	import { frozenSince } from '$lib/frozen';
	import { stopWarning } from '$lib/busyAgents';
	import { focusOnMount } from '$lib/focus';

	let projects = $state<Project[] | undefined>(undefined);
	/** The first load failed; the poll keeps trying, and Retry asks now. */
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	// The poll keeps the newest DESTROYED_PAGE; "Show more" past them
	// fetches the page before the oldest held (I-420).
	let destroyedNewest = $state<DestroyedProject[]>([]);
	let destroyedOlder = $state<DestroyedProject[]>([]);
	let destroyedDone = $state(false);
	/** When the destroyed list was last fetched; it is not on the 10s tick. */
	let lastDestroyed = 0;
	const destroyed = $derived.by(() => {
		const byId = new Map([...destroyedOlder, ...destroyedNewest].map((d) => [d.id, d]));
		return [...byId.values()].sort((a, b) =>
			a.destroyed_at < b.destroyed_at ? 1 : a.destroyed_at > b.destroyed_at ? -1 : 0
		);
	});
	const destroyedHasMore = $derived(
		!destroyedDone && (destroyedOlder.length > 0 || destroyedNewest.length >= DESTROYED_PAGE)
	);

	async function moreDestroyed() {
		const last = destroyed[destroyed.length - 1];
		if (!last) return;
		const page = await listDestroyed({ before: last.id, limit: DESTROYED_PAGE });
		destroyedOlder = [...destroyedOlder, ...page];
		if (page.length < DESTROYED_PAGE) destroyedDone = true;
	}
	/** The account, for the seats and waitlist state of a user with no project yet (I-290). */
	let me = $state<Me | undefined>(undefined);
	let holdActive = $derived(
		!!me?.waitlist?.hold_until && new Date(me.waitlist.hold_until).getTime() > Date.now()
	);

	/** Questions agents are waiting on, across every project (I-245). */
	let questions = $state<Question[]>([]);
	let questionsFor = $derived(
		questions.reduce(
			(m, q) => m.set(q.project_id, (m.get(q.project_id) ?? 0) + 1),
			new Map<string, number>()
		)
	);

	/** GET /billing for the plan strip and the lede; undefined while off or failing. */
	let billing = $state<Billing | undefined>(undefined);
	/** billing_disabled is an answer, not a failure: it is not asked again. */
	let billingOff = $state(false);
	let planName = $derived(
		billing?.subscription
			? billing.plans.find((p) => p.id === billing?.subscription?.plan)?.name
			: undefined
	);

	// A failing refresh toasts once, not on every poll (I-393).
	const listFailure = new PollFailure('Could not load projects.');

	async function refresh() {
		const first = projects === undefined;
		try {
			// The first load asks for the questions with the list, so the
			// card above the strip is in the first paint and nothing lands
			// above the table later (DESIGN-LANGUAGE "The pages"). It is a
			// side read, so passive: its answer must not clear the outage bar
			// a failing list raises at the same time (I-393).
			if (first) {
				// Billing comes with the list: without it the strip paints as
				// a row of stat cells and changes to squares and rings a few
				// hundred ms later, pushing the table down under the pointer
				// ("Nothing mounts above painted content", I-680).
				const [list, qs, bill] = await Promise.all([
					listProjects(),
					listQuestions('pending', { passive: true }).catch(() => undefined),
					getBilling({ passive: true }).catch((err: unknown) => {
						if (err instanceof ApiError && err.code === 'billing_disabled') billingOff = true;
						return undefined;
					})
				]);
				if (qs) {
					questions = qs;
					pendingQuestions.set(qs.length);
				}
				if (bill) billing = bill;
				projects = list;
			} else {
				projects = await listProjects();
			}
			loadFailed = false;
			listFailure.ok();
		} catch (err) {
			// Before the first load the banner says why, in the toast's words
			// (loadErrorText); a toast as well would say it twice. After it,
			// the list on screen stays and one toast reports the refresh.
			if (projects === undefined) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load projects.');
			}
			listFailure.fail(err, projects === undefined);
			// Nothing else is asked while the list fails: an answer would
			// reset the "cannot reach the api" bar the failed list just
			// raised (the strip's and the callout's calls included).
			return;
		}
		// The strip's and the callout's calls go out once the list is in;
		// each fails on its own and keeps what it showed last.
		const side = Promise.all([
			first ? undefined : refreshQuestions(),
			first ? undefined : refreshBilling()
		]);
		// Its own try: an api without the route (older than I-167) answers
		// 404, and that must not hide the live list or raise a toast.
		// Destroyed projects change a few times a week, and this is the
		// heaviest query on the page: the api answers the newest 100 with a
		// snapshot lookup each. It was on the 10s tick, 360 times an hour,
		// for ten rows thousands of pixels below the fold.
		if (first || Date.now() - lastDestroyed >= 60_000) {
			try {
				destroyedNewest = await listDestroyed();
				lastDestroyed = Date.now();
			} catch {
				destroyedNewest = [];
			}
		}
		if (projects.length === 0) {
			try {
				me = await getMe();
			} catch {
				me = undefined;
			}
		}
		await side;
	}

	async function refreshQuestions() {
		try {
			questions = await listQuestions('pending');
			pendingQuestions.set(questions.length);
		} catch {
			// The callout keeps the questions it showed; the list's own
			// failure is the one reported.
		}
	}

	async function refreshBilling() {
		if (billingOff) return;
		try {
			billing = await getBilling();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'billing_disabled') {
				billingOff = true;
				billing = undefined;
			}
			// Any other failure keeps the strip it drew last, or only Memory.
		}
	}

	/**
	 * The sentence after "code: " in last_error, for a project in error,
	 * one the platform stopped because a miner was running (I-239), or one
	 * running its previous system because its new one did not boot (I-590).
	 */
	function reason(p: Project): string {
		const abuse = abuseStopReason(p);
		if (abuse) return abuse;
		const boot = bootFallbackReason(p);
		if (boot) return boot;
		if (p.state !== 'error' || !p.last_error) return '';
		const i = p.last_error.indexOf(': ');
		return i > 0 ? p.last_error.slice(i + 2) : p.last_error;
	}

	onMount(() => pollWhileVisible(refresh));

	// One Start or Stop per row at a time; the label says it while it runs.
	let pending = $state<Record<string, RowAction>>({});

	function actionFor(p: Project): RowAction | undefined {
		if (pending[p.id]) return pending[p.id];
		if (p.state === 'running') return 'stop';
		if (p.state === 'stopped' || p.state === 'error') return 'start';
		return undefined;
	}

	/**
	 * The row whose Stop is waiting for an answer, because stopping it
	 * would end an agent's turn (I-614). Stop here is revealed on hover
	 * inside a row whose name link covers the whole row, so in a long
	 * list the pointer crosses many of them on the way to the one the
	 * user wants; a stray click had no undo.
	 */
	let askingStop = $state<string | undefined>(undefined);

	/** `act` is shadowed inside the phone row by its own `{@const act}`. */
	const stopNow = (p: Project) => void act(p, 'stop');

	function pressed(p: Project, action: RowAction) {
		if (action === 'stop' && stopWarning(p)) {
			askingStop = p.id;
			return;
		}
		void act(p, action);
	}

	/**
	 * A plan or capacity refusal from a row, drawn as the banner the
	 * project page draws: the api's sentence without its raw URL, a link
	 * to /billing, and a Stop for each machine holding the memory. It was
	 * a toast that took the sentence away before it could be acted on.
	 */
	let refusal = $state<
		{ message: string; reason?: string; projects: { slug: string; id: string }[] } | undefined
	>(undefined);

	function explainRefusal(err: ApiError) {
		const detail = err.detail ?? {};
		const reason = typeof detail.reason === 'string' ? detail.reason : undefined;
		const slugs = Array.isArray(detail.projects)
			? detail.projects.filter((x): x is string => typeof x === 'string')
			: [];
		const named = slugs.flatMap((slug) => {
			const q = (projects ?? []).find((x) => x.slug === slug);
			return q ? [{ slug, id: q.id }] : [];
		});
		refusal = { message: err.message, reason, projects: named };
	}

	async function act(p: Project, action: RowAction) {
		askingStop = undefined;
		if (pending[p.id]) return;
		pending = { ...pending, [p.id]: action };
		try {
			refusal = undefined;
			await runRowAction(action, p);
		} catch (err) {
			if (err instanceof ApiError && err.code === 'payment_required') explainRefusal(err);
			else if (err instanceof ApiError && err.code === 'capacity')
				refusal = { message: 'No capacity right now, try again in a few minutes.', projects: [] };
			else throw err;
		}
		await refresh();
		const rest = { ...pending };
		delete rest[p.id];
		pending = rest;
	}

	/**
	 * The guest's root filesystem used, with its figures for a title.
	 * The percentage is $lib/disk's, the one `repose status` and `repose
	 * ls` print: this had its own copy that fell back to the volume's
	 * allocated size when the guest sent no root figure, so the table
	 * could call a machine 95% full whose own page said nothing.
	 */
	function disk(p: Project): { pct: number; used: string; title: string } | undefined {
		const pct = diskPercent(p);
		if (pct === null || p.root_used_bytes === undefined || p.root_size_bytes === undefined) {
			return undefined;
		}
		return {
			pct,
			used: gb(p.root_used_bytes),
			title: `${gb(p.root_used_bytes)} of ${gb(p.root_size_bytes)}`
		};
	}

	let summary = $derived.by(() => {
		if (!projects || projects.length === 0) return undefined;
		const awake = projects.filter(holdsMemory).length;
		const n = projects.length;
		// A machine in error is the state the user has to act on, and at 40
		// projects it was visible only as a red line inside its own row.
		const broken = projects.filter((p) => p.state === 'error').length;
		return [
			awake > 0 ? `${awake} running` : 'No machines running',
			broken > 0 ? `${broken} in error` : '',
			`${n} ${n === 1 ? 'project' : 'projects'}`,
			planName
		]
			.filter(Boolean)
			.join(' · ');
	});

	/** Whether an agent is waiting on an answer about this project. */
	const waitingOn = (p: Project) => !!questionsFor.get(p.id);

	// The filter and the search live in the URL, so Back steps between
	// them and a reload keeps the list the user was reading.
	let filter = $derived(readFilter(page.url.searchParams));
	let query = $derived(readQuery(page.url.searchParams));
	/** What the search box holds while the URL catches up with it. */
	let typed = $state<string | undefined>(undefined);
	let searchBox = $derived(typed ?? query);
	let debounce: ReturnType<typeof setTimeout> | undefined;

	// The api answers in `order by created_at`, which is the order they
	// were made in; the list is read by what wants you, then what is up.
	// The order is taken on arrival and whenever a project appears or
	// disappears, then held: a machine the user stops keeps its place
	// instead of sliding away under the pointer that pressed Stop.
	let held = $state<string[]>([]);
	// The key is a $derived so the effect runs again only when the set of
	// projects actually changes; depending on `projects` itself would
	// re-run it on every 10s poll, which is the whole point of holding.
	let orderKey = $derived(idSetKey(projects ?? []));
	$effect(() => {
		void orderKey;
		untrack(() => {
			held = orderProjects(projects ?? [], waitingOn).map((p) => p.id);
		});
	});
	// Filtered first, so "Show all N" counts what is on screen.
	let matched = $derived(
		(projects ?? [])
			.filter((p) => matchesFilter(p, filter, waitingOn(p)) && matchesQuery(p, query))
			.sort(byHeldOrder(held))
	);
	/** Rows drawn: the first PROJECTS_SHOWN until "Show all" is pressed. */
	let showAll = $state(false);
	let rows = $derived(showAll ? matched : matched.slice(0, PROJECTS_SHOWN));
	let capped = $derived(matched.length - rows.length);
	/** The toolbar is drawn once the list is long enough to need it. */
	let toolbar = $derived((projects?.length ?? 0) > TOOLBAR_FROM);

	function filterHref(f: Filter): string {
		return `${resolve('/projects')}${listSearch(page.url.searchParams, f, searchBox)}`;
	}

	function onSearch(value: string) {
		typed = value;
		clearTimeout(debounce);
		// 200ms: a URL write per keystroke fills the history and re-runs
		// the page's load for a list that is already in memory.
		debounce = setTimeout(() => {
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- filterHref starts with resolve()
			void goto(`${resolve('/projects')}${listSearch(page.url.searchParams, filter, value)}`, {
				keepFocus: true,
				noScroll: true,
				replaceState: true
			});
			typed = undefined;
		}, 200);
	}

	$effect(() => () => clearTimeout(debounce));
</script>

{#snippet stateCell(p: Project)}
	{@const frozen = frozenSince(p)}
	<StateDot state={p.state} frozen={!!frozen} />
	{#if frozen}
		<!-- Frozen while idle (I-683, I-826): how long, in the frost
		     colour, where `up` would be. The word itself is StateDot's, so
		     the cell says it once; the stacked row below says the same
		     two things in the same order. -->
		<div class="frost-sub mt-0.5 text-xs tabular-nums">for {uptime(frozen)}</div>
	{:else if p.state === 'running' && !p.idle}
		<div class="mt-0.5 text-xs text-ink-muted tabular-nums">up {uptime(p.started_at)}</div>
	{:else if p.state === 'stopped'}
		<!-- A stopped row carried no time at all, so thirty of them read
		     the same and the order over them could not be seen. -->
		<div class="mt-0.5 text-xs text-ink-muted tabular-nums">
			{p.started_at
				? `last run ${relativeTime(p.started_at)}`
				: `made ${relativeTime(p.created_at)}`}
		</div>
	{/if}
	{#if p.state === 'running' && p.idle}
		<!-- Nobody on it for a day, holding its memory against the plan
		     (I-262, I-289, I-617). A frozen machine is normally unused as
		     well, and this line says the other thing: the plan's memory is
		     still held (I-826). The stacked row prints it too. -->
		<div class="mt-0.5 text-xs text-amber-700 tabular-nums dark:text-amber-400">
			unused {uptime(p.idle.since)}
		</div>
	{/if}
	{#if p.host_unreachable}
		<!-- A host going unreachable takes every machine on it, so this is
		     a several-rows-at-once state; the project page had the banner
		     and the list said nothing, showing a green dot and a climbing
		     uptime for a machine that cannot be started. -->
		<div class="mt-0.5 text-xs text-amber-700 dark:text-amber-400">server not answering</div>
	{/if}
	{#if p.expires_at}
		<div class="mt-0.5 text-xs text-ink-muted tabular-nums">{tempLeft(p.expires_at)}</div>
	{/if}
	{#if reason(p)}
		<div class="mt-0.5 max-w-xs text-xs text-red-700 dark:text-red-400">{reason(p)}</div>
	{/if}
{/snippet}

{#snippet diskCell(p: Project)}
	<!-- What the guest holds, as a figure: a bar per row was noise next to
	     the plan's own figure above (I-674). Amber only near full. -->
	{@const d = disk(p)}
	{#if d}
		{@const full = d.pct >= DISK_FULL_PERCENT}
		<span
			class="font-mono text-compact tabular-nums"
			class:disk--full={full}
			class:text-ink-muted={!full}
			title={d.title}>{full ? `${d.pct}% full` : d.used}</span
		>
	{/if}
{/snippet}

{#snippet actionButton(p: Project, always: boolean)}
	{@const a = actionFor(p)}
	{#if a}
		<!-- One word for the face and the accessible name: they drifted, so
		     mid-operation the button read "Stopping…" and was named "Stop",
		     which breaks voice control (WCAG 2.5.3). aria-disabled, not
		     disabled: a disabled element drops focus to <body> and a
		     keyboard user lost their place for the whole op (I-391). -->
		{@const word =
			pending[p.id] === 'stop'
				? 'Stopping…'
				: pending[p.id] === 'start'
					? 'Starting…'
					: a === 'stop'
						? 'Stop'
						: 'Start'}
		<button
			type="button"
			class="row-action btn-quiet btn--sm"
			class:row-action--shown={always || !!pending[p.id]}
			aria-disabled={pending[p.id] ? 'true' : undefined}
			aria-label="{word} {p.name}"
			onclick={() => pressed(p, a)}>{word}</button
		>
	{/if}
{/snippet}

<svelte:head>
	<title>Projects — repose</title>
</svelte:head>

<PageShell title="Projects" lede={summary}>
	<LoadState
		status={projects !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={refresh}
		error={loadError}
	>
		{#if projects && projects.length === 0}
			{@const banner =
				me?.waitlist && holdActive && me.waitlist.hold_until
					? 'held'
					: me?.waitlist
						? 'waitlist'
						: me?.billing.status === 'none'
							? 'none'
							: undefined}
			{@const planFigures = !banner && !billingOff && !!billing && billing.usage.memory_gb > 0}
			<!-- One card across the frame, as the plan strip is: what to run on
			     the left, the plan's figures or what stands before a first
			     machine beside it. -->
			<div class="empty" class:empty--side={!!banner || planFigures}>
				<div class="empty-main">
					<h2 class="text-xl font-semibold">No projects yet</h2>
					<p class="mt-2 text-ink-muted">
						A git checkout becomes a project, with a machine of its own, the first time you run this
						in it.
					</p>
					<div class="mt-4">
						<CodeCopy command="repose run" what="repose run" />
					</div>
				</div>
				<!-- The side column is the banner itself, its colours to the
				     card's edge, so no box sits inside the card's box. -->
				{#if banner === 'held' && me?.waitlist?.hold_until}
					<div class="empty-side banner banner--ok" data-testid="seat-held">
						Your seat is held until {dateTime(me.waitlist.hold_until)}.
						<a href={resolve('/billing')} class="link">Choose a plan</a> before then.
					</div>
				{:else if banner === 'waitlist' && me?.waitlist}
					<div class="empty-side banner banner--warn" data-testid="waitlist-place">
						repose is full. You’re number {me.waitlist.position} on the waitlist; we’ll email
						{me.email} when a seat frees, and you’ll have 72 hours to choose a plan.
					</div>
				{:else if banner === 'none'}
					<div class="empty-side empty-side--plain banner" data-testid="no-plan">
						<a href={resolve('/billing')} class="link">Choose a plan</a> before your first machine can
						start. Seven days free, card at checkout.
					</div>
				{:else if planFigures}
					<div class="empty-side empty-side--strip">
						<PlanStrip projects={[]} {billing} flat />
					</div>
				{/if}
			</div>
		{:else if projects}
			<div class="flex flex-col gap-6">
				<WaitingCard bind:questions cap={3} testid="pending-questions" />
				<PlanStrip {projects} billing={billingOff ? undefined : billing} {questions} />
			</div>

			<!-- A search and the state filters, once the list is long enough to
			     need them. The filters are links with aria-current, drawn like
			     the project's tabs (DESIGN-LANGUAGE "Tabs"), and both halves
			     live in the URL. -->
			{#if refusal}
				<div class="banner banner--warn mt-6" data-testid="row-refusal">
					<p>
						{withoutBillingURL(refusal.message)}
						{#if refusal.reason}
							<a href={resolve('/billing')} class="link">See your plan</a>
						{/if}
					</p>
					{#if refusal.projects.length > 0}
						<div class="mt-2 flex flex-wrap gap-2">
							{#each refusal.projects as named (named.id)}
								<button
									type="button"
									class="btn-quiet btn--sm"
									disabled={!!pending[named.id]}
									onclick={() =>
										stopNow((projects ?? []).find((q) => q.id === named.id) ?? ({} as Project))}
									>Stop {named.slug}</button
								>
							{/each}
						</div>
					{/if}
				</div>
			{/if}

			{#if toolbar}
				<div class="ptools mt-8">
					<input
						type="search"
						class="field pt-search"
						aria-label="Search projects"
						placeholder="Search"
						value={searchBox}
						oninput={(e) => onSearch(e.currentTarget.value)}
					/>
					<nav aria-label="Filter projects" class="flex gap-5 text-sm">
						{#each FILTERS as f (f)}
							{@const on = filter === f}
							<!-- eslint-disable svelte/no-navigation-without-resolve -- filterHref starts with resolve() -->
							<a
								href={filterHref(f)}
								aria-current={on ? 'page' : undefined}
								data-sveltekit-noscroll
								data-sveltekit-keepfocus
								data-sveltekit-replacestate
								class="-mb-px border-b py-2 transition-[color,border-color] duration-150 focus-visible:-outline-offset-2 {on
									? 'border-ink text-ink'
									: 'border-transparent text-ink-muted hover:text-ink'}">{FILTER_LABELS[f]}</a
							>
							<!-- eslint-enable svelte/no-navigation-without-resolve -->
						{/each}
					</nav>
				</div>
			{/if}

			{#if matched.length === 0}
				<p class="mt-8 text-ink-muted" data-testid="no-matches">
					No project matches. <a href={resolve('/projects')} class="link">Clear</a>
				</p>
			{/if}

			<!-- From sm up, a table. A region with a name and a tab stop: between
			     sm and lg the table can still run past the frame, and a keyboard
			     can only scroll what it can focus. -->
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<div
				class="mt-6 hidden overflow-x-auto sm:block"
				role="region"
				aria-label="Projects"
				tabindex="0"
			>
				<table class="table ptable">
					<thead>
						<tr>
							<th scope="col">Project</th>
							<th scope="col">State</th>
							<th scope="col" class="hidden lg:table-cell">Size</th>
							<th scope="col">Agents</th>
							<th scope="col" class="hidden md:table-cell">Disk</th>
							<th scope="col" class="hidden xl:table-cell">Last snapshot</th>
							<th scope="col"><span class="sr-only">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each rows as p (p.id)}
							{@const remote = p.remote_url ? normalizeRemoteDisplay(p.remote_url) : ''}
							<tr class="prow" class:prow--frozen={!!frozenSince(p)}>
								<td class="pcell-name">
									<div class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
										<!-- Underlined at rest, in a quiet rule colour: the name is
										     the row's link, and its ::after spreads over the whole
										     row, so a click anywhere in it opens the project. -->
										<a href={resolve('/projects/[id]', { id: p.id })} class="pname font-medium"
											>{p.name}</a
										>
										{#if p.expires_at}
											<!-- repose run --temp (I-347): destroyed with no snapshot. -->
											<span class="badge">temporary</span>
										{/if}
										{#if questionsFor.get(p.id)}
											{@const n = questionsFor.get(p.id)!}
											<span class="badge badge--warn">{n} question{n === 1 ? '' : 's'}</span>
										{/if}
									</div>
									{#if remote}
										<div
											class="premote mt-0.5 truncate font-mono text-compact text-ink-muted"
											title={remote}
										>
											{remote}
										</div>
									{/if}
								</td>
								<td class="whitespace-nowrap">{@render stateCell(p)}</td>
								<td class="hidden whitespace-nowrap lg:table-cell">
									<span class="font-mono text-sm">{p.class}</span>
									{#if classMemoryGB(p.class)}
										<span class="text-ink-muted"> · {classMemoryGB(p.class)} GB</span>
									{/if}
								</td>
								<td>
									<AgentChips
										agents={p.signals?.agents}
										ssh={p.signals?.ssh_sessions}
										live={!atRest(p)}
									/>
								</td>
								<td class="hidden whitespace-nowrap md:table-cell">{@render diskCell(p)}</td>
								<td class="hidden whitespace-nowrap text-ink-muted xl:table-cell">
									{#if p.last_snapshot_at}
										<time datetime={p.last_snapshot_at} title={dateTime(p.last_snapshot_at)}
											>{relativeTime(p.last_snapshot_at)}</time
										>
									{:else}
										<span class="text-ink-faint">none</span>
									{/if}
								</td>
								<td class="pcell-actions">
									<span class="inline-flex items-center gap-2">
										{@render actionButton(p, false)}
										<svg
											class="pchev"
											width="16"
											height="16"
											viewBox="0 0 16 16"
											fill="none"
											aria-hidden="true"
											><path
												d="M6 3.5 10.5 8 6 12.5"
												stroke="currentColor"
												stroke-width="1.5"
											/></svg
										>
									</span>
								</td>
							</tr>
							{#if askingStop === p.id}
								<tr data-testid="confirm-stop">
									<td colspan="7" class="pt-0">
										<div class="banner banner--warn">
											<p>{stopWarning(p)}</p>
											<div class="mt-2 flex items-center gap-3">
												<button type="button" class="btn-danger btn--sm" onclick={() => stopNow(p)}
													>Stop anyway</button
												>
												<button
													type="button"
													class="btn-ghost hit"
													use:focusOnMount
													onclick={() => (askingStop = undefined)}>Keep it running</button
												>
											</div>
										</div>
									</td>
								</tr>
							{/if}
						{/each}
					</tbody>
				</table>
			</div>

			<!-- Below sm, a stacked list: each project one link, its action
			     on its first line. -->
			<ul class="mt-6 border-t border-rule-strong sm:hidden" aria-label="Projects">
				{#each rows as p (p.id)}
					{@const remote = p.remote_url ? normalizeRemoteDisplay(p.remote_url) : ''}
					{@const agents = p.signals?.agents ?? []}
					{@const act = actionFor(p)}
					<!-- The row is one link; Start or Stop sits on its first line's
				     right edge, outside the link, as a tab stop of its own. -->
					<li class="relative border-b border-rule" class:mrow--frozen={!!frozenSince(p)}>
						<a href={resolve('/projects/[id]', { id: p.id })} class="mrow" class:mrow--act={!!act}>
							<span class="flex items-baseline justify-between gap-3">
								<span class="flex min-w-0 items-baseline gap-2">
									<span class="truncate font-medium">{p.name}</span>
									{#if p.expires_at}<span class="badge shrink-0">temporary</span>{/if}
								</span>
								<span class="shrink-0 text-sm"
									><StateDot state={p.state} frozen={!!frozenSince(p)} /></span
								>
							</span>
							<span
								class="mt-1 flex min-w-0 items-baseline gap-2 font-mono text-compact text-ink-muted"
							>
								<span class="shrink-0 whitespace-nowrap">
									{p.class}{#if frozenSince(p)}&nbsp;· for {uptime(
											frozenSince(p)!
										)}{:else if p.state === 'running'}&nbsp;· up {uptime(
											p.started_at
										)}{:else if p.state === 'stopped'}&nbsp;· {p.started_at
											? `last run ${relativeTime(p.started_at)}`
											: `made ${relativeTime(p.created_at)}`}{/if}
								</span>
								{#if remote}<span aria-hidden="true">·</span>
									<span class="min-w-0 truncate">{remote}</span>{/if}
							</span>
							<!-- A temporary machine's countdown and the unused warning are
							     losses the reader cannot undo, so the stacked row carries
							     them as the table's state cell does. -->
							{#if p.expires_at || (p.state === 'running' && p.idle)}
								<span class="mt-1 flex flex-wrap items-baseline gap-x-2 text-xs tabular-nums">
									{#if p.state === 'running' && p.idle}
										<span class="text-amber-700 dark:text-amber-400"
											>unused {uptime(p.idle.since)}</span
										>
									{/if}
									{#if p.expires_at}
										<span class="text-ink-muted">{tempLeft(p.expires_at)}</span>
									{/if}
								</span>
							{/if}
							{#if agents.length > 0 || questionsFor.get(p.id) || (p.signals?.ssh_sessions ?? 0) > 0}
								<span class="mt-2 flex flex-wrap items-center gap-1.5">
									{#if questionsFor.get(p.id)}
										{@const n = questionsFor.get(p.id)!}
										<span class="badge badge--warn">{n} question{n === 1 ? '' : 's'}</span>
									{/if}
									{#if agents.length > 0 || (p.signals?.ssh_sessions ?? 0) > 0}
										<AgentChips {agents} ssh={p.signals?.ssh_sessions} />
									{/if}
								</span>
							{/if}
							{#if reason(p)}
								<span class="mt-1 block text-xs text-red-700 dark:text-red-400">{reason(p)}</span>
							{/if}
						</a>
						{#if act}
							<span class="mrow-action">{@render actionButton(p, true)}</span>
						{/if}
						{#if askingStop === p.id}
							<div class="banner banner--warn mx-4 mb-4" data-testid="confirm-stop">
								<p>{stopWarning(p)}</p>
								<div class="mt-2 flex items-center gap-3">
									<button type="button" class="btn-danger btn--sm" onclick={() => stopNow(p)}
										>Stop anyway</button
									>
									<button
										type="button"
										class="btn-ghost hit"
										use:focusOnMount
										onclick={() => (askingStop = undefined)}>Keep it running</button
									>
								</div>
							</div>
						{/if}
					</li>
				{/each}
			</ul>

			<!-- The list is the one that grows to 100 (I-569), so it is capped
			     like every other growing list on the dashboard. -->
			{#if capped > 0 || showAll}
				<div class="mt-4 flex items-baseline gap-3">
					<button type="button" class="btn-ghost" onclick={() => (showAll = !showAll)}>
						{showAll ? 'Show fewer' : `Show all ${matched.length}`}
					</button>
					{#if capped > 0}
						<span class="text-sm text-ink-muted tabular-nums"
							>{rows.length} of {matched.length}</span
						>
					{/if}
				</div>
			{/if}

			<RecentActivity {projects} />
		{/if}
		<RecentlyDestroyed
			{destroyed}
			hasMore={destroyedHasMore}
			onmore={moreDestroyed}
			liveSlugs={(projects ?? []).map((p) => p.slug)}
			onrestored={() => void refresh()}
		/>
	</LoadState>
</PageShell>

<style>
	/* Cells sit on their first line's baseline, so a name, a state and a
	   size read as one line across the row. */
	.ptable th {
		white-space: nowrap;
	}
	.ptable td {
		vertical-align: baseline;
		padding-top: 0.875rem;
		padding-bottom: 0.875rem;
	}
	/* Inset 16px at both ends, as the strip's and the callout's text is,
	   so a hovered row's fill has room around its first and last words. */
	.ptable :is(th, td):first-child {
		padding-left: 1rem;
	}
	.ptable :is(th, td):last-child {
		padding-right: 0.75rem;
	}
	/* The search and the filters on one line, under the strip and over the
	   list; on a phone the search takes the row and the filters sit under
	   it, so neither is squeezed. */
	.ptools {
		display: flex;
		flex-wrap: wrap;
		/* Bottom-aligned, so the current filter's underline lands on the
		   toolbar's own rule, as the project's tabs do. */
		align-items: flex-end;
		justify-content: space-between;
		column-gap: 1.5rem;
		border-bottom: 1px solid var(--rule);
	}
	.pt-search {
		width: 100%;
		max-width: 18rem;
		margin-bottom: 0.5rem;
	}
	@media (width >= 40rem) {
		.pt-search {
			width: 16rem;
		}
	}
	.prow {
		position: relative;
		transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.prow:hover,
	.prow:has(.pname:focus-visible) {
		background-color: color-mix(in srgb, var(--sunken) 60%, transparent);
	}
	/* A frozen machine's row (I-683): a cold wash and an ice edge on its
	   left, so the whole row reads as frozen, not only its state word. */
	.prow--frozen,
	.mrow--frozen {
		background-color: color-mix(in srgb, var(--frost) 6%, transparent);
		box-shadow: inset 2px 0 0 var(--frost);
	}
	.prow--frozen:hover,
	.prow--frozen:has(.pname:focus-visible) {
		background-color: color-mix(in srgb, var(--frost) 10%, transparent);
	}
	.frost-sub {
		color: var(--frost);
	}
	/* The remote is cut with an ellipsis (its whole value in the title)
	   until lg, so the table fits the frame from sm up; from lg it takes
	   its column. */
	.pcell-name,
	.premote {
		max-width: 14rem;
	}
	@media (width >= 64rem) {
		.pcell-name {
			max-width: none;
		}
		.premote {
			max-width: 40rem;
		}
	}
	.pname {
		color: var(--ink);
		text-decoration: underline;
		text-decoration-color: var(--rule-strong);
		text-decoration-thickness: 1px;
		text-underline-offset: 4px;
	}
	.prow:hover .pname {
		text-decoration-color: currentColor;
	}
	/* The name's link covers the row; the Start or Stop button sits above. */
	.pname::after {
		content: '';
		position: absolute;
		inset: 0;
	}
	/* Fixed, so the columns hold still as Stop turns to Start. */
	.pcell-actions {
		width: 5.5rem;
		white-space: nowrap;
		text-align: right;
	}
	.row-action {
		position: relative;
		z-index: 10;
		opacity: 0;
		transition:
			opacity 150ms cubic-bezier(0.4, 0, 0.2, 1),
			background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.prow:hover .row-action,
	.prow:focus-within .row-action,
	.row-action--shown {
		opacity: 1;
	}
	@media (hover: none) {
		.row-action {
			opacity: 1;
		}
	}
	.pchev {
		color: var(--ink-faint);
		opacity: 0;
		transition: opacity 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.prow:hover .pchev {
		opacity: 1;
	}

	.disk--full {
		color: var(--color-amber-800);
	}
	@media (prefers-color-scheme: dark) {
		.disk--full {
			color: var(--color-amber-300);
		}
	}

	.mrow {
		display: block;
		padding: 0.75rem 0;
		color: inherit;
	}
	/* Room on the right for the button over the first line. */
	.mrow--act {
		padding-right: 5.75rem;
	}
	.mrow-action {
		position: absolute;
		top: 0.25rem;
		right: 0;
	}

	.empty {
		display: grid;
		overflow: hidden;
		border: 1px solid var(--rule);
		border-radius: var(--radius-md);
		background: var(--surface);
	}
	.empty-main {
		min-width: 0;
		padding: 1.5rem;
	}
	.empty-main > * {
		max-width: 36rem;
	}
	.empty-side {
		min-width: 0;
		margin: 0;
		padding: 1.5rem;
		border-width: 1px 0 0;
		border-style: solid;
		border-color: var(--rule);
		border-radius: 0;
	}
	.empty-side--plain {
		border-color: var(--rule);
		background: transparent;
	}
	/* The strip draws its own cells and padding. */
	.empty-side--strip {
		padding: 0;
	}
	@media (width >= 64rem) {
		.empty--side {
			grid-template-columns: minmax(0, 1fr) 22rem;
		}
		.empty-side {
			border-width: 0 0 0 1px;
		}
	}
</style>
