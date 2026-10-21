<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { listDestroyed, listProjects, listQuestions } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { billingStore } from '$lib/billing.svelte';
	import { projectsStore } from '$lib/projects.svelte';
	import { title } from '$lib/title';
	import { withoutBillingURL } from '$lib/refusal';
	import { PollFailure } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import {
		classMemoryGB,
		dateTime,
		gb,
		normalizeRemoteDisplay,
		relativeTime,
		sinceLabel,
		tempLeft,
		upLabel,
		uptime
	} from '$lib/format';
	import { clock } from '$lib/clock.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import Frost from '$lib/components/Frost.svelte';
	import CommandList from '$lib/hk/CommandList.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import { splitTicks } from '$lib/components/project/ticks';
	import { abuseStopReason } from '$lib/abuse';
	import { bootFallbackReason } from '$lib/bootfail';
	import { DISK_FULL_PERCENT, diskPercent } from '$lib/disk';
	import { DESTROYED_PAGE } from '$lib/destroyed';
	import RecentlyDestroyed from '$lib/components/RecentlyDestroyed.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import WaitingCard from '$lib/components/questions/WaitingCard.svelte';
	import { pendingQuestions } from '$lib/questions.svelte';
	import AgentChips from '$lib/components/projects/AgentChips.svelte';
	import RecentActivity from '$lib/components/projects/RecentActivity.svelte';
	import { runRowAction, type RowAction } from '$lib/components/projects/actions';
	import {
		BAND_LABELS,
		FILTERS,
		FILTER_LABELS,
		PROJECTS_SHOWN,
		TOOLBAR_FROM,
		byHeldOrder,
		holdsMemory,
		idSetKey,
		lastActive,
		listSearch,
		band,
		listView,
		matchesOnRemote,
		orderProjects,
		filterChosen,
		readFilter,
		readQuery,
		type Band,
		type Filter
	} from '$lib/projectList';
	import type { DestroyedProject, Project, Question } from '$lib/api/types';
	import { frozenSince } from '$lib/frozen';
	import { stopWarning } from '$lib/busyAgents';
	import { focusOnMount } from '$lib/focus';
	import { tabStopWhenScrolls } from '$lib/scroller';
	import { inDash } from '$lib/dash';

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
	/**
	 * The account, from the dashboard's one billing store (I-841): its
	 * billing status for the past-due and suspended banners, and the seats
	 * and waitlist state of a user with no project yet (I-290).
	 */
	let me = $derived(billingStore.me);
	let holdActive = $derived(
		!!me?.waitlist?.hold_until && new Date(me.waitlist.hold_until).getTime() > Date.now()
	);

	/**
	 * Whether Recent activity's first load has answered. Recently destroyed
	 * sits under it and waits until then: Recent activity mounts after its
	 * own reads, and mounting above a painted Recently destroyed pushed it
	 * down 174px, a CLS of 0.044 to 0.056 at 1440 (I-664, I-841).
	 */
	let activitySettled = $state(false);
	/** Whether the first read of the destroyed list has answered. */
	let destroyedSettled = $state(false);

	// The sections arrive one after another, and the page is long by the
	// time the last one lands: the footer stays under the fold until then,
	// so it is not pushed out of view as each arrives (I-664, I-841).
	const dash = inDash();
	const release = dash.hold();
	$effect(() => {
		if (loadFailed || (activitySettled && destroyedSettled)) release();
	});
	$effect(() => release);

	/** Questions agents are waiting on, across every project (I-245). */
	let questions = $state<Question[]>([]);
	let questionsFor = $derived(
		questions.reduce(
			(m, q) => m.set(q.project_id, (m.get(q.project_id) ?? 0) + 1),
			new Map<string, number>()
		)
	);

	// A failing refresh toasts once, not on every poll (I-393).
	const listFailure = new PollFailure('Could not load projects.');

	async function refresh() {
		const first = projects === undefined;
		try {
			// The first load asks for the questions with the list, so the
			// card above the table is in the first paint and nothing lands
			// above the table later (DESIGN-LANGUAGE "The pages"). It is a
			// side read, so passive: its answer must not clear the outage bar
			// a failing list raises at the same time (I-393).
			if (first) {
				// The account rides in the first load too, from the shared
				// store (one read with the rail's), so the past-due banner is
				// in the first paint ("Nothing mounts above painted content",
				// I-680).
				const [list, qs] = await Promise.all([
					listProjects(),
					listQuestions('pending', { passive: true }).catch(() => undefined),
					billingStore.refresh({ maxAge: 30_000 })
				]);
				if (qs) {
					questions = qs;
					pendingQuestions.set(qs);
				}
				projects = list;
			} else {
				projects = await listProjects();
			}
			// No project, no Recent activity to wait for.
			if (projects.length === 0) activitySettled = true;
			// The rail lists the same projects: it follows this poll with no
			// read of its own (I-841).
			projectsStore.set(projects);
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
		// The questions' call goes out once the list is in; it fails on its
		// own and keeps what it showed last.
		const side = first ? undefined : refreshQuestions();
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
			destroyedSettled = true;
		}
		// The account's status moves with a payment, a few times a month,
		// and the rail reads it every 60s. An account with no project yet
		// waits on a seat or a plan, so it is read on every tick.
		if (!first && projects.length === 0) await billingStore.refresh({ passive: false });
		await side;
	}

	async function refreshQuestions() {
		try {
			questions = await listQuestions('pending');
			pendingQuestions.set(questions);
		} catch {
			// The callout keeps the questions it showed; the list's own
			// failure is the one reported.
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

	onMount(() => {
		// The State cell's "up 3m" moves with the 30-second clock, not the poll.
		const stopClock = clock.use();
		const stopPoll = pollWhileVisible(refresh);
		return () => {
			stopClock();
			stopPoll();
		};
	});

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
	 * would end an agent's turn (I-614). Every row shows its Stop, so in
	 * a long list a stray click lands on one easily, and it had no undo.
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
				refusal = { message: 'No capacity right now.', projects: [] };
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
			`${n} ${n === 1 ? 'project' : 'projects'}`
		]
			.filter(Boolean)
			.join(' · ');
	});

	/** Whether an agent is waiting on an answer about this project. */
	const waitingOn = (p: Project) => !!questionsFor.get(p.id);

	// The filter and the search live in the URL, so Back steps between
	// them and a reload keeps the list the user was reading.
	let filter = $derived(readFilter(page.url.searchParams));
	let chosen = $derived(filterChosen(page.url.searchParams));
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
	/** The band each row was in when the order was taken, held with it. */
	let heldBands = $state(new Map<string, Band>());
	// The key is a $derived so the effect runs again only when the set of
	// projects actually changes; depending on `projects` itself would
	// re-run it on every 10s poll, which is the whole point of holding.
	let orderKey = $derived(idSetKey(projects ?? []));
	$effect(() => {
		void orderKey;
		untrack(() => {
			const ordered = orderProjects(projects ?? [], waitingOn);
			held = ordered.map((p) => p.id);
			heldBands = new Map(ordered.map((p) => [p.id, band(p, waitingOn(p))]));
		});
	});
	const bandOf = (p: Project): Band => heldBands.get(p.id) ?? band(p, waitingOn(p));
	/** Rows drawn: Stopped and All show PROJECTS_SHOWN until "Show all" is pressed. */
	let showAll = $state(false);
	let view = $derived(
		listView([...(projects ?? [])].sort(byHeldOrder(held)), {
			filter,
			chosen,
			query,
			waiting: waitingOn,
			bandOf,
			cap: showAll ? Number.POSITIVE_INFINITY : PROJECTS_SHOWN
		})
	);
	let drawn = $derived(view.groups.reduce((n, g) => n + g.rows.length, 0));
	let capped = $derived(view.folded > 0 ? 0 : view.matched - drawn);
	/** Band heads, where more than one band is drawn. */
	let heads = $derived(view.groups.length > 1);
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
			const search = listSearch(page.url.searchParams, chosen ? filter : undefined, value);
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- starts with resolve()
			void goto(`${resolve('/projects')}${search}`, {
				keepFocus: true,
				noScroll: true,
				replaceState: true
			});
			typed = undefined;
		}, 200);
	}

	$effect(() => () => clearTimeout(debounce));
