<script lang="ts">
	import { onMount } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
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
		patchProject,
		listDestroyed,
		restoreProject
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible, pollUntilDone } from '$lib/poll';
	import { bytes, dateOnly, dateTime, gb } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
	import ProjectMeta from '$lib/components/project/ProjectMeta.svelte';
	import CommandList from '$lib/hk/CommandList.svelte';
	import ErrorView from '$lib/components/ErrorView.svelte';
	import RestoreNameForm from '$lib/components/RestoreNameForm.svelte';
	import { splitTicks } from '$lib/components/project/ticks';
	import { defaultRestoreName } from '$lib/destroyed';
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
		DestroyedProject,
		PaymentRequiredReason,
		Project,
		Question,
		Snapshot,
		Revision,
		Samples,
		SampleWindow
	} from '$lib/api/types';

	const id = page.params.id as string;
	/** Below 640px the header's action sits under the status line. */
	const phone = new MediaQuery('max-width: 639.98px');

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

	/** Ticks of the poll since the api first said the project is gone. */
	let goneTicks = 0;

	async function refresh() {
		// Gone: one cheap ask a minute (every sixth 10 s tick), in case it
		// comes back, and not the six the full load makes.
		if (notFound && ++goneTicks % 6 !== 0) return;
		try {
			if (!project && notFound) await getProject(id);
			if (!project) await firstLoad();
			else project = await getProject(id);
			lastUpdated = new Date();
			notFound = false;
			loadFailed = false;
			projectFailure.ok();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'not_found') {
				if (!notFound) void findDestroyed();
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

	/**
	 * A project that is gone may be one the user destroyed: its row in
	 * "Recently destroyed" says when, and it can be restored from here.
	 */
	let gone = $state<DestroyedProject | undefined>(undefined);
	let goneName = $state('');
	let goneError = $state<string | undefined>(undefined);
	let goneBusy = $state(false);

	async function findDestroyed() {
		try {
			const list = await listDestroyed();
			gone = list.find((d) => d.id === id);
			if (gone) {
				const live = await listProjects().catch(() => []);
				goneName = defaultRestoreName(
					gone,
					live.map((p) => p.slug)
				);
			}
		} catch {
			// The 404 stands on its own.
		}
	}

	async function restoreGone(name: string) {
		if (!gone) return;
		goneBusy = true;
		goneError = undefined;
		try {
			const res = await restoreProject({ project_id: gone.id, name });
			toast.success(
				`Restoring ${res.name} from its snapshot of ${dateTime(res.snapshot_created_at)}.`
			);
			await goto(resolve('/projects/[id]', { id: res.project_id }));
		} catch (err) {
			if (
				err instanceof ApiError &&
				err.code === 'conflict' &&
				err.detail?.reason === 'name_taken'
			) {
				goneError = `A project called ${String(err.detail?.name ?? name)} already exists; pick another name.`;
			} else if (err instanceof ApiError && err.code === 'invalid') {
				goneError = err.message;
			} else {
				toastApiError(err, 'Could not restore the project.');
			}
		} finally {
			goneBusy = false;
		}
	}

	async function refreshSnapshots() {
		if (notFound) return;
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
		if (notFound) return;
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

	/** Destroy asks first: its button opens the typed confirm in its place. */
	let askingDestroy = $state(false);
	function keepIt() {
		askingDestroy = false;
		void focusAfterRender('destroy-open');
	}

	/** The button a step under way shows in the header, waiting. */
	const WAITING: Partial<Record<Project['state'], string>> = {
		creating: 'Creating…',
		building: 'Building…',
		starting: 'Starting…',
		stopping: 'Stopping…',
		restoring: 'Restoring…',
		destroying: 'Destroying…'
	};

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

	/** The sentence after "code: " in last_error. The api writes it lowercase
	 * ("stopping <slug> failed: ..."); the banner opens a sentence, so its
	 * first letter is raised. The list's sub-line keeps it as stored. */
	let errorReason = $derived.by(() => {
		if (!project || project.state !== 'error' || !project.last_error) return '';
		const i = project.last_error.indexOf(': ');
		const r = i > 0 ? project.last_error.slice(i + 2) : project.last_error;
		return r.charAt(0).toUpperCase() + r.slice(1);
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
	<title
		>{project
			? `${project.name} · repose`
			: notFound
				? 'Not found · repose'
				: 'Project · repose'}</title
	>
</svelte:head>

{#snippet ticks(text: string)}{#each splitTicks(text) as part, i (i)}{#if part.code}<code
				>{part.text}</code
			>{:else}{part.text}{/if}{/each}{/snippet}

<!-- The header's action: one row at the title's baseline from 640px; on a
     phone, the first row under the tabs, in the same place whatever the
     state. Under the tabs, so the head and the strip sit where Config's and
     Secrets' do (review r3 sys3-1). -->
{#snippet headActions()}
	<div class="acts">
		{#if project?.state === 'stopped'}
			<button
				type="button"
				class="hk-btn"
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
				class="hk-btn"
				aria-disabled={opBusy ? 'true' : undefined}
				onclick={onStart}
			>
				{opBusy === 'start' ? 'Restarting…' : 'Restart'}
			</button>
		{:else if project?.state === 'running'}
			<label class="hk-check snap-check">
				<input type="checkbox" bind:checked={stopSnapshotFirst} />
				Snapshot on stop
			</label>
			<button
				type="button"
				class="hk-btn-secondary"
				aria-disabled={opBusy ? 'true' : undefined}
				onclick={onStopPressed}
			>
				{opBusy === 'stop' ? 'Stopping…' : 'Stop'}
			</button>
		{:else if project && WAITING[project.state]}
			<!-- A step under way: the button that will come back, waiting. -->
			<button type="button" class="hk-btn" aria-disabled="true">{WAITING[project.state]}</button>
		{/if}
	</div>
{/snippet}

<!-- The remote, the state and the size under the title, then the tabs on
     the title's rule: the same head on Config and Secrets (ProjectMeta). -->
{#snippet tabs()}
	{#if project}<ProjectMeta {project} />{/if}
	<ProjectTabs {id} />
{/snippet}

{#if notFound}
	<ErrorView status={404} title="Not found">
		{#if gone}
			<p class="gone">Destroyed {dateTime(gone.destroyed_at)}.</p>
			<div class="gone-form">
				<RestoreNameForm
					id={gone.id}
					bind:value={goneName}
					error={goneError}
					busy={goneBusy}
					autofocus={false}
					onsubmit={(n) => void restoreGone(n)}
				/>
			</div>
		{/if}
	</ErrorView>
{:else if !project}
	<!-- The h1 stays a name while the project loads: "Loading…" as the
	     page's heading is what a screen reader announced as its title. -->
	<PageShell title="Project" crumbs={[{ label: 'Projects', href: resolve('/projects') }]} {tabs}>
		<!-- A screen's height while it loads, so the footer is not in view
		     to be pushed off it when the sections arrive (layout shift). -->
		<div class="min-h-screen">
			<LoadState status={loadFailed ? 'failed' : 'loading'} onretry={refresh} error={loadError} />
		</div>
	</PageShell>
{:else}
	{@const running = project.state === 'running'}
	{@const agents = running && (project.signals?.agents.length ?? 0) > 0}
	{@const hasNotes =
		askingStop ||
		!!errorReason ||
		!!startBanner ||
		!!project.host_unreachable ||
		!!abuseStopReason(project) ||
		!!bootFallbackReason(project)}
	<PageShell
		title={project.name}
		crumbs={[{ label: 'Projects', href: resolve('/projects') }]}
		{tabs}
		action={phone.current ? undefined : headActions}
	>
		{#if phone.current}<div class="phone-acts">{@render headActions()}</div>{/if}
		{#if hasNotes}
			<div class="notes">
				{#if askingStop}
					<!-- The CLI's words, and focus on the safe choice (I-614). -->
					<div class="hk-confirm" data-testid="confirm-stop">
						<p>{stopEnds}</p>
						<div class="hk-confirm-actions">
							<button type="button" class="hk-btn-danger hk-btn-sm" onclick={onStop}
								>Stop anyway</button
							>
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								use:focusOnMount
								onclick={() => (askingStop = false)}>Keep it running</button
							>
						</div>
					</div>
				{/if}

				<!-- One banner per exception, each changing what Connect will do. -->
				{#if errorReason}
					<div class="hk-banner hk-banner--err" role="alert" data-testid="last-error">
						<p>{@render ticks(errorReason)}</p>
					</div>
				{/if}

				{#if startBanner === 'payment_required'}
					<div
						class="hk-banner hk-banner--err"
						role="alert"
						data-testid="refusal"
						data-reason={refusal?.reason ?? 'unknown'}
					>
						<p>
							{refusal
								? withoutBillingURL(refusal.message)
								: 'A plan is needed before a machine can start.'}
							<a href={resolve('/billing')}>{refusalLink(refusal?.reason)}</a>
						</p>
						{#if refusal && refusal.projects.length > 0}
							<div class="refusal-stops">
								{#each refusal.projects as named (named.id)}
									<button
										type="button"
										class="hk-btn-secondary hk-btn-sm"
										aria-disabled={opBusy ? 'true' : undefined}
										onclick={() => stopNamed(named)}>Stop {named.slug}</button
									>
								{/each}
							</div>
						{/if}
					</div>
				{:else if startBanner === 'capacity'}
					<div class="hk-banner" role="status">
						<p>No capacity right now.</p>
					</div>
				{/if}

				{#if project.host_unreachable}
					<div class="hk-banner" role="status" data-testid="host-unreachable">
						<p>The server running this machine is not answering.</p>
					</div>
				{/if}

				{#if abuseStopReason(project)}
					<div class="hk-banner" role="status" data-testid="abuse-stop">
						<p>{abuseStopReason(project)}</p>
					</div>
				{/if}

				{#if bootFallbackReason(project)}
					<div class="hk-banner" role="status" data-testid="boot-fallback">
						<p>{bootFallbackReason(project)}</p>
					</div>
				{/if}
			</div>
		{/if}

		<section class="hk-section connect" aria-labelledby="connect-title">
			<div class="hk-section-head">
				<h2 id="connect-title" class="hk-h2-app">Connect</h2>
			</div>
			<CommandList
				label="Connect"
				commands={[
					// A temporary project has no checkout to run in.
					...(project.remote_url
						? [
								{
									command: 'repose run',
									what: 'repose run',
									note: 'in the checkout on your laptop'
								}
							]
						: []),
					// A plain ssh never starts a stopped machine; repose ssh does
					// (ssh-and-editors.md, review r1 dash-r1-5).
					project.state === 'running'
						? { command: `ssh ${project.slug}.repose`, what: 'the ssh command' }
						: { command: `repose ssh ${project.slug}`, what: 'the repose ssh command' },
					{ command: `repose code ${project.slug}`, what: 'the repose code command' }
				]}
			/>
		</section>

		<WaitingCard projectId={id} {pollFailures} bind:questions />

		<!-- Each row: a wide list on the left, short facts in a 21rem column
		     on the right, from 1024px (I-837). The DOM order is the phone's. -->
		<div class="pgrid">
			<div class="pcol">
				<EventsCard projectId={id} initial={firstEvents} {pollFailures} />
			</div>
			<div class="pcol pcol--side">
				{#if agents}
					<AgentsCard signals={project.signals} {questions} updated={lastUpdated} />
				{/if}
				<MachineCard
					{project}
					memUsed={memNow}
					memTotal={samples?.memory_bytes}
					{egressGb}
					busy={!!opBusy}
					resizeOpen={showResize}
					onresizetoggle={toggleResize}
				/>
			</div>
		</div>

		{#if showResize}
			<!-- Under the row that holds Machine, across the frame. The select
			     takes focus, and Cancel gives it back to Resize…. -->
			<section
				id="resize-panel"
				class="resize-panel"
				aria-labelledby="resize-title"
				bind:this={resizePanel}
			>
				<h2 id="resize-title" class="hk-sr">Resize the disk</h2>
				<form
					class="resize-form"
					onsubmit={(e) => {
						e.preventDefault();
						void submitResize();
					}}
				>
					<label for="resize-to" class="hk-field-label resize-label">Grow to</label>
					<select
						id="resize-to"
						class="hk-select resize-select"
						bind:value={resizeTo}
						use:focusOnMount
					>
						{#each largerSizes as s (s)}
							<option value={s}>{s} GB</option>
						{/each}
					</select>
					<button type="submit" class="hk-btn" disabled={!!opBusy || resizeTo === undefined}>
						{opBusy === 'resize' ? 'Resizing…' : 'Grow'}
					</button>
					<button type="button" class="hk-btn-ghost" onclick={closeResize}>Cancel</button>
					<p class="resize-note">
						{DISK_SIZES_GB[DISK_SIZES_GB.length - 1]} GB is the largest size.
					</p>
				</form>
			</section>
		{/if}

		<div class="pgrid">
			<div class="pcol">
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
			<div class="pcol pcol--side">
				<DetailsList {project} {revision} onkeep={onKeep} />
			</div>
		</div>

		<section class="hk-section" data-testid="usage-card" aria-labelledby="usage-title">
			<div class="hk-section-head">
				<h2 id="usage-title" class="hk-h2-app">Usage</h2>
				<div class="hk-seg--buttons hk-seg--sm" role="group" aria-label="Window">
					{#each WINDOWS as [w, name] (w)}
						<button type="button" aria-pressed={samplesWindow === w} onclick={() => chooseWindow(w)}
							>{name}</button
						>
					{/each}
				</div>
			</div>
			{#if !samples}
				<div class="hk-empty-row">
					<span
						>{samplesFailed
							? 'Could not load usage. It tries again in a minute.'
							: 'Loading…'}</span
					>
				</div>
			{:else if samples.points.length === 0}
				<div class="hk-empty-row">
					<span
						>{project.state === 'running'
							? 'No usage reported in this window yet.'
							: 'No samples in this window: the machine was not running.'}</span
					>
				</div>
			{:else}
				<div class="charts">
					<UsageChart
						label="CPU"
						points={series.cpu}
						max={1}
						stepS={samples.step_s}
						start={samplesAt - span}
						end={samplesAt}
						format={pct}
						maxLabel="100% of {samples.vcpus} {samples.vcpus === 1 ? 'core' : 'cores'}"
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
						label="Waiting for a core"
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
					<!-- Folded: the charts answer most visits, and the table is
					     there when a chart's peak needs a name (I-674). -->
					<details class="procs" data-testid="busiest-processes">
						<summary>
							<span class="procs-chev" aria-hidden="true"></span>
							Busiest processes
							<span class="procs-n">{samples.procs.length}</span>
						</summary>
						<table class="hk-table procs-table">
							<thead>
								<tr>
									<th scope="col">Process</th>
									<th scope="col" class="hk-num-col">CPU time</th>
									<th scope="col" class="hk-num-col">Peak memory</th>
								</tr>
							</thead>
							<tbody>
								{#each samples.procs as p (p.comm)}
									<tr>
										<td class="hk-mono">{p.comm}</td>
										<td class="hk-num-col">{cpuTime(p.cpu_s)}</td>
										<td class="hk-num-col">{bytes(p.rss_max_bytes)}</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</details>
				{/if}
			{/if}
		</section>

		<section class="hk-section destroy" aria-labelledby="destroy-title" data-testid="destroy-card">
			<div class="destroy-row">
				<div class="destroy-say">
					<h2 id="destroy-title" class="hk-h2-app">Destroy</h2>
					<p>
						{temporary
							? 'Deletes the disk. It is temporary: destroying it keeps no snapshot and it cannot be restored.'
							: 'Deletes the disk. The last snapshot is kept 30 days.'}
					</p>
				</div>
				<div class="destroy-act">
					{#if askingDestroy}
						<ConfirmType
							word={project.slug}
							label="Destroy"
							busyLabel="Destroying…"
							busy={opBusy === 'destroy'}
							disabled={!!opBusy}
							onconfirm={onDestroy}
							oncancel={keepIt}
							cancelLabel="Keep it"
							autofocus
						/>
					{:else}
						<button
							type="button"
							id="destroy-open"
							class="hk-btn-danger hk-btn-sm"
							aria-disabled={opBusy ? 'true' : undefined}
							onclick={() => !opBusy && (askingDestroy = true)}>Destroy {project.slug}…</button
						>
					{/if}
				</div>
			</div>
		</section>
	</PageShell>
{/if}

<style>
	/* ---- Head ---- */
	/* One height for every state's actions (a check and Stop, or a lone
	   Start), so the status line and the tabs never move with the state. */
	.acts {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.5rem;
		min-height: 2.25rem;
	}
	.phone-acts {
		margin-bottom: 1.75rem;
	}
	.snap-check {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		margin-right: 0.5rem;
	}
	.notes {
		display: grid;
		gap: 0.75rem;
		margin-bottom: 1.75rem;
	}
	.notes :global(.hk-banner) {
		margin: 0;
	}
	.refusal-stops {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.connect {
		margin-top: 0;
	}

	/* ---- Rows ---- */
	.pgrid {
		display: grid;
		margin-top: 2.75rem;
		row-gap: 2.75rem;
	}
	@media (min-width: 1024px) {
		.pgrid {
			grid-template-columns: minmax(0, 1fr) 21rem;
			column-gap: 3rem;
			align-items: start;
		}
	}
	.pcol {
		min-width: 0;
	}
	.pcol > :global(.hk-section:first-child) {
		margin-top: 0;
	}

	/* ---- Resize ---- */
	.resize-panel {
		margin-top: 1.75rem;
		padding: 0.875rem 1rem;
		border-radius: var(--hk-radius);
		background: var(--hk-surface-2);
	}
	.resize-form {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 0.75rem;
	}
	.resize-label {
		margin: 0;
	}
	.resize-select {
		width: auto;
		min-width: 8rem;
		font-variant-numeric: tabular-nums;
	}
	.resize-note {
		flex-basis: 100%;
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}

	/* ---- Usage ---- */
	/* The plots line up at their foot whatever their captions wrap to. */
	.charts {
		display: grid;
		align-items: end;
		gap: 1.75rem 2rem;
	}
	@media (min-width: 768px) {
		.charts {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	@media (min-width: 1024px) {
		.charts {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
	}
	.procs {
		margin-top: 1.5rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.procs summary {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		cursor: pointer;
		list-style: none;
		font-size: 0.875rem;
		font-weight: 500;
	}
	.procs summary::-webkit-details-marker {
		display: none;
	}
	/* The CSS chevron of Guide B 12.17. */
	.procs-chev {
		width: 6px;
		height: 6px;
		margin: 0 0.25rem 0 0.125rem;
		border-right: 1.5px solid var(--hk-text-3);
		border-bottom: 1.5px solid var(--hk-text-3);
		transform: rotate(-45deg);
		transition: transform var(--hk-dur-fast) var(--hk-ease);
	}
	.procs[open] .procs-chev {
		transform: rotate(45deg);
	}
	.procs-n {
		font-weight: 400;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.procs-table {
		margin-bottom: 0.75rem;
	}
	@media (prefers-reduced-motion: reduce) {
		.procs-chev {
			transition: none;
		}
	}

	/* ---- Destroy ---- */
	.destroy {
		margin-top: 3.5rem;
		padding-top: 1.75rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.destroy-row {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}
	.destroy-say p {
		margin: 0.375rem 0 0;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
	}
	@media (min-width: 1024px) {
		.destroy-row {
			flex-direction: row;
			align-items: flex-start;
			justify-content: space-between;
			gap: 3rem;
		}
		.destroy-act {
			flex: none;
			display: flex;
			justify-content: flex-end;
			width: 34rem;
			padding-top: 0.125rem;
		}
		/* ConfirmType lays out by its own width (a size container), so it
		   needs one: the field, Destroy and Keep it on one row, flush right. */
		.destroy-act > :global(form) {
			flex: none;
			width: var(--confirm-w, 31rem);
		}
	}

	/* ---- Not found ---- */
	.gone {
		margin: 0;
	}
	.gone-form {
		margin-top: 1rem;
		max-width: 40rem;
	}
</style>
