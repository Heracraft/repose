<script lang="ts" module>
	import type { Revision as Rev, Samples as Smp } from '$lib/api/types';
	/**
	 * What a switch back from another tab can reuse, per project: the
	 * memory sample (a minute old at most, as the poll asks), the config
	 * revisions (read again only when the project names a new one) and the
	 * egress reading (a minute). Questions and the newest events are read
	 * on every mount.
	 */
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- a plain cache, read once per mount
	const kept = new Map<
		string,
		{
			samples?: Smp;
			samplesAt: number;
			revisions?: Rev[];
			egress?: number;
			egressAt: number;
			egressFrom?: string;
		}
	>();
	const keep = (id: string) => {
		let k = kept.get(id);
		if (!k) kept.set(id, (k = { samplesAt: 0, egressAt: 0 }));
		return k;
	};
</script>

<script lang="ts">
	// A project's Overview (DECISIONS I-841): what needs you, how to get
	// in, what runs now, then the facts and Destroy. The layout above owns
	// the project, its poll, the header and Start/Stop; this tab reads its
	// questions, the newest 5 events, the memory sample (Machine's "in
	// use"; the project carries no memory figure) and the config revision
	// once, together, so nothing lands above what is on screen.
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		destroyProject,
		resizeProject,
		listRevisions,
		getSamples,
		listProjectQuestions,
		getUsage,
		patchProject,
		listEventsPage
	} from '$lib/api/client';
	import { ApiError, errorText } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { dateOnly } from '$lib/format';
	import { billingStore } from '$lib/billing.svelte';
	import CommandList from '$lib/hk/CommandList.svelte';
	import LoadState from '$lib/components/LoadState.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import WaitingCard from '$lib/components/questions/WaitingCard.svelte';
	import MachineCard, { DISK_SIZES_GB } from '$lib/components/project/MachineCard.svelte';
	import AgentsCard from '$lib/components/project/AgentsCard.svelte';
	import DetailsList from '$lib/components/project/DetailsList.svelte';
	import LatestEvents, { LATEST_EVENTS } from '$lib/components/project/LatestEvents.svelte';
	import { useProject } from '$lib/components/project/context.svelte';
	import { destroyRefusalMessage } from '$lib/components/project/host';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { latest, type Pt } from '$lib/usage';
	import type { ProjectEvent, Question, Revision, Samples } from '$lib/api/types';

	const ctx = useProject();
	const id = ctx.id;
	let project = $derived(ctx.project);

	let ready = $state(false);
	let questions = $state<Question[]>([]);
	let events = $state<ProjectEvent[] | undefined>(undefined);
	const cache = keep(id);
	let revisions = $state<Revision[]>(cache.revisions ?? []);
	let samples = $state<Samples | undefined>(cache.samples);
	let samplesAt = cache.samplesAt;

	/**
	 * Everything the tab paints, at once: each part falls back to its own
	 * empty state, so only the project (the layout's) can stop the page.
	 */
	async function firstLoad() {
		const freshSamples = Date.now() - samplesAt < 55_000;
		const want = ctx.project?.config_revision_id;
		const haveRevision =
			!!cache.revisions && (!want || revisions.some((r) => r.revision_id === want));
		const [qs, ev, smp, revs] = await Promise.all([
			listProjectQuestions(id).catch(() => [] as Question[]),
			listEventsPage(id, { limit: LATEST_EVENTS })
				.then((r) => r.events)
				.catch(() => undefined),
			freshSamples ? undefined : getSamples(id, '1h').catch(() => undefined),
			haveRevision ? revisions : listRevisions(id).catch(() => [] as Revision[])
		]);
		questions = qs;
		events = ev;
		revisions = revs;
		cache.revisions = revs;
		if (smp) {
			samples = smp;
			samplesAt = Date.now();
			cache.samples = smp;
			cache.samplesAt = samplesAt;
		}
		ready = true;
	}

	/** The memory sample changes once a minute; the poll asks at most that often. */
	async function refreshSamples() {
		if (Date.now() - samplesAt < 55_000) return;
		try {
			samples = await getSamples(id, '1h');
			samplesAt = Date.now();
			cache.samples = samples;
			cache.samplesAt = samplesAt;
		} catch {
			// Machine reads "not running" until a sample arrives.
		}
	}

	onMount(() => {
		void firstLoad();
		let first = true;
		return pollWhileVisible(() => {
			if (first) {
				first = false;
				return;
			}
			return refreshSamples();
		});
	});

	// A new config revision (an Apply on Config, the CLI) is read when the
	// project names one this tab has not seen.
	let revision = $derived(revisions.find((r) => r.revision_id === project?.config_revision_id));
	$effect(() => {
		const want = project?.config_revision_id;
		if (!ready || !want || revisions.some((r) => r.revision_id === want)) return;
		void listRevisions(id)
			.then((r) => (revisions = cache.revisions = r))
			.catch(() => {});
	});

	/** The memory in use now: the newest sample's, while the machine runs. */
	let memNow = $derived.by(() => {
		if (project?.state !== 'running' || !samples) return undefined;
		const pts: Pt[] = samples.points.map((p) => ({ t: Date.parse(p.ts), v: p.mem_used_bytes }));
		return latest(pts);
	});

	/**
	 * This project's egress since the billing period started (GET /usage,
	 * filtered to it), from the period the shared billing store holds.
	 * Billing off, or no subscription, has no period, and no reading.
	 */
	let egressGb = $state<number | undefined>(cache.egress);
	let periodStart = $derived(billingStore.info?.subscription?.period_start);
	$effect(() => {
		const from = periodStart;
		if (!from) return;
		if (cache.egressFrom === from && Date.now() - cache.egressAt < 60_000) return;
		// GET /usage takes days; to is the end of today, so today counts.
		const tomorrow = new Date(Date.now() + 86_400_000).toISOString();
		void getUsage(dateOnly(from), dateOnly(tomorrow))
			.then((days) => {
				egressGb = days.filter((d) => d.project_id === id).reduce((n, d) => n + d.egress_gb, 0);
				Object.assign(cache, { egress: egressGb, egressAt: Date.now(), egressFrom: from });
			})
			.catch(() => {});
	});

	/** Whether this machine is on the temporary timer (I-347). */
	let temporary = $derived(!!project?.expires_at);

	/** Keeps a temporary machine, as `repose keep` does: PATCH with expires_at null. */
	async function onKeep() {
		if (!project) return;
		try {
			const kept = await patchProject(project.id, { expires_at: null });
			ctx.set(kept);
			toast.success(`${kept.slug} is no longer temporary.`);
		} catch (err) {
			toastApiError(err, 'Could not keep the machine.');
		}
	}

	// Resize. Machine's Resize… opens a panel under the row that holds it,
	// across the column (I-680). A volume only grows, so the choices are the
	// sizes strictly larger than the disk now, and the select opens on the
	// smallest of them.
	const GIB = 1024 * 1024 * 1024;
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

	/** Closes the panel; focus goes back to Resize… only if it was inside the panel. */
	function closeResize() {
		const hadFocus = !!resizePanel?.contains(document.activeElement);
		showResize = false;
		if (hadFocus) void focusAfterRender('resize-open', 'resize-largest');
	}

	async function submitResize() {
		// The select only offers grows, but the api must never be asked to
		// shrink a disk from a button labelled Grow, whatever the select holds.
		if (!project || resizeTo === undefined || resizeTo * GIB <= project.volume_bytes) return;
		ctx.opBusy = 'resize';
		try {
			const { op_id } = await resizeProject(id, resizeTo * GIB);
			let grown = false;
			await ctx.waitForOp(op_id, () => {
				grown = true;
			});
			if (grown) await ctx.refresh();
			ctx.opBusy = undefined;
			if (grown) closeResize();
		} catch (err) {
			ctx.opBusy = undefined;
			// disk_limit is a plan's refusal, told like a start's (api.md).
			if (err instanceof ApiError && err.code === 'payment_required') {
				await ctx.refuse(err);
				closeResize();
				return;
			}
			toastApiError(err, 'Could not resize the disk.');
		}
	}

	/** Destroy asks first: its button opens the typed confirm in its place. */
	let askingDestroy = $state(false);
	/** Inline refusal shown in the Destroy panel until dismissed or retried. */
	let destroyError = $state<string | undefined>(undefined);
	function keepIt() {
		askingDestroy = false;
		destroyError = undefined;
		void focusAfterRender('destroy-open');
	}

	async function onDestroy() {
		if (ctx.opBusy || !project) return;
		ctx.opBusy = 'destroy';
		destroyError = undefined;
		try {
			await destroyProject(id);
			// The destroy runs on after this returns (I-166). A temporary
			// machine's destroy takes no snapshot and never reaches "Recently
			// destroyed" (I-347), so the sentence that promises a restore is
			// false for it.
			toast.success(
				temporary
					? `Destroying ${project.name}. It was temporary, so there is no snapshot to restore.`
					: `Destroying ${project.name}. It can be restored from "Recently destroyed" for 30 days.`
			);
			await goto(resolve('/projects'));
		} catch (err) {
			ctx.opBusy = undefined;
			const conflict = destroyRefusalMessage(err, project.state);
			if (conflict) {
				destroyError = conflict;
				void ctx.refresh();
				return;
			}
			if (err instanceof ApiError && err.code !== 'internal') {
				destroyError = errorText(err, 'Could not destroy the project.');
				return;
			}
			toastApiError(err, 'Could not destroy the project.');
		}
	}