</script>

{#snippet ticks(text: string)}{#each splitTicks(text) as part, i (i)}{#if part.code}<code
				>{part.text}</code
			>{:else}{part.text}{/if}{/each}{/snippet}

<!-- The state cell: the dot and its word, then one 12px line of what the
     state means right now. -->
{#snippet stateCell(p: Project)}
	{@const frozen = frozenSince(p)}
	<StateDot state={p.state} frozen={!!frozen} />
	{#if frozen}
		<span class="sub">{sinceLabel(frozen, 'for', clock.now)}</span>
	{:else if p.state === 'running' && p.idle}
		<!-- Nobody on it for a day, holding its memory against the plan
		     (I-262, I-289, I-617). -->
		<span class="sub">unused {uptime(p.idle.since)}</span>
	{:else if p.state === 'running'}
		<span class="sub">{upLabel(p.started_at, clock.now)}</span>
	{:else if p.state === 'stopped'}
		<span class="sub"
			>{p.started_at
				? `last run ${relativeTime(lastActive(p))}`
				: `made ${relativeTime(p.created_at)}`}</span
		>
	{/if}
	{#if p.host_unreachable}
		<!-- A host going unreachable takes every machine on it at once. -->
		<span class="sub">server not answering</span>
	{/if}
	{#if p.expires_at}
		<span class="sub">{tempLeft(p.expires_at)}</span>
	{/if}
	{#if reason(p)}
		<span class="sub sub--reason">{@render ticks(reason(p))}</span>
	{/if}
{/snippet}

{#snippet diskCell(p: Project)}
	<!-- What the guest holds, as a figure; past 90% it says how full. -->
	{@const d = disk(p)}
	{#if d}
		<span title={d.title}>{d.pct >= DISK_FULL_PERCENT ? `${d.pct}% full` : d.used}</span>
	{/if}
{/snippet}

{#snippet actionButton(p: Project)}
	{@const a = actionFor(p)}
	{#if a}
		<!-- One word for the face and the accessible name (WCAG 2.5.3);
		     aria-disabled while it runs, so focus stays (I-391). -->
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
			class="hk-btn-ghost hk-btn-sm row-action"
			aria-disabled={pending[p.id] ? 'true' : undefined}
			aria-label="{word} {p.name}"
			onclick={() => pressed(p, a)}>{word}</button
		>
	{/if}
{/snippet}

{#snippet confirmStop(p: Project)}
	<div class="hk-confirm">
		<p>{stopWarning(p)}</p>
		<div class="hk-confirm-actions">
			<button type="button" class="hk-btn-danger hk-btn-sm" onclick={() => stopNow(p)}
				>Stop anyway</button
			>
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm"
				use:focusOnMount
				onclick={() => (askingStop = undefined)}>Keep it running</button
			>
		</div>
	</div>
{/snippet}

{#snippet questionsBadge(p: Project)}
	{@const n = questionsFor.get(p.id)}
	{#if n}
		<span class="hk-badge hk-badge--warn">{n} question{n === 1 ? '' : 's'}</span>
	{/if}
{/snippet}

<svelte:head>
	<title>{title('Projects')}</title>
</svelte:head>

<!-- Until the list answers, the lede's line is held empty, so the page
     under it does not drop 28px when the count arrives (I-664). -->
<PageShell title="Projects" lede={projects === undefined ? '\u00a0' : summary}>
	<LoadState
		status={projects !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={refresh}
		error={loadError}
	>
		<!-- The account's payment state, one sentence each; the full rule is
		     on /billing. Below 1024px the plan line under the strip says it
		     already, so the banner gives way there (I-841). -->
		{#if me?.billing.status === 'suspended'}
			<div
				class="hk-banner hk-banner--err pay-banner"
				role="alert"
				data-testid="projects-suspended"
			>
				<p>
					Your account is suspended. <a href={resolve('/billing')}>Update card and pay</a>
				</p>
			</div>
		{:else if me?.billing.status === 'past_due'}
			<div class="hk-banner pay-banner" role="status" data-testid="projects-past-due">
				<p>Your last payment failed. <a href={resolve('/billing')}>Update card</a></p>
			</div>
		{/if}

		{#if projects && projects.length === 0}
			{@const banner =
				me?.waitlist && holdActive && me.waitlist.hold_until
					? 'held'
					: me?.waitlist
						? 'waitlist'
						: me?.billing.status === 'none'
							? 'none'
							: undefined}
			<div class="empty">
				{#if banner === 'held' && me?.waitlist?.hold_until}
					<div class="hk-banner hk-banner--ok" role="status" data-testid="seat-held">
						<p>
							Your seat is held until {dateTime(me.waitlist.hold_until)}.
							<a href={resolve('/billing')}>Choose a plan</a> before then.
						</p>
					</div>
				{:else if banner === 'waitlist' && me?.waitlist}
					<div class="hk-banner hk-banner--info" role="status" data-testid="waitlist-place">
						<p>
							repose is full. You’re number {me.waitlist.position} on the waitlist; we’ll email
							{me.email} when a seat frees, and you’ll have 72 hours to choose a plan.
						</p>
					</div>
				{:else if banner === 'none'}
					<div class="hk-empty-row" data-testid="no-plan">
						<span
							>Choose a plan before your first machine can start. Seven days free, card at checkout.</span
						>
						<a href={resolve('/billing')} class="hk-btn-secondary hk-btn-sm">Choose a plan</a>
					</div>
				{/if}
				<div class="hk-empty-row">
					<span>No projects yet.</span>
					<a href={resolve('/docs')} class="hk-more empty-more">Quickstart</a>
				</div>
				<CommandList
					class="empty-commands"
					label="Commands"
					commands={[
						{ command: INSTALL_COMMAND, what: 'the install command' },
						{ command: 'repose login', what: 'repose login' },
						{
							command: 'repose run',
							what: 'repose run',
							note: 'in a git checkout on your laptop'
						}
					]}
				/>
			</div>
		{:else if projects}
			{#if refusal}
				<!-- A plan or capacity refusal from a row, drawn as the project
				     page draws it: the api's sentence, a link to /billing, and a
				     Stop for each machine holding the memory. -->
				<div class="hk-banner refusal" role="status" data-testid="row-refusal">
					<p>
						{withoutBillingURL(refusal.message)}
						{#if refusal.reason}
							<a href={resolve('/billing')}>See your plan</a>
						{/if}
					</p>
					{#if refusal.projects.length > 0}
						<div class="refusal-stops">
							{#each refusal.projects as named (named.id)}
								<button
									type="button"
									class="hk-btn-secondary hk-btn-sm"
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

			<WaitingCard bind:questions limit={1} />

			<div class="hk-section list">
				<!-- A search and the state filters, once the list is long enough
				     to need them; both live in the URL. -->
				{#if toolbar}
					<div class="ptools">
						<input
							type="search"
							class="hk-input hk-input--search pt-search"
							aria-label="Search projects"
							placeholder="Search"
							value={searchBox}
							oninput={(e) => onSearch(e.currentTarget.value)}
						/>
						<nav aria-label="Filter projects" class="hk-seg--links pt-filter">
							{#each FILTERS as f (f)}
								<!-- eslint-disable svelte/no-navigation-without-resolve -- filterHref starts with resolve() -->
								<a
									href={filterHref(f)}
									aria-current={view.filter === f ? 'page' : undefined}
									data-sveltekit-noscroll
									data-sveltekit-keepfocus
									data-sveltekit-replacestate>{FILTER_LABELS[f]}</a
								>
								<!-- eslint-enable svelte/no-navigation-without-resolve -->
							{/each}
						</nav>
					</div>
				{/if}

				{#if drawn === 0}
					<div class="hk-empty-row" data-testid="no-matches">
						<span>No project matches.</span>
						<a href={resolve('/projects')} class="hk-link">Clear</a>
					</div>
				{:else}
					<!-- From a 600px column, a table, laid out by the column's width
					     (@container dash, I-841). Where it scrolls sideways it is a
					     named region with a tab stop. Past one band, each band has
					     a head row; Running ends on the stopped machines that ran
					     last and a link to the rest. -->
					<div class="hk-table-wrap ptable-wrap" use:tabStopWhenScrolls={'Projects'}>
						<table class="hk-table hk-table--hover ptable">
							<thead>
								<tr>
									<th scope="col" class="c-name">Project</th>
									<th scope="col" class="c-state">State</th>
									<th scope="col" class="c-size">Size</th>
									<th scope="col" class="c-agents">Agents</th>
									<th scope="col" class="c-disk hk-num-col">Disk</th>
									<th scope="col" class="c-act"><span class="hk-sr">Actions</span></th>
								</tr>
							</thead>
							{#each view.groups as g (g.band)}
								<tbody data-band={g.band}>
									{#if heads}
										<tr class="band-row">
											<th scope="rowgroup" colspan="6">
												<span class="band-head">
													<span>{BAND_LABELS[g.band]}</span>
													{#if g.band === 'off' && view.folded > 0}
														<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- filterHref starts with resolve() -->
														<a href={filterHref('off')} class="band-all" data-testid="all-stopped"
															>All {view.resting} stopped</a
														>
													{/if}
												</span>
											</th>
										</tr>
									{/if}
									{#each g.rows as p, i (p.id)}
										{@const remote = p.remote_url ? normalizeRemoteDisplay(p.remote_url) : ''}
										{@const frozen = frozenSince(p)}
										<tr
											class="prow"
											class:prow--rest={p.state === 'stopped'}
											data-frost-on={frozen ? 'row' : undefined}
										>
											<td class="c-name">
												<span class="pname-line">
													<!-- The name's ::after covers the row, so a click
													     anywhere in it opens the project. -->
													<a
														href={resolve('/projects/[id]', { id: p.id })}
														class="pname"
														title={p.state === 'stopped' && remote ? remote : undefined}>{p.name}</a
													>
													{#if p.expires_at}
														<!-- repose run --temp (I-347): destroyed with no snapshot. -->
														<span class="hk-badge hk-badge--outline">temporary</span>
													{/if}
													{@render questionsBadge(p)}
												</span>
												{#if remote}
													<!-- A stopped row hides its remote, unless the search
													     matched on it and nothing else. -->
													<span
														class="premote"
														class:premote--hit={matchesOnRemote(p, query)}
														title={remote}>{remote}</span
													>
												{/if}
												{#if frozen}<Frost kind="row" since={frozen} n={i} />{/if}
											</td>
											<td class="c-state">{@render stateCell(p)}</td>
											<td class="c-size whitespace-nowrap">
												<span class="hk-mono">{p.class}</span>{#if classMemoryGB(p.class)}<span
														class="muted">&nbsp;· {classMemoryGB(p.class)} GB</span
													>{/if}
											</td>
											<td class="c-agents">
												<AgentChips agents={p.signals?.agents} asked={!!questionsFor.get(p.id)} />
											</td>
											<td class="c-disk hk-num-col whitespace-nowrap">{@render diskCell(p)}</td>
											<td class="c-act">{@render actionButton(p)}</td>
										</tr>
										{#if askingStop === p.id}
											<tr class="confirm-row" data-testid="confirm-stop">
												<td colspan="6">{@render confirmStop(p)}</td>
											</tr>
										{/if}
									{/each}
								</tbody>
							{/each}
						</table>
					</div>

					<!-- Below a 600px column, stacked rows: the name and its Start or
					     Stop on the first line, the state and the figures on the second. -->
					<div class="mlist">
						{#each view.groups as g (g.band)}
							{#if heads}
								<p class="band-head mband" id="mband-{g.band}">
									<span>{BAND_LABELS[g.band]}</span>
									{#if g.band === 'off' && view.folded > 0}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- filterHref starts with resolve() -->
										<a href={filterHref('off')} class="band-all">All {view.resting} stopped</a>
									{/if}
								</p>
							{/if}
							<ul
								class="mgroup"
								data-band={g.band}
								aria-label={heads ? undefined : 'Projects'}
								aria-labelledby={heads ? `mband-${g.band}` : undefined}
							>
								{#each g.rows as p, i (p.id)}
									{@const agents = p.signals?.agents ?? []}
									{@const frozen = frozenSince(p)}
									{@const d = disk(p)}
									{@const facts = [
										frozen ? sinceLabel(frozen, 'for', clock.now) : '',
										p.state === 'stopped'
											? p.started_at
												? `last run ${relativeTime(lastActive(p))}`
												: `made ${relativeTime(p.created_at)}`
											: '',
										`${p.class}${classMemoryGB(p.class) ? ` · ${classMemoryGB(p.class)} GB` : ''}`,
										agents.map((a) => a.agent).join(', '),
										d ? (d.pct >= DISK_FULL_PERCENT ? `${d.pct}% full` : d.used) : ''
									].filter(Boolean)}
									<li class="mrow" data-frost-on={frozen ? 'row' : undefined}>
										<div class="mrow-line">
											<span class="mrow-name">
												<a href={resolve('/projects/[id]', { id: p.id })} class="pname">{p.name}</a>
												{#if p.expires_at}<span class="hk-badge hk-badge--outline">temporary</span
													>{/if}
												{@render questionsBadge(p)}
											</span>
											{@render actionButton(p)}
										</div>
										{#if p.remote_url && matchesOnRemote(p, query)}
											<span class="premote">{normalizeRemoteDisplay(p.remote_url)}</span>
										{/if}
										<div class="mrow-meta">
											<StateDot state={p.state} frozen={!!frozen} />
											{#each facts as f (f)}<span aria-hidden="true">·</span><span>{f}</span>{/each}
										</div>
										<!-- A temporary machine's countdown and the unused warning
										     are losses the reader cannot undo, so the stacked row
										     carries them as the table's state cell does. -->
										{#if p.expires_at || (p.state === 'running' && p.idle) || p.host_unreachable || reason(p)}
											<div class="mrow-sub">
												{#if p.host_unreachable}<span>server not answering</span>{/if}
												{#if p.state === 'running' && p.idle}<span
														>unused {uptime(p.idle.since)}</span
													>{/if}
												{#if p.expires_at}<span>{tempLeft(p.expires_at)}</span>{/if}
												{#if reason(p)}<span>{@render ticks(reason(p))}</span>{/if}
											</div>
										{/if}
										{#if askingStop === p.id}
											<div class="mrow-confirm" data-testid="confirm-stop">
												{@render confirmStop(p)}
											</div>
										{/if}
										{#if frozen}<Frost kind="row" since={frozen} n={i} />{/if}
									</li>
								{/each}
							</ul>
						{/each}
					</div>
				{/if}

				<!-- Stopped and All grow to 100 (I-569), so they are capped like
				     every other growing list on the dashboard. -->
				{#if capped > 0 || (showAll && view.matched > PROJECTS_SHOWN)}
					<div class="pmore">
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm"
							onclick={() => (showAll = !showAll)}
						>
							{showAll ? 'Show fewer' : `Show all ${view.matched}`}
						</button>
						{#if capped > 0}
							<span>{drawn} of {view.matched}</span>
						{/if}
					</div>
				{/if}
			</div>

			<RecentActivity {projects} onsettled={() => (activitySettled = true)} />
		{/if}
		{#if activitySettled}
			<RecentlyDestroyed
				{destroyed}
				hasMore={destroyedHasMore}
				onmore={moreDestroyed}
				liveSlugs={(projects ?? []).map((p) => p.slug)}
				onrestored={() => void refresh()}
			/>
		{/if}
	</LoadState>
</PageShell>

<style>
	/* ---- The table (from a 600px column) ----
	   The column, not the window, decides (I-841): at 1024 the rail leaves
	   the page 700px. Disk joins at 680px and Size at 800px, so Start and
	   Stop always fit. */
	.ptable-wrap {
		display: none;
	}
	.ptable .c-size,
	.ptable .c-disk {
		display: none;
	}
	@container dash (min-width: 600px) {
		.ptable-wrap {
			display: block;
		}
		.mlist {
			display: none;
		}
	}
	@container dash (min-width: 680px) {
		.ptable .c-disk {
			display: table-cell;
		}
	}
	@container dash (min-width: 800px) {
		.ptable .c-size {
			display: table-cell;
		}
	}
	.ptable td {
		padding-top: 0.875rem;
		padding-bottom: 0.875rem;
		vertical-align: top;
	}
	/* Fixed widths, so a filter or a search never moves the columns:
	   Disk and the action are sized to their content, the rest share
	   what is left. */
	.ptable {
		table-layout: fixed;
	}
	/* A hidden column keeps its width in a fixed table, so it has none. */
	.ptable .c-size,
	.ptable .c-disk {
		width: 0;
	}
	.ptable .c-name {
		width: 36%;
	}
	.ptable .c-state {
		width: 26%;
	}
	.ptable .c-agents {
		width: 19%;
	}
	@container dash (min-width: 680px) {
		.ptable .c-disk {
			width: 3.5rem;
		}
	}
	@container dash (min-width: 800px) {
		.ptable .c-name {
			width: 31%;
		}
		.ptable .c-state {
			width: 22.5%;
		}
		.ptable .c-size {
			width: 14%;
		}
		.ptable .c-agents {
			width: 17%;
		}
	}
	.ptable .c-act {
		width: 4.25rem;
		padding-left: 1.5rem;
		text-align: right;
		white-space: nowrap;
	}
	.ptable td.c-act {
		padding-top: 0.6875rem;
		padding-bottom: 0;
	}
	.prow {
		position: relative;
	}
	.pname-line,
	.mrow-name {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.5rem;
		min-width: 0;
	}
	.pname {
		font-size: 0.875rem;
		font-weight: 500;
		color: var(--hk-text);
		text-decoration: none;
	}
	.prow:hover .pname,
	.mrow:hover .pname {
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	/* The name's link covers its row; the row's action sits above it. */
	.pname::after {
		content: '';
		position: absolute;
		inset: 0;
	}
	.pname:focus-visible {
		outline: none;
	}
	/* The row has no side padding, so the ring stands 8px outside it and
	   clears the name, the remote and the action (review r1 dash-r1-3). */
	.pname:focus-visible::after {
		inset: 0 -0.5rem;
		outline: 2px solid var(--hk-accent);
		outline-offset: -2px;
		border-radius: var(--hk-radius-sm);
	}
	.premote {
		display: block;
		max-width: 22rem;
		margin-top: 0.125rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.c-state :global(.state) {
		font-size: 0.875rem;
	}
	.sub {
		display: block;
		margin-top: 0.125rem;
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.sub--reason {
		max-width: 14rem;
		white-space: normal;
	}
	.muted {
		color: var(--hk-text-3);
	}
	/* Always visible, above the row's link, its text flush with the
	   frame's edge like every ghost on the page. The scroll box grows its
	   end inset to the ghost's 10px, so the pull stays inside it and the
	   table does not scroll sideways when the button takes focus. */
	.list .hk-table-wrap {
		padding-inline-end: 0.625rem;
		margin-inline-end: -0.625rem;
	}
	.row-action {
		position: relative;
		z-index: 1;
		margin-right: -0.625rem;
	}
	/* Forced colours draw the ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.row-action,
		.mrow .row-action,
		.pmore .hk-btn-ghost {
			margin-inline: 0;
		}
	}
	.confirm-row td {
		padding-top: 0;
		border-top: 0;
	}
	.prow:has(+ .confirm-row) td {
		background: transparent;
	}

	/* ---- Band heads (past one band) ----
	   The rail's group head, in the column: 11px 500 uppercase in
	   --hk-text-3, so a band reads as a label over its rows, not a row. */
	.band-row th {
		padding: 1.75rem 0 0.5rem;
		border-bottom: 0;
	}
	.ptable tbody:first-of-type .band-row th {
		padding-top: 1rem;
	}
	.band-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		margin: 0;
		font-size: 0.6875rem;
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.band-all {
		font-size: 0.8125rem;
		letter-spacing: 0;
		text-transform: none;
		color: var(--hk-accent);
		text-decoration: none;
	}
	.band-all:hover {
		text-decoration: underline;
	}
	.mband {
		padding: 1.5rem 0 0.5rem;
	}
	.mband:first-child {
		padding-top: 0.5rem;
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.band-all {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.75rem;
		}
	}

	/* ---- Stopped rows: one line each ----
	   An account is mostly stopped machines, so a stopped row takes one
	   line: the name, and "last run" beside the state. Its remote is in
	   the name's title, and the search still finds it. */
	.ptable .prow--rest td {
		padding-top: 0.625rem;
		padding-bottom: 0.625rem;
	}
	.ptable .prow--rest td.c-act {
		padding-top: 0.375rem;
	}
	.prow--rest .premote:not(.premote--hit) {
		display: none;
	}
	.prow--rest .c-state {
		white-space: nowrap;
	}
	.prow--rest .sub {
		display: inline;
		margin: 0 0 0 0.5rem;
	}

	/* ---- The toolbar ---- */
	.ptools {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem 1.5rem;
		margin-bottom: 1rem;
	}
	.pt-search {
		max-width: 18rem;
	}
	@media (max-width: 639.98px) {
		.pt-search {
			max-width: none;
		}
		.pt-filter {
			width: 100%;
		}
		/* The three share the track, so it has no empty end. */
		.pt-filter > a {
			flex: 1 1 0;
		}
	}

	/* ---- Stacked rows (below 640px) ---- */
	.mgroup {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	/* One rule where two sections meet: right under the questions, the
	   first row leaves its top rule to the questions' last one. */
	:global(#questions) + .list:not(:has(.ptools)) .mrow:first-child {
		border-top: 0;
	}
	.mrow {
		position: relative;
		min-height: 3.5rem;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.mrow .pname {
		font-size: 0.9375rem;
	}
	.mrow-line {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
	}
	.mrow-line :global(.row-action) {
		margin-block: -0.5rem;
	}
	.mrow-meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.125rem 0.375rem;
		margin-top: 0.125rem;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.mrow-meta :global(.state) {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.mrow-meta :global(.state[data-frozen]) {
		color: var(--hk-ice-ink);
	}
	.mrow-sub {
		display: flex;
		flex-wrap: wrap;
		gap: 0 0.75rem;
		margin-top: 0.25rem;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.mrow-confirm {
		position: relative;
		z-index: 1;
		margin-top: 0.625rem;
	}

	.pmore {
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
		margin-top: 0.5rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.pmore .hk-btn-ghost {
		margin-left: -0.625rem;
	}
	.refusal-stops {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}

	@media (max-width: 1023.98px) {
		.pay-banner {
			display: none;
		}
	}

	/* ---- No projects ---- */
	.empty :global(.hk-empty-row + .hk-empty-row) {
		border-top: 0;
	}
	.empty-more {
		margin-top: 0;
	}
	.empty :global(.empty-commands) {
		margin-top: 1rem;
	}
</style>
