<script lang="ts">
	import Snowflake from '$lib/components/Snowflake.svelte';
	import { frozenSince } from '$lib/frozen';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		getProject,
		listProjects,
		startProject,
		stopProject,
		destroyProject,
		resizeProject,
		getOp,
		listSnapshots,
		createSnapshot,
		restoreSnapshot,
		listRevisions,
		getSamples,
		listProjectQuestions,
		getBilling,
		getUsage,
		patchProject
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible, pollUntilDone } from '$lib/poll';
	import {
		bytes,
		classMemoryGB,
		classVCPUs,
		dateOnly,
		gb,
		gbs,
		normalizeRemoteDisplay,
		tempLeft,
		uptime
	} from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { abuseStopReason } from '$lib/abuse';
	import { bootFallbackReason } from '$lib/bootfail';
	import { withoutBillingURL } from '$lib/refusal';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import WaitingCard from '$lib/components/questions/WaitingCard.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import UsageChart from '$lib/components/UsageChart.svelte';
	import EventsCard, {
		fetchEventsPage,
		type LoadedEvents
	} from '$lib/components/project/EventsCard.svelte';
	import { readEventsState } from '$lib/components/project/eventsUrl';
	import MachineCard, { DISK_SIZES_GB } from '$lib/components/project/MachineCard.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { stopWarning } from '$lib/busyAgents';
	import AgentsCard from '$lib/components/project/AgentsCard.svelte';
	import DetailsList from '$lib/components/project/DetailsList.svelte';
	import SnapshotsTable from '$lib/components/project/SnapshotsTable.svelte';
	import { pct, cpuTime, latest, type Pt } from '$lib/usage';
	import type {
		PaymentRequiredReason,
		Project,
		Question,
		Snapshot,
		Revision,
		Samples,
		SampleWindow
	} from '$lib/api/types';

	const id = page.params.id as string;

	let project = $state<Project | undefined>(undefined);
	let notFound = $state(false);
	/** The first load failed; the poll keeps trying, and Retry asks now. */
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	let lastUpdated = $state<Date | undefined>(undefined);
	let snapshots = $state<Snapshot[]>([]);
	let revisions = $state<Revision[]>([]);
	let questions = $state<Question[]>([]);
	/** The events page the first load fetched, handed to the card so it paints with the rest. */
	let firstEvents = $state<LoadedEvents | undefined>(undefined);
	/** This project's egress this billing period, in GB; undefined with billing off. */
	let egressGb = $state<number | undefined>(undefined);

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

	// One failure, one report (I-393): the page's polls, the questions
	// card's and the events card's included, share one toast, raised by the
	// first to fail and gone when all of them get through (I-395), and none
	// while the load banner or the outage bar already says it. The
	// snapshots poll stays quiet while the project itself has not loaded,
	// since the banner covers the page.
	const pollFailures = new PollGroup();
	const projectFailure = new PollFailure('Could not load the project.', pollFailures);
	const snapshotsFailure = new PollFailure('Could not load snapshots.', pollFailures);

	/**
	 * The first load asks for everything the page paints at once: the
	 * project, its questions, the events page the URL names, the snapshots,
	 * the samples, the plan and the config revisions. Nothing then lands
	 * above content already on screen (the questions card did, and pushed
	 * the page down a few hundred ms in). Only the project's failure stops
	 * the page; each other part falls back to its own empty state and its
	 * own poll.
	 */
	async function firstLoad() {
		const [p, qs, ev, snaps, smp, revs] = await Promise.all([
			getProject(id),
			listProjectQuestions(id).catch(() => [] as Question[]),
			fetchEventsPage(id, readEventsState(page.url.searchParams)).catch(() => undefined),
			listSnapshots(id).catch(() => undefined),
			getSamples(id, samplesWindow).catch(() => undefined),
			listRevisions(id).catch(() => [] as Revision[])
		]);
		questions = qs;
		if (ev && ev !== 'newest') firstEvents = ev;
		if (snaps) snapshots = snaps;
		if (smp) {
			samples = smp;
			samplesAt = Date.now();
		}
		revisions = revs;
		project = p;
	}

	async function refresh() {
		try {
			if (!project) await firstLoad();
			else project = await getProject(id);
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

	async function refreshSnapshots() {
		try {
			snapshots = await listSnapshots(id);
			snapshotsFailure.ok();
		} catch (err) {
			snapshotsFailure.fail(err, !project);
		}
	}

	/**
	 * This project's egress since the billing period started (GET /usage,
	 * filtered to it). Billing off, or no subscription, has no period, and
	 * the reading is left out.
	 */
	async function refreshEgress() {
		try {
			const b = await getBilling();
			const from = b.subscription?.period_start;
			if (!from) return;
			// GET /usage takes days; to is the end of today, so today counts.
			const tomorrow = new Date(Date.now() + 86_400_000).toISOString();
			const days = await getUsage(dateOnly(from), dateOnly(tomorrow));
			egressGb = days.filter((d) => d.project_id === id).reduce((n, d) => n + d.egress_gb, 0);
		} catch {
			// Billing off (503 billing_disabled) or not reachable: no reading.
		}
	}

	// Usage (I-492): the api's minute samples over a window. They change
	// once a minute, so the 10 s page poll fetches them at most that often;
	// a window switch fetches at once.
	const WINDOWS: [SampleWindow, string, number][] = [
		['1h', 'Hour', 3600_000],
		['24h', 'Day', 86400_000],
		['7d', 'Week', 7 * 86400_000]
	];
	let samplesWindow = $state<SampleWindow>('1h');
	let samples = $state<Samples | undefined>(undefined);
	let samplesAt = $state(0);
	let samplesFailed = $state(false);

	async function refreshSamples(force = false) {
		if (!force && samples?.window === samplesWindow && Date.now() - samplesAt < 55_000) return;
		const want = samplesWindow;
		try {
			const got = await getSamples(id, want);
			if (want !== samplesWindow) return; // switched again while this was in flight
			samples = got;
			samplesAt = Date.now();
			samplesFailed = false;
		} catch {
			// The card says it could not load; the rest of the page stands.
			samplesFailed = true;
		}
	}

	function chooseWindow(w: SampleWindow) {
		if (w === samplesWindow) return;
		samplesWindow = w;
		void refreshSamples(true);
	}

	let span = $derived(WINDOWS.find(([w]) => w === samples?.window)?.[2] ?? 3600_000);
	let series = $derived.by(() => {
		const pts = samples?.points ?? [];
		const at = (f: (p: (typeof pts)[number]) => number | null): Pt[] =>
			pts.map((p) => ({ t: Date.parse(p.ts), v: f(p) }));
		return {
			cpu: at((p) => p.cpu),
			mem: at((p) => p.mem_used_bytes),
			pressure: at((p) => p.cpu_pressure),
			wait: at((p) => p.host_cpu_wait)
		};
	});
	/** The memory in use now: the newest sample's, while the machine runs. */
	let memNow = $derived(project?.state === 'running' ? latest(series.mem) : undefined);
	const GIB = 1024 * 1024 * 1024;

	onMount(() => {
		// The first tick of each poll is the first load's; snapshots and
		// samples come with it, so their polls skip that tick.
		const later = (fn: () => unknown) => {
			let first = true;
			return () => {
				if (first) {
					first = false;
					return;
				}
				return fn() as Promise<void>;
			};
		};
		const stop1 = pollWhileVisible(refresh);
		const stop2 = pollWhileVisible(later(refreshSnapshots));
		const stop3 = pollWhileVisible(later(() => refreshSamples()));
		void refreshEgress();
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

	/** Polls an op until it ends; onDone on success. Resolves true when it ended done. */
	function waitForOp(opId: string, onDone: () => void, projectId = id): Promise<boolean> {
		return new Promise((settle) => {
			pollUntilDone(async () => {
				try {
					const op = await getOp(projectId, opId);
					if (op.state === 'done') {
						onDone();
						settle(true);
						return true;
					}
					if (op.state === 'error') {
						toast.error(op.error ?? 'The operation failed.');
						opBusy = undefined;
						settle(false);
						return true;
					}
					return false;
				} catch (err) {
					toastApiError(err);
					opBusy = undefined;
					settle(false);
					return true;
				}
			});
		});
	}

	async function onStart() {
		if (opBusy) return;
		opBusy = 'start';
		startBanner = undefined;
		try {
			const { op_id } = await startProject(id);
			void waitForOp(op_id, () => {
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

	/**
	 * The sentence `repose stop` asks before it ends an agent's turn, or
	 * "" when nothing would be interrupted (I-614). The CLI refuses to do
	 * this silently; the dashboard did it on one click.
	 */
	let stopEnds = $derived(stopWarning(project));
	let askingStop = $state(false);

	function onStopPressed() {
		if (opBusy) return;
		if (stopEnds) {
			askingStop = true;
			return;
		}
		void onStop();
	}

	async function onStop() {
		if (opBusy) return;
		askingStop = false;
		opBusy = 'stop';
		try {
			const { op_id } = await stopProject(id, stopSnapshotFirst);
			void waitForOp(op_id, () => {
				opBusy = undefined;
				void refresh();
			});
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, 'Could not stop the project.');
		}
	}

	async function onDestroy() {
		if (opBusy) return;
		opBusy = 'destroy';
		try {
			await destroyProject(id);
			// The destroy runs on after this returns (I-166); the list shows it
			// as destroying, then under "Recently destroyed" with a Restore.
			// A temporary machine's destroy takes no snapshot, expires the
			// ones it has and never reaches "Recently destroyed" (I-347), so
			// the sentence that promises a restore is false for it.
			toast.success(
				temporary
					? `Destroying ${project?.name}. It was temporary, so there is no snapshot to restore.`
					: `Destroying ${project?.name}. It can be restored from "Recently destroyed" for 30 days.`
			);
			await goto(resolve('/projects'));
		} catch (err) {
			opBusy = undefined;
			toastApiError(err, 'Could not destroy the project.');
		}
	}

	/** Whether this machine is on the temporary timer (I-347). */
	let temporary = $derived(!!project?.expires_at);

	/**
	 * Keeps a temporary machine, as `repose keep` does: PATCH with
	 * expires_at null (api.md). The dashboard showed the countdown in
	 * three places and offered no way off it, including to the user who
	 * taps the hour-left notification on a phone.
	 */
	async function onKeep() {
		if (!project) return;
		try {
			const kept = await patchProject(project.id, { expires_at: null });
			project = kept;
			toast.success(`${kept.slug} is no longer temporary.`);
		} catch (err) {
			toastApiError(err, 'Could not keep the machine.');
		}
	}

	// Resize. The Machine card's Resize… opens a panel across the frame
	// under the card's row: the card is a third wide, and a form needs
	// room (I-680). A volume only grows, so the choices are the sizes
	// strictly larger than the disk now, and the select opens on the
	// smallest of them.
	let showResize = $state(false);
	let largerSizes = $derived(
		project ? DISK_SIZES_GB.filter((s) => s * GIB > project!.volume_bytes) : []
	);
	let resizeTo = $state<number | undefined>(undefined);
	let resizePanel = $state<HTMLElement | undefined>(undefined);
	// A poll can grow the disk under an open panel (another tab, the CLI):
	// the choice moves to the next size that is still a grow, and the panel
	// closes once none is left.
	$effect(() => {
		if (!showResize) return;
		if (largerSizes.length === 0) closeResize();
		else if (resizeTo === undefined || !largerSizes.includes(resizeTo)) resizeTo = largerSizes[0];
	});

	function toggleResize() {
		if (showResize) return closeResize();
		resizeTo = largerSizes[0];
		showResize = true;
	}

	/**
	 * Close the resize panel. Focus goes back to Resize… (or, when the disk
	 * is now at the largest size, to the sentence that replaces it), but
	 * only if it was inside the panel: a grow ends after a poll, and by then
	 * the person may be somewhere else on the page.
	 */
	function closeResize() {
		const hadFocus = !!resizePanel?.contains(document.activeElement);
		showResize = false;
		if (hadFocus) void focusAfterRender('resize-open', 'resize-largest');
	}

	async function submitResize() {
		// The select only offers grows, but the api must never be asked to
		// shrink a disk from a button labelled Grow, whatever the select holds.
		if (!project || resizeTo === undefined || resizeTo * GIB <= project.volume_bytes) return;
		if (await onResize(resizeTo * GIB)) closeResize();
	}

	/** Grows the volume; true when the resize panel should close. */
	async function onResize(bytes: number): Promise<boolean> {
		opBusy = 'resize';
		try {
			const { op_id } = await resizeProject(id, bytes);
			let grown = false;
			await waitForOp(op_id, () => {
				grown = true;
			});
			if (grown) await refresh();
			opBusy = undefined;
			return grown;
		} catch (err) {
			opBusy = undefined;
			// disk_limit is a plan's refusal, told like a start's (api.md).
			if (err instanceof ApiError && err.code === 'payment_required') {
				startBanner = err.code;
				await explainRefusal(err);
				return true;
			}
			toastApiError(err, 'Could not resize the disk.');
			return false;
		}
	}

	async function onCreateSnapshot() {
		opBusy = 'snapshot';
		try {
			const { op_id } = await createSnapshot(id);
			void waitForOp(op_id, () => {
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
	 * true once done; a string is why the api refused the new name.
	 */
	async function onRestore(snapshotId: string, asNew?: string): Promise<boolean | string> {
		opBusy = 'restore';
		try {
			const { op_id, project_id } = await restoreSnapshot(id, snapshotId, asNew);
			// An as-new restore's op belongs to the project it created, not
			// to this one, so it is polled there; under this id it never
			// reached `done` and the success this reports never fired.
			return await waitForOp(
				op_id,
				() => {
					opBusy = undefined;
					toast.success(
						asNew
							? `Created ${asNew} from the snapshot.`
							: `Restored ${project?.name ?? 'the project'}.`
					);
					// An as-new restore made a project elsewhere; go to it, as
					// restoring from "Recently destroyed" does. Telling the user
					// it exists and leaving them on the source was a dead end.
					if (asNew && project_id) {
						void goto(resolve('/projects/[id]', { id: project_id }));
						return;
					}
					void refresh();
					void refreshSnapshots();
				},
				asNew && project_id ? project_id : id
			);
		} catch (err) {
			opBusy = undefined;
			// A refused name belongs under the field, like "Recently destroyed".
			if (asNew && err instanceof ApiError && (err.code === 'conflict' || err.code === 'invalid')) {
				return err.detail?.reason === 'name_taken'
					? `A project called ${asNew} already exists; pick another name.`
					: err.message;
			}
			toastApiError(err, 'Could not restore the snapshot.');
			return false;
		}
	}

	let revision = $derived(revisions.find((r) => r.revision_id === project?.config_revision_id));

	// The size's vCPUs, for the status line, which has to say them for a
	// stopped machine too: a stopped machine is not sampled, so the
	// samples payload's own `vcpus` is not there to read (I-492). The
	// figure lives in format.ts, pinned to scheduler.ClassVCPU by
	// plans.test.ts (DECISIONS I-784, amending R3-2).
	let vcpus = $derived(project ? classVCPUs(project.class) || undefined : undefined);

	/** The sentence after "code: " in last_error, as the projects list shows it. */
	/** When the machine was frozen while idle, behind the flag (I-683). */
	let frozen = $derived(project ? frozenSince(project) : undefined);
	let errorReason = $derived.by(() => {
		if (!project || project.state !== 'error' || !project.last_error) return '';
		const i = project.last_error.indexOf(': ');
		return i > 0 ? project.last_error.slice(i + 2) : project.last_error;
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

{#snippet tabs()}<ProjectTabs {id} />{/snippet}

{#if notFound}
	<PageShell title="Not found" crumbs={[{ label: 'Projects', href: resolve('/projects') }]}>
		<p class="text-sm text-ink-muted">This project doesn't exist, or isn't yours.</p>
	</PageShell>
{:else if !project}
	<!-- The h1 stays a name while the project loads: "Loading…" as the
	     page's heading is what a screen reader announced as its title. -->
	<PageShell title="Project" crumbs={[{ label: 'Projects', href: resolve('/projects') }]} {tabs}>
		<!-- A screen's height while it loads, so the footer is not in view
		     to be pushed off it when the cards arrive (layout shift). -->
		<div class="min-h-screen">
			<LoadState status={loadFailed ? 'failed' : 'loading'} onretry={refresh} error={loadError} />
		</div>
	</PageShell>
{:else}
	<PageShell
		title={project.name}
		crumbs={[{ label: 'Projects', href: resolve('/projects') }]}
		{tabs}
	>
		{#snippet action()}
			{#if project?.state === 'stopped'}
				<button
					type="button"
					class="btn"
					aria-disabled={opBusy ? 'true' : undefined}
					onclick={onStart}
				>
					{opBusy === 'start' ? 'Starting…' : 'Start'}
				</button>
			{:else if project?.state === 'error'}
				<!-- POST /start restarts a machine in error on its newest
				     configuration (lifecycle.md). -->
				<button
					type="button"
					class="btn"
					aria-disabled={opBusy ? 'true' : undefined}
					onclick={onStart}
				>
					{opBusy === 'start' ? 'Restarting…' : 'Restart'}
				</button>
			{:else if project?.state === 'running'}
				<!-- Below sm the check sits under Stop; from sm up beside it. -->
				<div class="flex flex-col-reverse items-start gap-1 sm:flex-row sm:items-center sm:gap-4">
					<label class="check-row">
						<input type="checkbox" bind:checked={stopSnapshotFirst} />
						Snapshot on stop
					</label>
					<button
						type="button"
						class="btn-quiet"
						aria-disabled={opBusy ? 'true' : undefined}
						onclick={onStopPressed}
					>
						{opBusy === 'stop' ? 'Stopping…' : 'Stop'}
					</button>
				</div>
			{/if}
		{/snippet}

		{#if askingStop}
			<!-- The two-step the secrets page uses for Delete: the line in
			     the CLI's words, and focus on the safe choice. -->
			<div class="banner banner--warn mt-4" data-testid="confirm-stop">
				<p>{stopEnds}</p>
				<div class="mt-2 flex items-center gap-3">
					<button type="button" class="btn-danger btn--sm" onclick={onStop}>Stop anyway</button>
					<button
						type="button"
						class="btn-ghost hit"
						use:focusOnMount
						onclick={() => (askingStop = false)}>Keep it running</button
					>
				</div>
			</div>
		{/if}

		<p class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-ink-muted">
			<span class="text-ink" data-testid="project-state"
				><StateDot state={project.state} frozen={!!frozen} /></span
			>
			<span aria-hidden="true">·</span>
			<span class="font-mono text-compact" data-testid="project-class">{project.class}</span>
			{#if vcpus}
				<span aria-hidden="true">·</span>
				<span class="tabular-nums">{vcpus} vCPU</span>
				<span aria-hidden="true">·</span>
				<span class="tabular-nums">{classMemoryGB(project.class)} GB</span>
			{/if}
			{#if project.multiplexer === 'herdr' && project.state === 'stopped'}
				<!-- The stored choice is the next start's; a running machine may
				     still run tmux until it stops (I-502), so only a stopped one
				     shows it. -->
				<span aria-hidden="true">·</span>
				<span class="font-mono text-compact">herdr</span>
			{/if}
			{#if project.state === 'running'}
				<span aria-hidden="true">·</span>
				<span class="tabular-nums">up {uptime(project.started_at)}</span>
			{/if}
			{#if project.expires_at}
				<span aria-hidden="true">·</span>
				<span class="badge" data-testid="project-temporary">temporary</span>
				<span class="whitespace-nowrap tabular-nums">{tempLeft(project.expires_at)}</span>
			{/if}
		</p>

		{#if frozen}
			<!-- Frozen while idle (I-683, behind a flag): its memory is put
			     away and its processes held; a connection wakes it. -->
			<div class="frost-band mt-4" data-testid="frozen">
				<Snowflake size={18} />
				<p>
					<span class="font-medium">Frozen for {uptime(frozen)}.</span>
					<span class="text-ink-muted">It wakes when you connect.</span>
				</p>
			</div>
		{/if}

		{#if errorReason}
			<div class="banner banner--error mt-4" data-testid="last-error">{errorReason}</div>
		{/if}

		{#if project.host_unreachable}
			<div class="banner banner--warn mt-4" data-testid="host-unreachable">
				The server running this machine is not answering.
			</div>
		{/if}

		{#if abuseStopReason(project)}
			<div class="banner banner--warn mt-4" data-testid="abuse-stop">
				{abuseStopReason(project)}
			</div>
		{/if}

		{#if bootFallbackReason(project)}
			<div class="banner banner--warn mt-4" data-testid="boot-fallback">
				{bootFallbackReason(project)}
			</div>
		{/if}

		{#if startBanner === 'payment_required'}
			<div
				class="banner banner--warn mt-4"
				data-testid="refusal"
				data-reason={refusal?.reason ?? 'unknown'}
			>
				<p>
					{refusal
						? withoutBillingURL(refusal.message)
						: 'A plan is needed before a machine can start.'}
					<a href={resolve('/billing')} class="link">{refusalLink(refusal?.reason)}</a>
				</p>
				{#if refusal && refusal.projects.length > 0}
					<div class="mt-2 flex flex-wrap gap-2">
						{#each refusal.projects as named (named.id)}
							<button
								type="button"
								class="btn-quiet btn--sm"
								aria-disabled={opBusy ? 'true' : undefined}
								onclick={() => stopNamed(named)}>Stop {named.slug}</button
							>
						{/each}
					</div>
				{/if}
			</div>
		{:else if startBanner === 'capacity'}
			<div class="banner banner--warn mt-4">No capacity right now.</div>
		{/if}

		<!-- Questions run across the frame, and only while there are any:
		     their count changes, so they never sit in a column (I-680).
		     empty:hidden: with no question the card renders nothing, and the
		     empty cell would add a gap. One wrapper either way, so the card
		     keeps its closing line when the last question goes. -->
		<div class="mt-6 min-w-0 empty:hidden">
			<WaitingCard projectId={id} {pollFailures} bind:questions />
		</div>

		<!-- Each card is as tall as its content; the columns line up by what
		     they hold (I-680). From lg two columns: Events on the left,
		     Connect then Machine on the right (about 560 against 510px with
		     a full page of events). From md Connect and Machine pair over
		     Events. Below lg the right column's wrapper steps aside
		     (display: contents), and a phone orders the cards by what a
		     visit is for: Connect, Events, Machine. Each card's title is an
		     h2 at the one h2 size every dashboard page uses (text-xl). -->
		<div
			class="mt-6 grid grid-cols-1 items-start gap-6 md:grid-cols-2 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]"
		>
			<div
				class="order-2 min-w-0 md:order-3 md:col-span-2 lg:order-none lg:col-span-1 lg:self-stretch lg:*:h-full"
			>
				<EventsCard projectId={id} initial={firstEvents} {pollFailures} />
			</div>

			<!-- The two columns end on one line, so the gap under the row is one
			     gap: Machine takes what the column has left, its volume line
			     at its foot, and with few events the Events card does. -->
			<div class="contents lg:flex lg:min-w-0 lg:flex-col lg:gap-6 lg:self-stretch">
				<section class="card order-1 min-w-0 lg:order-none" aria-labelledby="connect-title">
					<h2 id="connect-title" class="text-xl font-semibold">Connect</h2>
					<div class="mt-4 space-y-2">
						{#if project.remote_url}
							<!-- A temporary project has no checkout to run in. -->
							<CodeCopy command="repose run" what="repose run, from the checkout" />
						{/if}
						<CodeCopy command="ssh {project.slug}.repose" what="the ssh command" />
						<CodeCopy command="repose code {project.slug}" what="the repose code command" />
					</div>
					{#if project.remote_url}
						<!-- wrap-anywhere, not break-all: the URL breaks at a hyphen or a
						     slash first, and mid-word only for a part wider than the card. -->
						<p class="mt-3 font-mono text-compact wrap-anywhere text-ink-muted">
							{normalizeRemoteDisplay(project.remote_url)}
						</p>
					{/if}
				</section>
				<div class="order-3 min-w-0 md:order-2 md:self-stretch md:*:h-full lg:order-none lg:flex-1">
					<MachineCard
						{project}
						frozen={!!frozen}
						memUsed={memNow}
						memTotal={samples?.memory_bytes}
						busy={!!opBusy}
						resizeOpen={showResize}
						onresizetoggle={toggleResize}
					/>
				</div>
			</div>

			{#if showResize}
				<!-- Under the row that holds Machine, across the frame; from md
				     the row under Connect and Machine, on a phone under Machine.
				     The select takes focus, and Cancel gives it back to
				     Resize…. From 40rem the sentence sits left and the form
				     right, as in Destroy; narrower, they stack (I-680). -->
				<section
					id="resize-panel"
					class="card @container order-3 min-w-0 md:order-2 md:col-span-2 lg:order-none"
					aria-labelledby="resize-title"
					bind:this={resizePanel}
				>
					<div
						class="flex flex-col gap-4 @min-[40rem]:flex-row @min-[40rem]:items-start @min-[40rem]:justify-between @min-[40rem]:gap-10"
					>
						<div class="min-w-0">
							<h2 id="resize-title" class="text-xl font-semibold">Resize the disk</h2>
							<p class="mt-1 text-sm text-ink-muted tabular-nums">
								It is {gb(project.volume_bytes)} now. Disks can't shrink.
							</p>
						</div>
						<form
							class="@container min-w-0 @min-[40rem]:w-[26rem] @min-[40rem]:shrink-0"
							onsubmit={(e) => {
								e.preventDefault();
								void submitResize();
							}}
						>
							<label for="resize-to" class="block text-sm text-ink-muted">Grow to</label>
							<div class="mt-1.5 flex flex-wrap items-center gap-2">
								<select
									id="resize-to"
									class="field min-w-0 max-w-[32rem] flex-[1_1_8rem] tabular-nums"
									bind:value={resizeTo}
									use:focusOnMount
								>
									{#each largerSizes as s (s)}
										<option value={s}>{s} GB</option>
									{/each}
								</select>
								<div class="flex shrink-0 items-center gap-2">
									<button
										type="submit"
										class="btn-quiet"
										disabled={!!opBusy || resizeTo === undefined}
									>
										{opBusy === 'resize' ? 'Resizing…' : 'Grow'}
									</button>
									<button type="button" class="btn-ghost" onclick={closeResize}>Cancel</button>
								</div>
							</div>
						</form>
					</div>
				</section>
			{/if}
		</div>

		<!-- Usage spans the frame between the two rows: the charts read best
		     wide (I-679). -->
		<section class="card mt-6 min-w-0" data-testid="usage-card" aria-labelledby="usage-title">
			<div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
				<h2 id="usage-title" class="text-xl font-semibold">Usage</h2>
				<!-- The current window is marked the way the config tabs mark
					     theirs: ink with a 1px underline, no accent. -->
				<div class="flex gap-4 text-sm" role="group" aria-label="Window">
					{#each WINDOWS as [w, name] (w)}
						<button
							type="button"
							aria-pressed={samplesWindow === w}
							class="cursor-pointer border-b {samplesWindow === w
								? 'border-current text-ink'
								: 'border-transparent text-ink-muted hover:text-ink'}"
							onclick={() => chooseWindow(w)}>{name}</button
						>
					{/each}
				</div>
			</div>
			{#if !samples}
				<p class="mt-3 text-sm text-ink-muted">
					{samplesFailed ? 'Could not load usage. It tries again in a minute.' : 'Loading…'}
				</p>
			{:else if samples.points.length === 0}
				<p class="mt-3 text-sm text-ink-muted">
					{project.state === 'running'
						? 'No usage reported in this window yet.'
						: 'No samples in this window: the machine was not running.'}
				</p>
			{:else}
				<div class="mt-5 grid grid-cols-1 gap-x-8 gap-y-6 md:grid-cols-2">
					<UsageChart
						label="CPU"
						points={series.cpu}
						max={1}
						stepS={samples.step_s}
						start={samplesAt - span}
						end={samplesAt}
						format={pct}
						maxLabel="100% of {samples.vcpus} vCPUs"
					/>
					<UsageChart
						label="Memory"
						points={series.mem}
						max={samples.memory_bytes}
						stepS={samples.step_s}
						start={samplesAt - span}
						end={samplesAt}
						format={bytes}
						maxLabel={gb(samples.memory_bytes)}
					/>
					<UsageChart
						label="Waiting for a vCPU"
						points={series.pressure}
						max={1}
						stepS={samples.step_s}
						start={samplesAt - span}
						end={samplesAt}
						format={pct}
						maxLabel="100%"
					/>
					<UsageChart
						label="Waiting for the server"
						points={series.wait}
						max={1}
						stepS={samples.step_s}
						start={samplesAt - span}
						end={samplesAt}
						format={pct}
						maxLabel="100%"
					/>
				</div>
				{#if samples.procs.length > 0}
					<!-- Folded by default: the charts answer most visits, and the
						     table is there when a chart's peak needs a name (I-674).
						     The summary is a caption, not a heading (I-375). -->
					<details class="procs" data-testid="busiest-processes">
						<summary>
							<svg class="procs-chev" viewBox="0 0 16 16" aria-hidden="true"
								><path d="M6 4l4 4-4 4" /></svg
							>
							Busiest processes
							<span class="procs-n">{samples.procs.length}</span>
						</summary>
						<table class="table mt-2 w-full text-sm">
							<thead>
								<tr>
									<th scope="col" class="text-left">Process</th>
									<th scope="col" class="text-right">CPU time</th>
									<th scope="col" class="text-right">Peak memory</th>
								</tr>
							</thead>
							<tbody>
								{#each samples.procs as p (p.comm)}
									<tr>
										<td class="font-mono text-compact">{p.comm}</td>
										<td class="text-right tabular-nums">{cpuTime(p.cpu_s)}</td>
										<td class="text-right tabular-nums">{bytes(p.rss_max_bytes)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</details>
				{/if}
			{/if}
			{#if egressGb !== undefined}
				<p
					class="egress-row mt-6 flex items-baseline justify-between gap-4 border-t pt-4 text-sm border-rule"
					data-testid="egress-period"
				>
					<span class="font-medium">Egress this period</span>
					<span class="text-xs text-ink-muted tabular-nums">{gbs(egressGb)}</span>
				</p>
			{/if}
		</section>

		<!-- Snapshots take the wide column, since a row opens its restore
		     form under it and a form needs room (I-680); Agents, while the
		     machine runs, then Details on the right. With no snapshot yet
		     the list has no form to open, and a running machine's three
		     short cards share one row of three. From md the cards pair,
		     and one left without a partner spans both columns. -->
		{@const running = project.state === 'running'}
		{@const anySnapshot = snapshots.length > 0}
		<div
			class="mt-6 grid grid-cols-1 items-start gap-6 md:grid-cols-2 {running && !anySnapshot
				? 'lg:grid-cols-3'
				: 'lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]'}"
		>
			<div class="min-w-0 {anySnapshot ? 'md:col-span-2 lg:col-span-1' : ''}">
				<SnapshotsTable
					{project}
					{snapshots}
					busy={!!opBusy}
					creating={opBusy === 'snapshot'}
					restoring={opBusy === 'restore'}
					oncreate={onCreateSnapshot}
					onrestore={onRestore}
				/>
			</div>
			<div
				class={running && !anySnapshot
					? 'contents'
					: 'contents lg:flex lg:min-w-0 lg:flex-col lg:gap-6'}
			>
				{#if running}
					<div class="min-w-0">
						<AgentsCard signals={project.signals} {questions} updated={lastUpdated} />
					</div>
				{/if}
				<div class="min-w-0 {running !== anySnapshot ? 'md:col-span-2 lg:col-span-1' : ''}">
					<DetailsList {project} {revision} onkeep={onKeep} />
				</div>
			</div>
		</div>

		<section
			class="danger mt-10 rounded-sm border p-5"
			aria-labelledby="destroy-title"
			data-testid="destroy-card"
		>
			<div class="flex flex-col gap-4 md:flex-row md:items-start md:justify-between md:gap-10">
				<div>
					<h2 id="destroy-title" class="text-xl font-semibold text-red-700 dark:text-red-400">
						Destroy
					</h2>
					<p class="mt-1 text-sm text-ink-muted">
						{temporary
							? 'Deletes the disk. It is temporary: destroying it keeps no snapshot and it cannot be restored.'
							: 'Deletes the disk. The last snapshot is kept 30 days.'}
					</p>
				</div>
				<div class="md:w-[26rem] md:shrink-0">
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
		</section>
	</PageShell>
{/if}

<style>
	.procs {
		margin-top: 1.75rem;
		border-top: 1px solid var(--rule);
	}
	.procs summary {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		cursor: pointer;
		list-style: none;
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.procs summary::-webkit-details-marker {
		display: none;
	}
	.procs summary:hover {
		color: var(--ink);
	}
	.procs-chev {
		width: 14px;
		height: 14px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.5;
		color: var(--ink-muted);
		transition: transform 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.procs[open] .procs-chev {
		transform: rotate(90deg);
	}
	/* Rows keep their hairlines; the last one drops its own, since
	   Egress's rule follows right under it. */
	.procs td {
		padding-block: 0.5rem;
	}
	.procs tr:last-child td {
		border-bottom: 0;
	}
	.procs table {
		margin-bottom: 0.5rem;
	}
	.procs + .egress-row {
		margin-top: 0;
	}
	.procs-n {
		font-weight: 400;
		font-variant-numeric: tabular-nums;
		color: var(--ink-muted);
	}
	@media (prefers-reduced-motion: reduce) {
		.procs-chev {
			transition: none;
		}
	}
	.danger {
		border-color: color-mix(in srgb, var(--color-red-600) 35%, var(--rule));
	}
	/* The frozen band (I-683): ice, not a warning; nothing is wrong. */
	.frost-band {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding: 0.75rem 1rem;
		border: 1px solid color-mix(in srgb, var(--frost) 45%, var(--rule));
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--frost) 7%, var(--surface));
		color: var(--frost);
		font-size: var(--text-sm);
	}
	.frost-band p {
		color: var(--ink);
	}
</style>
