<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		getMe,
		getProject,
		listProjects,
		startProject,
		stopProject,
		destroyProject,
		resizeProject,
		getOp,
		listEvents,
		listSnapshots,
		createSnapshot,
		restoreSnapshot,
		listRevisions
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible, pollUntilDone } from '$lib/poll';
	import { uptime, gb, relativeTime, dateTime, normalizeRemoteDisplay } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import { abuseStopReason } from '$lib/abuse';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import QuestionsCard from '$lib/components/QuestionsCard.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import RestoreNameForm from '$lib/components/RestoreNameForm.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type {
		Me,
		PaymentRequiredReason,
		Project,
		ProjectEvent,
		Snapshot,
		Revision
	} from '$lib/api/types';

	const id = page.params.id as string;

	let project = $state<Project | undefined>(undefined);
	let notFound = $state(false);
	/** The first load failed; the poll keeps trying, and Retry asks now. */
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	let lastUpdated = $state<Date | undefined>(undefined);
	// The poll keeps the newest EVENTS_POLL; "Show older" pages back from
	// the oldest one held (I-414). The card shows `eventsShown` of them.
	const EVENTS_POLL = 50;
	const EVENTS_STEP = 20;
	let events = $state<ProjectEvent[]>([]);
	let olderEvents = $state<ProjectEvent[]>([]);
	let olderDone = $state(false);
	let loadingOlder = $state(false);
	let eventsShown = $state(EVENTS_STEP);
	const allEvents = $derived.by(() => {
		const byId = new Map([...olderEvents, ...events].map((e) => [e.id, e]));
		return [...byId.values()].sort((a, b) => (a.ts < b.ts ? 1 : a.ts > b.ts ? -1 : 0));
	});
	// Fewer than a full poll means the poll already holds every event.
	const noOlder = $derived(olderDone || (olderEvents.length === 0 && events.length < EVENTS_POLL));
	const canShowOlder = $derived(allEvents.length > eventsShown || !noOlder);
	let snapshots = $state<Snapshot[]>([]);
	let revisions = $state<Revision[]>([]);
	let me = $state<Me | undefined>(undefined);

	// Start/stop/destroy.
	let opBusy = $state<'start' | 'stop' | 'destroy' | 'resize' | 'snapshot' | 'restore' | undefined>(
		undefined
	);
	let startBanner = $state<'payment_required' | 'capacity' | undefined>(undefined);
	/** The refusal behind a payment_required banner: its sentence and reason (api.md). */
	let refusal = $state<
		| { message: string; reason?: PaymentRequiredReason; projects: { slug: string; id: string }[] }
		| undefined
	>(undefined);
	let stopSnapshotFirst = $state(true);

	// Resize. A volume only grows, so the choices are the sizes strictly
	// larger than the disk now, and the select opens on the smallest of them.
	const GIB = 1024 * 1024 * 1024;
	const SIZES_GB = [20, 40, 80, 160, 320];
	let showResize = $state(false);
	let largerSizes = $derived(
		project ? SIZES_GB.filter((s) => s * GIB > (project?.volume_bytes ?? Infinity)) : []
	);
	let resizeTo = $state<number | undefined>(undefined);
	// A poll can grow the disk under an open panel (another tab, the CLI);
	// the choice then moves to the next size that is still a grow.
	$effect(() => {
		if (showResize && (resizeTo === undefined || !largerSizes.includes(resizeTo))) {
			resizeTo = largerSizes[0];
		}
	});

	let resizeForm = $state<HTMLFormElement | undefined>(undefined);

	function openResize() {
		resizeTo = largerSizes[0];
		showResize = true;
	}

	/**
	 * Close the resize panel. Focus goes back to the Resize… button (or, when
	 * the disk is now at the largest size, to the sentence that replaces it),
	 * but only if it was inside the panel: a grow ends after a poll, and by
	 * then the person may be somewhere else on the page.
	 */
	function closeResize() {
		const hadFocus = !!resizeForm?.contains(document.activeElement);
		showResize = false;
		if (hadFocus) void focusAfterRender('resize-open', 'resize-largest');
	}

	// Restore. One snapshot at a time has a panel open under it: either the
	// typed confirm for restoring over this disk, or the name for a new
	// project.
	let restorePanel = $state<{ snapshotId: string; kind: 'replace' | 'new' } | undefined>(undefined);
	let restoreAsNewName = $state('');
	let restoreAsNewError = $state<string | undefined>(undefined);

	function openRestore(snapshotId: string, kind: 'replace' | 'new') {
		restorePanel = { snapshotId, kind };
		restoreAsNewName = '';
		restoreAsNewError = undefined;
	}

	/** Close a restore panel and give focus back to the button that opened it. */
	function closeRestore() {
		const open = restorePanel;
		restorePanel = undefined;
		if (open) void focusAfterRender(`restore-${open.kind}-${open.snapshotId}`);
	}

	// One failure, one report (I-393): the page's polls, the questions
	// card's included, share one toast, raised by the first to fail and
	// gone when all of them get through (I-395), and none while the load
	// banner or the outage bar already says it. The events and snapshots
	// polls stay quiet while the project itself has not loaded, since the
	// banner covers the page.
	const pollFailures = new PollGroup();
	const projectFailure = new PollFailure('Could not load the project.', pollFailures);
	const eventsFailure = new PollFailure('Could not load events.', pollFailures);
	const snapshotsFailure = new PollFailure('Could not load snapshots.', pollFailures);

	async function refresh() {
		try {
			project = await getProject(id);
			lastUpdated = new Date();
			notFound = false;
			loadFailed = false;
			projectFailure.ok();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'not_found') {
				notFound = true;
				return;
			}
			// Before the first load the banner says why, in the toast's words
			// (loadErrorText); a toast as well would say it twice. After it,
			// what is on screen stays and one toast reports the refresh.
			if (!project) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load the project.');
			}
			projectFailure.fail(err, !project);
		}
	}

	async function refreshEvents() {
		try {
			events = await listEvents(id, { limit: EVENTS_POLL });
			eventsFailure.ok();
		} catch (err) {
			eventsFailure.fail(err, !project);
		}
	}

	async function showOlderEvents() {
		// Fetch when the next step would run past what is held.
		if (allEvents.length < eventsShown + EVENTS_STEP && !noOlder) {
			loadingOlder = true;
			try {
				const page = await listEvents(id, {
					before: allEvents[allEvents.length - 1].id,
					limit: EVENTS_POLL
				});
				olderEvents = [...olderEvents, ...page];
				if (page.length < EVENTS_POLL) olderDone = true;
			} catch (err) {
				eventsFailure.fail(err, false);
				return;
			} finally {
				loadingOlder = false;
			}
		}
		eventsShown += EVENTS_STEP;
	}

	async function refreshSnapshots() {
		try {
			snapshots = await listSnapshots(id);
			snapshotsFailure.ok();
		} catch (err) {
			snapshotsFailure.fail(err, !project);
		}
	}

	async function refreshRevisions() {
		try {
			revisions = await listRevisions(id);
		} catch {
			// The config card degrades to "—" below; the config page is the
			// place that must work.
		}
	}

	async function refreshMe() {
		try {
			me = await getMe();
		} catch {
			// The plan card degrades to the class alone.
		}
	}

	onMount(() => {
		const stop1 = pollWhileVisible(refresh);
		const stop2 = pollWhileVisible(refreshEvents);
		const stop3 = pollWhileVisible(refreshSnapshots);
		void refreshRevisions();
		void refreshMe();
		return () => {
			stop1();
			stop2();
			stop3();
		};
	});

	/**
	 * A payment_required refusal, kept whole: api.md's sentence, its
	 * reason, and for plan_limit the machines using the memory, resolved to
	 * this user's projects so each can be stopped from here.
	 */
	async function explainRefusal(err: ApiError) {
		const detail = err.detail ?? {};
		const reason =
			typeof detail.reason === 'string' ? (detail.reason as PaymentRequiredReason) : undefined;
		const slugs = Array.isArray(detail.projects)
			? detail.projects.filter((s): s is string => typeof s === 'string')
			: [];
		let named: { slug: string; id: string }[] = [];
		if (slugs.length > 0) {
			try {
				const mine = await listProjects();
				named = slugs.flatMap((slug) => {
					const p = mine.find((q) => q.slug === slug);
					return p ? [{ slug, id: p.id }] : [];
				});
			} catch {
				// The sentence still names them.
			}
		}
		refusal = { message: err.message, reason, projects: named };
	}

	async function stopNamed(target: { slug: string; id: string }) {
		opBusy = 'stop';
		try {
			const { op_id } = await stopProject(target.id, true);
			waitForOp(
				op_id,
				() => {
					opBusy = undefined;
					if (refusal)
						refusal = { ...refusal, projects: refusal.projects.filter((p) => p.id !== target.id) };
					toast.success(`Stopped ${target.slug}.`);
				},
				target.id
			);
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, `Could not stop ${target.slug}.`);
		}
	}

	function waitForOp(opId: string, onDone: () => void, projectId = id) {
		pollUntilDone(async () => {
			try {
				const op = await getOp(projectId, opId);
				if (op.state === 'done') {
					onDone();
					return true;
				}
				if (op.state === 'error') {
					toast.error(op.error ?? 'The operation failed.');
					opBusy = undefined;
					return true;
				}
				return false;
			} catch (err) {
				toastApiError(err);
				opBusy = undefined;
				return true;
			}
		});
	}

	async function onStart() {
		opBusy = 'start';
		startBanner = undefined;
		try {
			const { op_id } = await startProject(id);
			waitForOp(op_id, () => {
				opBusy = undefined;
				void refresh();
			});
		} catch (err) {
			opBusy = undefined;
			if (err instanceof ApiError && err.code === 'payment_required') {
				startBanner = err.code;
				await explainRefusal(err);
				return;
			}
			if (err instanceof ApiError && err.code === 'capacity') {
				startBanner = err.code;
				return;
			}
			toastApiError(err, 'Could not start the project.');
		}
	}

	async function onStop() {
		opBusy = 'stop';
		try {
			const { op_id } = await stopProject(id, stopSnapshotFirst);
			waitForOp(op_id, () => {
				opBusy = undefined;
				void refresh();
			});
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, 'Could not stop the project.');
		}
	}

	async function onDestroy() {
		opBusy = 'destroy';
		try {
			await destroyProject(id);
			// The destroy runs on after this returns (I-166); the list shows it
			// as destroying, then under "Recently destroyed" with a Restore.
			toast.success(
				`Destroying ${project?.name}. It can be restored from "Recently destroyed" for 30 days.`
			);
			await goto(resolve('/projects'));
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, 'Could not destroy the project.');
		}
	}

	async function onResize() {
		// The select only offers grows, but the api must never be asked to
		// shrink a disk from a button labelled Grow, whatever the select holds.
		if (!project || resizeTo === undefined || resizeTo * GIB <= project.volume_bytes) return;
		opBusy = 'resize';
		try {
			const { op_id } = await resizeProject(id, resizeTo * GIB);
			waitForOp(op_id, async () => {
				opBusy = undefined;
				await refresh();
				closeResize();
			});
		} catch (err) {
			opBusy = undefined;
			// disk_limit is a plan's refusal, told like a start's (api.md).
			if (err instanceof ApiError && err.code === 'payment_required') {
				startBanner = err.code;
				closeResize();
				await explainRefusal(err);
				return;
			}
			toastApiError(err, 'Could not resize the volume.');
		}
	}

	async function onCreateSnapshot() {
		opBusy = 'snapshot';
		try {
			const { op_id } = await createSnapshot(id);
			waitForOp(op_id, () => {
				opBusy = undefined;
				void refreshSnapshots();
			});
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, 'Could not take a snapshot.');
		}
	}

	/**
	 * Restores a snapshot over this project's disk, or into a new project
	 * when asNew names one. Restoring over is confirmed by typing the slug in
	 * the panel (ConfirmType), the same as Destroy, because it is as final.
	 */
	async function onRestore(snapshotId: string, asNew?: string) {
		opBusy = 'restore';
		restoreAsNewError = undefined;
		try {
			const { op_id } = await restoreSnapshot(id, snapshotId, asNew);
			waitForOp(op_id, () => {
				opBusy = undefined;
				restorePanel = undefined;
				restoreAsNewName = '';
				toast.success(
					asNew
						? `Created ${asNew} from the snapshot.`
						: `Restored ${project?.name ?? 'the project'}.`
				);
				void refresh();
				void refreshSnapshots();
			});
		} catch (err) {
			opBusy = undefined;
			// A refused name belongs under the field, like "Recently destroyed".
			if (asNew && err instanceof ApiError && (err.code === 'conflict' || err.code === 'invalid')) {
				restoreAsNewError =
					err.detail?.reason === 'name_taken'
						? `A project called ${asNew} already exists; pick another name.`
						: err.message;
				return;
			}
			toastApiError(err, 'Could not restore the snapshot.');
		}
	}

	function currentRevisionStatus(): Revision | undefined {
		return revisions.find((r) => r.revision_id === project?.config_revision_id);
	}

	const CLASS_GB: Record<string, number> = { small: 4, large: 8, xl: 16 };

	/** "8 GB of 8 GB": the class's memory, of what the plan runs at once. */
	let memoryLine = $derived.by(() => {
		if (!project) return '—';
		const own = CLASS_GB[project.class];
		if (own === undefined) return '—';
		return me && me.limits.memory_gb > 0 ? `${own} GB of ${me.limits.memory_gb} GB` : `${own} GB`;
	});

	/** The link a payment_required reason wants, on the billing page. */
	function refusalLink(reason?: PaymentRequiredReason): string {
		switch (reason) {
			case 'subscription_required':
				return 'Choose a plan';
			case 'plan_limit':
				return 'Upgrade';
			case 'disk_limit':
				return 'Change plan';
			case 'egress_limit':
				return 'See usage';
			case 'past_due':
				return 'Update card';
			case 'suspended':
				return 'Pay the invoice';
			default:
				return 'Billing';
		}
	}
