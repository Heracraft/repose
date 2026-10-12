<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { getBilling, getMe, listDestroyed, listProjects, listQuestions } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
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
	import { DISK_FULL_PERCENT } from '$lib/disk';
	import { DESTROYED_PAGE } from '$lib/destroyed';
	import RecentlyDestroyed from '$lib/components/RecentlyDestroyed.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import PlanStrip from '$lib/components/projects/PlanStrip.svelte';
	import PendingQuestions from '$lib/components/projects/PendingQuestions.svelte';
	import { pendingQuestions } from '$lib/questions.svelte';
	import AgentChips from '$lib/components/projects/AgentChips.svelte';
	import RecentActivity from '$lib/components/projects/RecentActivity.svelte';
	import { runRowAction, type RowAction } from '$lib/components/projects/actions';
	import type { Billing, DestroyedProject, Me, Project, Question } from '$lib/api/types';

	let projects = $state<Project[] | undefined>(undefined);
	/** The first load failed; the poll keeps trying, and Retry asks now. */
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	// The poll keeps the newest DESTROYED_PAGE; "Show more" past them
	// fetches the page before the oldest held (I-420).
	let destroyedNewest = $state<DestroyedProject[]>([]);
	let destroyedOlder = $state<DestroyedProject[]>([]);
	let destroyedDone = $state(false);
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
		try {
			projects = await listProjects();
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
		const side = Promise.all([refreshQuestions(), refreshBilling()]);
		// Its own try: an api without the route (older than I-167) answers
		// 404, and that must not hide the live list or raise a toast.
		try {
			destroyedNewest = await listDestroyed();
		} catch {
			destroyedNewest = [];
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

	async function act(p: Project, action: RowAction) {
		if (pending[p.id]) return;
		pending = { ...pending, [p.id]: action };
		await runRowAction(action, p);
		await refresh();
		const rest = { ...pending };
		delete rest[p.id];
		pending = rest;
	}

	/** The guest's root filesystem used, in percent, with its figures for a title. */
	function disk(p: Project): { pct: number; used: string; title: string } | undefined {
		const used = p.root_used_bytes;
		const size = p.root_size_bytes ?? p.volume_bytes;
		if (used === undefined || !size || size <= 0 || used < 0) return undefined;
		const pct = Math.min(100, Math.floor((used * 100) / size));
		return { pct, used: gb(used), title: `${gb(used)} of ${gb(size)}` };
	}

	let summary = $derived.by(() => {
		if (!projects || projects.length === 0) return undefined;
		const awake = projects.filter((p) => p.state === 'running').length;
		return [awake > 0 ? `${awake} running` : 'No machines running', planName]
			.filter(Boolean)
			.join(' · ');
	});
</script>

{#snippet stateCell(p: Project)}
	<StateDot state={p.state} />
	{#if p.state === 'running' && p.idle}
		<!-- Nobody on it for a day, holding its memory against the plan
		     (I-262, I-289, I-617). -->
		<div class="mt-0.5 text-xs text-amber-700 tabular-nums dark:text-amber-400">
			unused {uptime(p.idle.since)}
		</div>
	{:else if p.state === 'running'}
		<div class="mt-0.5 text-xs text-ink-muted tabular-nums">up {uptime(p.started_at)}</div>
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
	{:else}
		<span class="font-mono text-compact text-ink-muted">{gb(p.volume_bytes)}</span>
	{/if}
{/snippet}

{#snippet actionButton(p: Project, always: boolean)}
	{@const a = actionFor(p)}
	{#if a}
		<button
			type="button"
			class="row-action btn-quiet btn--sm"
			class:row-action--shown={always || !!pending[p.id]}
			disabled={!!pending[p.id]}
			aria-label="{a === 'stop' ? 'Stop' : 'Start'} {p.name}"
			onclick={() => act(p, a)}
			>{pending[p.id] === 'stop'
				? 'Stopping…'
				: pending[p.id] === 'start'
					? 'Starting…'
					: a === 'stop'
						? 'Stop'
						: 'Start'}</button
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
			<div class="max-w-xl">
				{#if me?.waitlist && holdActive && me.waitlist.hold_until}
					<div class="banner banner--ok mb-6" data-testid="seat-held">
						Your seat is held until {dateTime(me.waitlist.hold_until)}.
						<a href={resolve('/billing')} class="link">Choose a plan</a> before then.
					</div>
				{:else if me?.waitlist}
					<div class="banner banner--warn mb-6" data-testid="waitlist-place">
						repose is full. You’re number {me.waitlist.position} on the waitlist; we’ll email
						{me.email} when a seat frees, and you’ll have 72 hours to choose a plan.
					</div>
				{:else if me?.billing.status === 'none'}
					<div class="banner mb-6" data-testid="no-plan">
						<a href={resolve('/billing')} class="link">Choose a plan</a> before your first machine can
						start. Seven days free, card at checkout.
					</div>
				{/if}
				<h2 class="text-xl font-semibold">No projects yet</h2>
				<p class="mt-2 text-ink-muted">
					A git checkout becomes a project, with a machine of its own, the first time you run this
					in it.
				</p>
				<div class="mt-4 max-w-sm">
					<CodeCopy command="repose run" what="repose run" />
				</div>
			</div>
		{:else if projects}
			<div class="flex flex-col gap-6">
				<PendingQuestions {questions} />
				<PlanStrip {projects} billing={billingOff ? undefined : billing} />
			</div>

			<!-- From sm up, a table. A region with a name and a tab stop: between
			     sm and lg the table can still run past the frame, and a keyboard
			     can only scroll what it can focus. -->
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<div
				class="mt-8 hidden overflow-x-auto sm:block"
				role="region"
				aria-label="Projects"
				tabindex="0"
			>
				<table class="table ptable">
					<thead>
						<tr>
							<th>Project</th>
							<th>State</th>
							<th class="hidden lg:table-cell">Size</th>
							<th>Agents</th>
							<th class="hidden md:table-cell">Disk</th>
							<th class="hidden xl:table-cell">Last snapshot</th>
							<th><span class="sr-only">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each projects as p (p.id)}
							{@const remote = p.remote_url ? normalizeRemoteDisplay(p.remote_url) : ''}
							<tr class="prow">
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
											<span class="badge badge--warn">question</span>
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
									<AgentChips agents={p.signals?.agents} ssh={p.signals?.ssh_sessions} />
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
						{/each}
					</tbody>
				</table>
			</div>

			<!-- Below sm, a stacked list: each project one link, its action
			     under it. -->
			<ul class="mt-6 border-t border-rule-strong sm:hidden" aria-label="Projects">
				{#each projects as p (p.id)}
					{@const remote = p.remote_url ? normalizeRemoteDisplay(p.remote_url) : ''}
					{@const agents = p.signals?.agents ?? []}
					<li class="border-b border-rule">
						<a href={resolve('/projects/[id]', { id: p.id })} class="mrow">
							<span class="flex items-baseline justify-between gap-3">
								<span class="flex min-w-0 items-baseline gap-2">
									<span class="truncate font-medium">{p.name}</span>
									{#if p.expires_at}<span class="badge shrink-0">temporary</span>{/if}
								</span>
								<span class="shrink-0 text-sm"><StateDot state={p.state} /></span>
							</span>
							<span
								class="mt-1 flex min-w-0 items-baseline gap-2 font-mono text-compact text-ink-muted"
							>
								<span class="shrink-0 whitespace-nowrap">
									{p.class}{#if p.state === 'running'}&nbsp;· up {uptime(p.started_at)}{/if}
								</span>
								{#if remote}<span aria-hidden="true">·</span>
									<span class="min-w-0 truncate">{remote}</span>{/if}
							</span>
							{#if agents.length > 0 || questionsFor.get(p.id) || (p.signals?.ssh_sessions ?? 0) > 0}
								<span class="mt-2 flex flex-wrap items-center gap-1.5">
									{#if questionsFor.get(p.id)}
										<span class="badge badge--warn">question</span>
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
						{#if actionFor(p)}
							<div class="pb-3">{@render actionButton(p, true)}</div>
						{/if}
					</li>
				{/each}
			</ul>

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
	.prow {
		position: relative;
		transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.prow:hover,
	.prow:has(.pname:focus-visible) {
		background-color: color-mix(in srgb, var(--sunken) 60%, transparent);
	}
	/* The remote is cut with an ellipsis (its whole value in the title),
	   narrower until lg, so the table fits the frame from sm up. */
	.pcell-name,
	.premote {
		max-width: 14rem;
	}
	@media (width >= 64rem) {
		.pcell-name,
		.premote {
			max-width: 22rem;
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
	.pcell-actions {
		width: 1px;
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
		min-height: 3.5rem;
		padding: 0.75rem 0;
		color: inherit;
	}
	.mrow + div {
		margin-top: -0.25rem;
	}
</style>