</script>

{#if !project || !ready}
	<!-- A screen's height while it loads, so the footer is not in view to
	     be pushed off it when the sections arrive (layout shift). -->
	<div class="min-h-screen">
		<LoadState status="loading" onretry={firstLoad} />
	</div>
{:else}
	{@const agents = project.state === 'running' && (project.signals?.agents.length ?? 0) > 0}

	<!-- What waits on you first: an agent is stopped until it is answered. -->
	<WaitingCard projectId={id} pollFailures={ctx.pollFailures} bind:questions />

	<section class="hk-section" aria-labelledby="connect-title">
		<div class="hk-section-head">
			<h2 id="connect-title" class="hk-h2-app">Connect</h2>
		</div>
		<CommandList
			label="Connect"
			commands={[
				// One command for every machine, temporary or not, from any
				// folder: attach, which starts a stopped one. run is for
				// making a machine, not reaching one (DECISIONS I-1350).
				{ command: `repose attach ${project.slug}`, what: 'the repose attach command' },
				// A plain ssh never starts a stopped machine; repose ssh does
				// (ssh-and-editors.md, review r1 dash-r1-5).
				project.state === 'running'
					? { command: `ssh ${project.slug}.repose`, what: 'the ssh command' }
					: { command: `repose ssh ${project.slug}`, what: 'the repose ssh command' },
				{ command: `repose code ${project.slug}`, what: 'the repose code command' }
			]}
		/>
	</section>

	<!-- From a 760px column, the events on the left and what runs now on
	     the right. The DOM order is the phone's: state before history. -->
	<div class="ogrid">
		<div class="oside">
			{#if agents}
				<AgentsCard
					projectId={project.id}
					signals={project.signals}
					{questions}
					updated={ctx.lastUpdated}
				/>
			{/if}
			<MachineCard
				{project}
				memUsed={memNow}
				memTotal={samples?.memory_bytes}
				{egressGb}
				busy={!!ctx.opBusy}
				resizeOpen={showResize}
				onresizetoggle={toggleResize}
			/>
		</div>

		{#if showResize}
			<!-- Under the row that holds Machine, across the column. The select
			     takes focus, and Cancel gives it back to Resize…. -->
			<section
				id="resize-panel"
				class="oresize"
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
					<button
						type="submit"
						class="hk-btn-secondary"
						disabled={!!ctx.opBusy || resizeTo === undefined}
					>
						{ctx.opBusy === 'resize' ? 'Resizing…' : 'Grow'}
					</button>
					<button type="button" class="hk-btn-ghost" onclick={closeResize}>Cancel</button>
					<p class="resize-note">
						{DISK_SIZES_GB[DISK_SIZES_GB.length - 1]} GB is the largest size.
					</p>
				</form>
			</section>
		{/if}

		<div class="oevents">
			<LatestEvents projectId={id} bind:events pollFailures={ctx.pollFailures} />
		</div>
	</div>

	<DetailsList {project} {revision} onkeep={onKeep} />

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
			<div class="destroy-act" class:destroy-act--open={askingDestroy}>
				{#if askingDestroy}
					<ConfirmType
						word={project.slug}
						label="Destroy"
						busyLabel="Destroying…"
						busy={ctx.opBusy === 'destroy'}
						disabled={!!ctx.opBusy}
						onconfirm={onDestroy}
						oncancel={keepIt}
						cancelLabel="Keep it"
						autofocus
					/>
					{#if destroyError}
						<div class="hk-banner destroy-error" role="alert" data-testid="destroy-error">
							<p>
								{destroyError}
								<button
									type="button"
									class="hk-banner-action"
									onclick={() => (destroyError = undefined)}>Dismiss</button
								>
							</p>
						</div>
					{/if}
				{:else}
					<button
						type="button"
						id="destroy-open"
						class="hk-btn-danger hk-btn-sm"
						aria-disabled={ctx.opBusy ? 'true' : undefined}
						onclick={() => !ctx.opBusy && (askingDestroy = true)}>Destroy {project.slug}…</button
					>
				{/if}
			</div>
		</div>
	</section>
{/if}

<style>
	/* ---- Now and history ---- */
	.ogrid {
		display: grid;
		margin-top: 2.75rem;
		row-gap: 2.75rem;
	}
	.oside {
		display: grid;
		row-gap: 2.75rem;
		align-content: start;
		min-width: 0;
	}
	.oevents {
		min-width: 0;
	}
	.oside > :global(.hk-section),
	.oevents > :global(.hk-section) {
		margin-top: 0;
	}
	@container dash (min-width: 760px) {
		.ogrid {
			grid-template-columns: minmax(0, 1fr) 18rem;
			grid-template-areas:
				'events side'
				'resize resize';
			column-gap: 2.5rem;
			align-items: start;
		}
		.oside {
			grid-area: side;
		}
		.oevents {
			grid-area: events;
		}
		.oresize {
			grid-area: resize;
		}
	}

	/* ---- Resize ---- */
	.oresize {
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
	.destroy-act {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.75rem;
	}
	.destroy-act--open {
		align-items: stretch;
	}
	.destroy-error {
		margin: 0;
	}
	@container dash (min-width: 760px) {
		.destroy-row {
			flex-direction: row;
			align-items: flex-start;
			justify-content: space-between;
			gap: 2.5rem;
		}
		.destroy-act {
			flex: none;
			align-items: flex-end;
			padding-top: 0.125rem;
		}
		.destroy-act--open {
			width: 31rem;
			align-items: stretch;
		}
		/* ConfirmType lays out by its own width (a size container), so it
		   needs one: the field, Destroy and Keep it on one row, flush right. */
		.destroy-act > :global(form) {
			flex: none;
			width: 100%;
		}
	}
</style>