</script>

<svelte:head>
	<title>{project ? `${project.name} — repose` : 'Project — repose'}</title>
</svelte:head>

{#if notFound}
	<PageShell title="Not found" crumbs={[{ label: 'Projects', href: resolve('/projects') }]}>
		<p class="text-sm text-ink-muted">This project doesn't exist, or isn't yours.</p>
	</PageShell>
{:else if !project}
	<!-- The h1 stays a name while the project loads: "Loading…" as the
	     page's heading is what a screen reader announced as its title. -->
	<PageShell title="Project" crumbs={[{ label: 'Projects', href: resolve('/projects') }]}>
		<LoadState status={loadFailed ? 'failed' : 'loading'} onretry={refresh} error={loadError} />
	</PageShell>
{:else}
	<PageShell title={project.name} crumbs={[{ label: 'Projects', href: resolve('/projects') }]}>
		{#snippet action()}
			<div class="flex items-center gap-2">
				{#if project?.state === 'stopped'}
					<button type="button" class="btn" disabled={!!opBusy} onclick={onStart}>
						{opBusy === 'start' ? 'Starting…' : 'Start'}
					</button>
				{:else if project?.state === 'running'}
					<label class="check-row">
						<input type="checkbox" bind:checked={stopSnapshotFirst} />
						Snapshot on stop
					</label>
					<button type="button" class="btn" disabled={!!opBusy} onclick={onStop}>
						{opBusy === 'stop' ? 'Stopping…' : 'Stop'}
					</button>
				{/if}
			</div>
		{/snippet}

		<p class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-ink-muted">
			<span class="text-ink"><StateDot state={project.state} /></span>
			<span class="font-mono">{project.class}</span>
			{#if project.state === 'running'}
				<span class="tabular-nums">up {uptime(project.started_at)}</span>
			{/if}
			<a href={resolve('/projects/[id]/config', { id })} class="link">Config</a>
			<a href={resolve('/projects/[id]/secrets', { id })} class="link">Secrets</a>
		</p>

		{#if abuseStopReason(project)}
			<div class="banner banner--warn mt-4" data-testid="abuse-stop">
				{abuseStopReason(project)}
			</div>
		{/if}

		{#if startBanner === 'payment_required'}
			<div
				class="banner banner--warn mt-4"
				data-testid="refusal"
				data-reason={refusal?.reason ?? 'unknown'}
			>
				<p>
					{refusal?.message ?? 'A plan is needed before a machine can start.'}
					<a href={resolve('/billing')} class="link">{refusalLink(refusal?.reason)}</a>
				</p>
				{#if refusal && refusal.projects.length > 0}
					<div class="mt-2 flex flex-wrap gap-2">
						{#each refusal.projects as named (named.id)}
							<button
								type="button"
								class="btn-quiet btn--sm"
								disabled={!!opBusy}
								onclick={() => stopNamed(named)}>Stop {named.slug}</button
							>
						{/each}
					</div>
				{/if}
			</div>
		{:else if startBanner === 'capacity'}
			<div class="banner banner--warn mt-4">No capacity right now, try again in a few minutes.</div>
		{/if}

		<QuestionsCard projectId={id} {pollFailures} />

		<!-- Each card is a section of the page under its h1, so its title is
		     an h2 at the one h2 size every dashboard page uses (text-xl), the
		     same as billing's plan cards and the Destroy section below. -->
		<div class="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
			<div class="card min-w-0">
				<h2 class="text-xl font-semibold">Connect</h2>
				<code class="codeblock mt-3 block px-3 py-2">repose run</code>
				<code class="codeblock mt-2 block px-3 py-2">ssh {project.slug}.repose</code>
				{#if project.remote_url}
					<!-- wrap-anywhere, not break-all: the URL breaks at a hyphen or a
					     slash first, and mid-word only for a part wider than the card.
					     break-all split "kanali-with-a-longer-name" as "…-long" / "er-name". -->
					<p class="mt-2 font-mono text-compact wrap-anywhere text-ink-muted">
						{normalizeRemoteDisplay(project.remote_url)}
					</p>
				{/if}
			</div>

			<div class="card">
				<h2 class="text-xl font-semibold">Signals</h2>
				{#if project.signals}
					<dl class="mt-3 space-y-1 text-sm">
						<div class="flex justify-between gap-4">
							<dt class="text-ink-muted">SSH sessions</dt>
							<dd class="tabular-nums">{project.signals.ssh_sessions}</dd>
						</div>
						<div class="flex justify-between gap-4">
							<dt class="text-ink-muted">tmux clients</dt>
							<dd class="tabular-nums">{project.signals.tmux_clients}</dd>
						</div>
						{#if project.signals.agents.length === 0}
							<div class="flex justify-between gap-4">
								<dt class="text-ink-muted">Agents</dt>
								<dd>none</dd>
							</div>
						{:else}
							{#each project.signals.agents as a (a.window)}
								<div class="flex justify-between gap-4">
									<dt class="text-ink-muted">{a.agent} ({a.window})</dt>
									<dd>{a.state}</dd>
								</div>
							{/each}
						{/if}
					</dl>
				{:else}
					<p class="mt-3 text-sm text-ink-muted">Not running.</p>
				{/if}
				{#if lastUpdated}
					<p class="mt-3 text-xs text-ink-muted">
						Updated {relativeTime(lastUpdated.toISOString())}
					</p>
				{/if}
			</div>

			<div class="card">
				<h2 class="text-xl font-semibold">Plan</h2>
				<dl class="mt-3 space-y-1 text-sm">
					<div class="flex justify-between gap-4">
						<dt class="text-ink-muted">Memory while running</dt>
						<dd class="tabular-nums">{memoryLine}</dd>
					</div>
					{#if me?.billing.plan}
						<div class="flex justify-between gap-4">
							<dt class="text-ink-muted">Plan</dt>
							<dd class="capitalize">{me.billing.plan}</dd>
						</div>
					{/if}
				</dl>
				<a href={resolve('/billing')} class="link mt-2 inline-block text-sm">Billing</a>
			</div>

			<div class="card">
				<h2 class="text-xl font-semibold">Disk</h2>
				<p class="mt-3 text-sm tabular-nums">
					{project.disk_used_bytes !== undefined ? gb(project.disk_used_bytes) : '—'} of {gb(
						project.volume_bytes
					)}
				</p>
				{#if !showResize}
					{#if largerSizes.length > 0}
						<button type="button" id="resize-open" class="btn-ghost mt-2 px-0" onclick={openResize}
							>Resize…</button
						>
					{:else}
						<!-- tabindex=-1: after a grow to the largest size, focus comes
						     here in place of the button that went away. -->
						<p id="resize-largest" tabindex="-1" class="mt-2 text-sm text-ink-muted tabular-nums">
							{SIZES_GB[SIZES_GB.length - 1]} GB is the largest size.
						</p>
					{/if}
				{:else}
					<!-- The panel takes the Resize… button's place, so the select
					     takes its focus (DESIGN-LANGUAGE.md, "Focus follows the
					     panel"), and Cancel gives it back. -->
					<form
						class="mt-3"
						bind:this={resizeForm}
						onsubmit={(e) => {
							e.preventDefault();
							void onResize();
						}}
					>
						<label for="resize-to" class="block text-sm text-ink-muted">Grow to</label>
						<div class="mt-1.5 flex flex-wrap items-center gap-2">
							<select
								id="resize-to"
								class="field tabular-nums"
								bind:value={resizeTo}
								use:focusOnMount
							>
								{#each largerSizes as s (s)}
									<option value={s}>{s} GB</option>
								{/each}
							</select>
							<button type="submit" class="btn" disabled={!!opBusy || resizeTo === undefined}>
								{opBusy === 'resize' ? 'Resizing…' : 'Grow'}
							</button>
							<button type="button" class="btn-ghost" onclick={closeResize}>Cancel</button>
						</div>
					</form>
				{/if}
			</div>

			<div class="card">
				<h2 class="text-xl font-semibold">Last build</h2>
				{#if currentRevisionStatus()}
					{@const rev = currentRevisionStatus()}
					<p class="mt-3 text-sm">
						<span
							class="badge"
							class:badge--new={rev?.status === 'applied'}
							class:badge--error={rev?.status === 'failed'}>{rev?.status}</span
						>
					</p>
				{:else}
					<p class="mt-3 text-sm text-ink-muted">base {project.base_version}</p>
				{/if}
				<a href={resolve('/projects/[id]/config', { id })} class="link mt-2 inline-block text-sm"
					>View config</a
				>
			</div>

			<div class="card sm:col-span-2">
				<h2 class="text-xl font-semibold">Events</h2>
				{#if events.length === 0}
					<p class="mt-3 text-sm text-ink-muted">No events yet.</p>
				{:else}
					<ul class="mt-2">
						{#each allEvents.slice(0, eventsShown) as e (e.id)}
							<!-- Baseline, so the smaller time sits on the summary's line. -->
							<li class="row flex items-baseline justify-between gap-4 text-sm">
								<span class="flex min-w-0 flex-wrap items-baseline gap-2">
									{#if e.agent}<span class="badge">{e.agent}</span>{/if}
									<span>{e.summary}</span>
								</span>
								<span class="shrink-0 text-xs text-ink-muted tabular-nums"
									>{relativeTime(e.ts)}</span
								>
							</li>
						{/each}
					</ul>
					{#if canShowOlder}
						<div class="mt-3 flex items-baseline gap-3">
							<button
								type="button"
								class="btn-ghost px-0"
								disabled={loadingOlder}
								onclick={showOlderEvents}>{loadingOlder ? 'Loading…' : 'Show older'}</button
							>
							<span class="text-sm text-ink-muted tabular-nums"
								>{Math.min(eventsShown, allEvents.length)} shown</span
							>
						</div>
					{/if}
				{/if}
			</div>

			<div class="card sm:col-span-2">
				<div class="flex items-center justify-between gap-4">
					<h2 class="text-xl font-semibold">Snapshots</h2>
					<!-- -mr-2 takes back the ghost button's padding, so "Create" ends
					     on the card's edge like the rows' actions below it. -->
					<button
						type="button"
						class="btn-ghost -mr-2"
						disabled={!!opBusy}
						onclick={onCreateSnapshot}
					>
						{opBusy === 'snapshot' ? 'Snapshotting…' : 'Create'}
					</button>
				</div>
				{#if snapshots.length === 0}
					<p class="mt-3 text-sm text-ink-muted">No snapshots yet.</p>
				{:else}
					<ul class="mt-2">
						{#each snapshots as s (s.id)}
							{@const open = restorePanel?.snapshotId === s.id ? restorePanel.kind : undefined}
							<li class="row text-sm" data-testid="snapshot-row">
								<!-- Wraps: at 390px the actions drop under the date instead
								     of squeezing it to a word a line. -->
								<div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
									<span class="tabular-nums"
										>{dateTime(s.created_at)} · {gb(s.bytes)} · {s.reason}</span
									>
									{#if !open}
										<!-- -mx-2 takes back the ghost buttons' own padding, so their
										     words line up with the card's edge whether the actions sit
										     at the right or wrap under the date. -->
										<span class="-mx-2 flex flex-wrap items-center">
											<button
												type="button"
												id={`restore-replace-${s.id}`}
												class="btn-ghost-danger"
												disabled={!!opBusy}
												onclick={() => openRestore(s.id, 'replace')}>Restore…</button
											>
											<button
												type="button"
												id={`restore-new-${s.id}`}
												class="btn-ghost"
												disabled={!!opBusy}
												onclick={() => openRestore(s.id, 'new')}>Restore as new…</button
											>
										</span>
									{/if}
								</div>
								{#if open === 'replace'}
									<div class="mt-3 rounded-sm border p-4 border-rule" data-testid="restore-confirm">
										<p>
											Restoring replaces the disk of {project.slug} with this snapshot. Anything written
											since {dateTime(s.created_at)} is lost.
										</p>
										{#if project.state !== 'stopped'}
											<p class="mt-2 text-ink-muted">
												Stop {project.slug} first, or restore the snapshot as a new project.
											</p>
										{/if}
										<div class="mt-3">
											<ConfirmType
												word={project.slug}
												label="Restore"
												busyLabel="Restoring…"
												busy={opBusy === 'restore'}
												disabled={!!opBusy || project.state !== 'stopped'}
												onconfirm={() => onRestore(s.id)}
												autofocus
												oncancel={closeRestore}
											/>
										</div>
									</div>
								{:else if open === 'new'}
									<RestoreNameForm
										id={s.id}
										bind:value={restoreAsNewName}
										error={restoreAsNewError}
										busy={opBusy === 'restore'}
										submitLabel="Restore as new"
										onsubmit={(n) => void onRestore(s.id, n)}
										oncancel={closeRestore}
									/>
								{/if}
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</div>

		<div class="form-section">
			<h2 class="text-xl font-semibold text-red-700 dark:text-red-400">Destroy</h2>
			<p class="mt-1 text-sm text-ink-muted">
				Deletes the volume. The last snapshot is kept 30 days.
			</p>
			<div class="mt-3">
				<ConfirmType
					word={project.slug}
					label="Destroy"
					busyLabel="Destroying…"
					busy={opBusy === 'destroy'}
					disabled={!!opBusy}
					onconfirm={onDestroy}
				/>
			</div>
		</div>
	</PageShell>
{/if}
