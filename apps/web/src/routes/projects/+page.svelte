<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { getMe, listDestroyed, listProjects } from '$lib/api/client';
	import { PollFailure } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { dateTime, normalizeRemoteDisplay, tempLeft, uptime } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import { abuseStopReason } from '$lib/abuse';
	import { DESTROYED_PAGE } from '$lib/destroyed';
	import RecentlyDestroyed from '$lib/components/RecentlyDestroyed.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { DestroyedProject, Me, Project } from '$lib/api/types';

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
			// Not asked while the list fails: its answer would reset the
			// "cannot reach the api" bar the failed list just raised.
			return;
		}
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
	}

	/**
	 * The sentence after "code: " in last_error, for a project in error or
	 * one the platform stopped because a miner was running (I-239).
	 */
	function reason(p: Project): string {
		const abuse = abuseStopReason(p);
		if (abuse) return abuse;
		if (p.state !== 'error' || !p.last_error) return '';
		const i = p.last_error.indexOf(': ');
		return i > 0 ? p.last_error.slice(i + 2) : p.last_error;
	}

	onMount(() => pollWhileVisible(refresh));

	function agentSummary(p: Project): string {
		const agents = p.signals?.agents ?? [];
		if (agents.length === 0) return '—';
		// guestd's `unknown` (quiet for less than the idle time) is not named.
		return agents
			.map((a) => (a.state && a.state !== 'unknown' ? `${a.agent} · ${a.state}` : a.agent))
			.join(', ');
	}

	let summary = $derived.by(() => {
		if (!projects || projects.length === 0) return undefined;
		const awake = projects.filter((p) => p.state === 'running').length;
		return [
			`${projects.length} ${projects.length === 1 ? 'project' : 'projects'}`,
			`${awake} running`
		].join(' · ');
	});
</script>

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
					Projects are created by <code>repose run</code> in a git checkout.
				</p>
			</div>
		{:else if projects}
			<!-- A region with a name and a tab stop: at phone width the table
			     scrolls sideways, and a keyboard can only scroll what it can
			     focus (the links in the first column never bring the others in). -->
			<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
			<div class="overflow-x-auto" role="region" aria-label="Projects" tabindex="0">
				<table class="table">
					<thead>
						<tr>
							<th>Project</th>
							<th>State</th>
							<th>Size</th>
							<th>Agents</th>
						</tr>
					</thead>
					<tbody>
						{#each projects as p (p.id)}
							<tr>
								<td>
									<!-- Underlined at rest, in a quiet rule colour: the name is
									     the only link in the row, and on touch there is no hover
									     to find it by. -->
									<a
										href={resolve('/projects/[id]', { id: p.id })}
										class="font-medium underline decoration-rule-strong decoration-1 underline-offset-4 hover:decoration-current"
										>{p.name}</a
									>
									{#if p.expires_at}
										<!-- repose run --temp (I-347): destroyed with no snapshot. -->
										<span class="badge ml-1.5 align-middle">temporary</span>
									{/if}
									{#if p.remote_url}
										<!-- A phone breaks the URL after a slash or a hyphen
										     ("github.com/", "heracraft/job-", "alerts" at 390), not
										     mid-word as "herac/raft" did; wrap-anywhere is left for a
										     segment longer than the column. -->
										<div class="mt-0.5 font-mono text-compact wrap-anywhere text-ink-muted">
											{#each normalizeRemoteDisplay(p.remote_url).split('/') as part, i (i)}{#if i}/<wbr
													/>{/if}{part}{/each}
										</div>
									{/if}
								</td>
								<td>
									<StateDot state={p.state} />
									{#if p.state === 'running'}
										<div class="mt-0.5 text-xs text-ink-muted tabular-nums">
											up {uptime(p.started_at)}
										</div>
									{/if}
									{#if p.state === 'running' && p.idle}
										<!-- Nobody on it for a day, still running and holding its
										     memory against the plan (I-262, I-289). -->
										<div class="mt-0.5 text-xs text-amber-700 tabular-nums dark:text-amber-400">
											idle {uptime(p.idle.since)} · still running
										</div>
									{/if}
									{#if p.expires_at}
										<div class="mt-0.5 text-xs text-ink-muted tabular-nums">
											{tempLeft(p.expires_at)}
										</div>
									{/if}
									{#if reason(p)}
										<div class="mt-0.5 max-w-xs text-xs text-red-700 dark:text-red-400">
											{reason(p)}
										</div>
									{/if}
								</td>
								<!-- leading-5 gives the 13px mono the 20px line of the text-sm
								     cells beside it, so the size sits on the state's baseline. -->
								<td class="font-mono text-compact leading-5">{p.class}</td>
								<td class="text-ink-muted">{agentSummary(p)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
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
