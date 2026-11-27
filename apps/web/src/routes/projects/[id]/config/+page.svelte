<script lang="ts">
	// A project's Config tab (DECISIONS I-841): the base and machine.nix
	// switches, the project's secrets, its webhook routes (I-1110), and on
	// a Nix machine the fragment with Apply in a bar at the window's foot,
	// then the revisions. The menu is gone (I-1698): an Ubuntu machine
	// installs with apt-get, and an earlier menu's entries are listed
	// read-only. The layout above holds the project and the header.
	import { onMount, onDestroy, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { leaveAnyway, leavingAnyway } from '$lib/leave';
	import { MediaQuery } from 'svelte/reactivity';
	import { resolve } from '$app/paths';
	import {
		getConfig,
		putConfig,
		listRevisions,
		applyRevision,
		patchProject,
		opLogUrl,
		getOp,
		startProject,
		stopProject
	} from '$lib/api/client';
	import { toast } from 'svelte-sonner';
	import { toastApiError } from '$lib/api/toast';
	import { dateTime } from '$lib/format';
	import { menuEntries } from '$lib/menuSelection';
	import ProjectSecrets from '$lib/components/project/ProjectSecrets.svelte';
	import { useProject } from '$lib/components/project/context.svelte';
	import NixEditor from '$lib/components/NixEditor.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Config, Revision } from '$lib/api/types';

	const ctx = useProject();
	const id = ctx.id;
	let project = $derived(ctx.project);
	// An erofs machine (I-1462) boots the Ubuntu base: no Nix editor, no
	// machine.nix switch, and a newer base waits for a restart (I-1490).
	let erofs = $derived(project?.guest_runtime === 'erofs');

	let config = $state<Config | undefined>(undefined);
	let revisions = $state<Revision[]>([]);
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	/** The revision a Re-apply was sent for, until its build starts. */
	let reapplying = $state<string | undefined>(undefined);
	let holdSaving = $state(false);
	let personalSaving = $state(false);
	let keepAwakeSaving = $state(false);

	// The newest revisions show; the rest wait behind one toggle.
	const REVISIONS_SHOWN = 8;
	let allRevisions = $state(false);
	let shownRevisions = $derived(allRevisions ? revisions : revisions.slice(0, REVISIONS_SHOWN));

	// From lg the editor opens with room to write (28rem) whatever the
	// fragment's length.
	const wide = new MediaQuery('min-width: 64rem');

	// The Nix editor.
	let fragmentText = $state('');
	let nixDirty = $derived(!!config && fragmentText !== (config.fragment ?? ''));
	let nixOverrideEditable = $state(false);
	// A fragment the menu wrote stays read-only until the reader takes it
	// over, which turns that menu off (the api's "custom fragment" rule).
	let nixReadonly = $derived(!!config?.menu && !nixOverrideEditable);
	// What an earlier menu holds, installed again at every base update
	// (I-1698); an Ubuntu machine lists it and nothing more.
	let oldMenu = $derived(menuEntries(config?.menu));

	// Build/apply state.
	let applying = $state(false);
	let buildLines = $state<string[]>([]);
	let buildDone = $state(false);
	let buildError = $state<string | undefined>(undefined);
	let currentOpId = $state<string | undefined>(undefined);
	/** An op's error without the code the api stores in front of it. */
	function plainError(err: string | undefined): string {
		return (err ?? '').replace(/^menu_failed: /, '').split('\n')[0];
	}
	let eventSource: EventSource | undefined;
	let logEl = $state<HTMLPreElement | undefined>(undefined);

	function fragmentErrorLine(message: string | undefined): number | undefined {
		if (!message) return undefined;
		const m = /fragment\.nix:(\d+):/.exec(message);
		return m ? Number(m[1]) : undefined;
	}

	async function load() {
		try {
			[config, revisions] = await Promise.all([getConfig(id), listRevisions(id)]);
			fragmentText = config?.fragment ?? '';
			loadFailed = false;
		} catch (err) {
			if (!config) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load the config.');
			} else {
				toastApiError(err, 'Could not load the config.');
			}
		}
	}

	// The op an Apply started, kept for this tab (I-1516): a reload, or
	// back then forward, finds it and shows it again while it runs, so the
	// page never offers Apply over an install already under way.
	const opKey = `repose-config-op:${id}`;
	function rememberOp(opId: string | undefined) {
		try {
			if (opId) sessionStorage.setItem(opKey, opId);
			else sessionStorage.removeItem(opKey);
		} catch {
			// Storage refused (a private window): the page works without it.
		}
	}
	async function resumeOp() {
		let opId: string | null = null;
		try {
			opId = sessionStorage.getItem(opKey);
		} catch {
			return;
		}
		if (!opId || currentOpId) return;
		try {
			const op = await getOp(id, opId);
			if (op.state === 'running' || op.state === 'pending') {
				applying = true;
				startBuild(opId);
				return;
			}
		} catch {
			// Gone or unreadable: nothing to show again.
		}
		rememberOp(undefined);
	}

	onMount(() => {
		void load().then(resumeOp);
		// The Apply bar holds the page's primary: the header's Start draws
		// secondary while this tab is open.
		ctx.holdPrimary = true;
		return () => (ctx.holdPrimary = false);
	});

	onDestroy(() => eventSource?.close());

	// An edit not applied yet holds a navigation away (I-578): the bar asks,
	// Stay keeps the page and Leave goes on. Closing the tab gets the
	// browser's own question.
	let dirty = $derived(!applying && nixDirty);
	let heldNavigation = $state<URL | undefined>(undefined);
	let stayButton = $state<HTMLButtonElement | undefined>(undefined);
	beforeNavigate(({ cancel, type, to }) => {
		if (!dirty || leavingAnyway()) return;
		cancel();
		if (type === 'leave' || !to) return;
		heldNavigation = to.url;
		void tick().then(() => stayButton?.focus());
	});
	async function leave() {
		const url = heldNavigation;
		heldNavigation = undefined;
		if (url) await leaveAnyway(url);
	}

	function startBuild(opId: string) {
		currentOpId = opId;
		rememberOp(opId);
		buildLines = [];
		buildDone = false;
		buildError = undefined;
		eventSource?.close();
		void (async () => {
			const url = await opLogUrl(id, opId);
			const es = new EventSource(url);
			eventSource = es;
			es.onmessage = (ev) => {
				const data = JSON.parse(ev.data) as { seq: number; line: string };
				buildLines = [...buildLines, data.line];
				queueMicrotask(() => logEl?.scrollTo(0, logEl.scrollHeight));
			};
			es.addEventListener('done', async () => {
				es.close();
				rememberOp(undefined);
				buildDone = true;
				applying = false;
				try {
					const op = await getOp(id, opId);
					if (op.state === 'error') buildError = plainError(op.error);
				} catch {
					// The revision list below still shows failed/applied.
				}
				await load();
			});
			es.onerror = () => {
				es.close();
				buildDone = true;
				applying = false;
			};
		})();
	}

	async function applyNix() {
		applying = true;
		try {
			const { op_id } = await putConfig(id, { fragment: fragmentText });
			startBuild(op_id);
		} catch (err) {
			applying = false;
			toastApiError(err, 'Could not apply the fragment.');
		}
	}

	// Disabled and relabelled while it is sent, and held with the Apply
	// buttons until the build ends, so a second click cannot start a second
	// build.
	async function reapply(rev: Revision) {
		applying = true;
		reapplying = rev.revision_id;
		try {
			const { op_id } = await applyRevision(id, rev.revision_id);
			startBuild(op_id);
		} catch (err) {
			applying = false;
			toastApiError(err, 'Could not re-apply that revision.');
		} finally {
			reapplying = undefined;
		}
	}

	function editAsNix() {
		nixOverrideEditable = true;
	}

	// The machine.nix switch (DECISIONS I-490) follows the project like the
	// hold flag; a change rebuilds the machine with or without the layer.
	async function togglePersonal(box: HTMLInputElement) {
		if (!project) return;
		const on = box.checked;
		personalSaving = true;
		try {
			ctx.set(await patchProject(id, { personal_opt_out: !on }));
			toast.success(
				on
					? 'machine.nix is on for this machine; it switches in the background.'
					: 'machine.nix is off for this machine; it switches in the background.'
			);
			revisions = await listRevisions(id);
		} catch (err) {
			box.checked = !ctx.project?.personal_opt_out;
			toastApiError(err, 'Could not change the machine.nix switch.');
		} finally {
			personalSaving = false;
		}
	}

	// Effective at once: a machine already frozen is woken by the api, and
	// one that is running is not frozen again while this is ticked
	// (I-818). The checkbox follows the project, as the two above it do.
	//
	// The label says freeze, not awake: "frozen" is the word a user
	// reads for this everywhere else (I-683), and "awake" asked them to
	// hold a second model of the same mechanism (I-898). The wire field
	// stays keep_awake.
	async function toggleKeepAwake(box: HTMLInputElement) {
		if (!project) return;
		const next = box.checked;
		keepAwakeSaving = true;
		try {
			project = await patchProject(id, { keep_awake: next });
		} catch (err) {
			box.checked = !!project.keep_awake;
			toastApiError(err, 'Could not change whether this machine is kept from freezing.');
		} finally {
			keepAwakeSaving = false;
		}
	}

	// Restart for a pending base (I-1490): a running machine is stopped,
	// with no snapshot, and started; a stopped one is started. Asked
	// first, since the machine's processes end.
	let askingRestart = $state(false);
	async function restartForBase() {
		if (!project || ctx.opBusy) return;
		askingRestart = false;
		const running = project.state === 'running';
		ctx.opBusy = running ? 'stop' : 'start';
		try {
			if (running) {
				const stopped = await ctx.waitForOp((await stopProject(id, false)).op_id, () => {});
				if (!stopped) return;
				ctx.opBusy = 'start';
			}
			await ctx.waitForOp((await startProject(id)).op_id, () => {});
		} catch (err) {
			toastApiError(err, 'Could not restart the machine.');
		} finally {
			ctx.opBusy = undefined;
			void ctx.refresh();
		}
	}

	// The checkbox follows the project, not the click: on a refusal it is
	// put back to what the server holds, so it never shows a hold that
	// was not saved.
	async function toggleHold(box: HTMLInputElement) {
		if (!project) return;
		const next = box.checked;
		holdSaving = true;
		try {
			ctx.set(await patchProject(id, { hold_base_updates: next }));
		} catch (err) {
			box.checked = !!ctx.project?.hold_base_updates;
			toastApiError(err, 'Could not change the hold flag.');
		} finally {
			holdSaving = false;
		}
	}
</script>

{#if project}
	<!-- What changes how the machine is built: the base and its hold, and
	     whether your machine.nix is layered on; then whether it is left
	     awake (I-818), which changes how it runs rather than how it is
	     built, and is the only other switch the page carries. -->
	<div class="cfacts" data-testid="config-facts">
		<div class="cfact-row">
			<p class="cfact">Base <span class="hk-mono">{project.base_version}</span></p>
			<label class="hk-check">
				<input
					type="checkbox"
					checked={project.hold_base_updates}
					disabled={holdSaving}
					onchange={(e) => void toggleHold(e.currentTarget)}
				/>
				Hold base updates
			</label>
		</div>
		{#if erofs && project.pending_base_version}
			<div class="hk-banner cbase" role="status" data-testid="pending-base">
				{#if askingRestart}
					<p>Restart {project.slug}? Its processes end; its files and services' data stay.</p>
					<span class="cbase-buttons">
						<button
							type="button"
							class="hk-btn-secondary hk-btn-sm"
							aria-disabled={ctx.opBusy ? 'true' : undefined}
							onclick={() => void restartForBase()}>Restart now</button
						>
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm"
							onclick={() => (askingRestart = false)}>Not now</button
						>
					</span>
				{:else}
					<p>
						Base <span class="hk-mono">{project.pending_base_version}</span> is ready. It is used at the
						next restart.
					</p>
					<button
						type="button"
						class="hk-banner-action"
						aria-disabled={ctx.opBusy ? 'true' : undefined}
						onclick={() => {
							if (project?.state === 'running') askingRestart = true;
							else void restartForBase();
						}}>{ctx.opBusy === 'start' || ctx.opBusy === 'stop' ? 'Restarting…' : 'Restart'}</button
					>
				{/if}
			</div>
		{/if}
		{#if !erofs}
			<div class="cfact-row">
				<label class="hk-check">
					<input
						type="checkbox"
						checked={!project.personal_opt_out}
						disabled={personalSaving}
						onchange={(e) => void togglePersonal(e.currentTarget)}
					/>
					Use your machine.nix on this machine
				</label>
				<a class="hk-link mnix-link" href={resolve('/settings/machine-nix')}>Edit machine.nix</a>
			</div>
		{/if}
		<div class="cfact-row">
			<label class="hk-check">
				<input
					type="checkbox"
					checked={!!project.keep_awake}
					disabled={keepAwakeSaving}
					onchange={(e) => void toggleKeepAwake(e.currentTarget)}
				/>
				Never freeze this machine
			</label>
		</div>
		{#if erofs && oldMenu.length}
			<!-- An earlier menu's entries (I-1698): installed again at every
			     base update, so the reader sees what comes back; nothing to
			     edit. -->
			<div class="cfact-row">
				<p class="cfact" data-testid="old-menu">
					Installed again after every base update, from the menu:
					<span class="hk-mono">{oldMenu.join(', ')}</span>
				</p>
			</div>
		{/if}
	</div>

	<ProjectSecrets projectId={id} projectName={project?.slug} />
{/if}

{#if !erofs}
	<section class="hk-section packages" aria-labelledby="packages-title">
		<LoadState
			status={project && config ? 'ready' : loadFailed ? 'failed' : 'loading'}
			onretry={load}
			error={loadError}
		>
			{#if project && config}
				<div class="hk-section-head chead">
					<h2 id="packages-title" class="hk-h2-app">repose.nix</h2>
					{#if config.applied_at}
						<span class="applied">Applied {dateTime(config.applied_at)}</span>
					{/if}
				</div>

				<div class="cpanel">
					{#if nixReadonly}
						<div class="hk-banner" role="status">
							<p>The menu wrote this file. Editing it here turns that menu off.</p>
							<button type="button" class="hk-banner-action" onclick={editAsNix}>Edit as Nix</button
							>
						</div>
					{/if}
					<NixEditor
						bind:value={fragmentText}
						floor={wide.current ? '28rem' : undefined}
						readonly={nixReadonly}
						errorLine={fragmentErrorLine(buildError)}
					/>
				</div>

				{#if currentOpId}
					<section class="build" aria-labelledby="build-title">
						<h3 id="build-title" class="build-title">Build log</h3>
						<div class="hk-code">
							<pre class="hk-code-body build-log" bind:this={logEl}>{buildLines.join('\n')}</pre>
						</div>
						{#if buildDone && buildError}
							<div class="hk-banner hk-banner--err build-result" role="alert">
								<p class="build-error">{buildError}</p>
							</div>
						{:else if buildDone}
							<p class="build-ok" role="status">
								<span class="hk-dot hk-dot--on" aria-hidden="true"></span>Applied.
							</p>
						{/if}
					</section>
				{/if}

				<!-- Held to the foot of the window while the editor is in view
				     (Guide B 12.23). -->
				<div class="hk-bottom-bar cbar">
					{#if heldNavigation}
						<div class="hk-banner cguard" role="alert">
							<p>Unsaved changes. Leave this page anyway?</p>
							<span class="cguard-buttons">
								<button
									type="button"
									class="hk-btn-secondary hk-btn-sm"
									bind:this={stayButton}
									onclick={() => (heldNavigation = undefined)}>Stay</button
								>
								<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}>Leave</button>
							</span>
						</div>
					{/if}
					<span class="cbar-status" role="status">
						{#if applying}
							Building…
						{:else if nixReadonly}
							Written by the menu
						{:else if nixDirty && !heldNavigation}
							<span class="cbar-dirty">Unsaved changes</span>
						{/if}
					</span>
					<!-- The screen's primary once there is something to apply. -->
					<button
						type="button"
						class={nixDirty ? 'hk-btn' : 'hk-btn-secondary'}
						disabled={applying || nixReadonly || !nixDirty}
						onclick={applyNix}
					>
						{applying ? 'Applying…' : 'Apply'}
					</button>
				</div>
			{/if}
		</LoadState>
	</section>

	{#if project && config}
		<section class="hk-section" aria-labelledby="rev-title">
			<div class="hk-section-head">
				<h2 id="rev-title" class="hk-h2-app">Revisions</h2>
			</div>
			{#if revisions.length === 0}
				<div class="hk-empty-row"><span>No revisions yet.</span></div>
			{:else}
				<ul class="rev-list">
					{#each shownRevisions as rev (rev.revision_id)}
						{@const live = rev.status === 'applied' && rev.revision_id === config?.revision_id}
						<li class="rev">
							<span class="rev-date">{dateTime(rev.created_at)}</span>
							<span class="rev-status"
								>{#if rev.status === 'failed'}<span class="hk-dot hk-dot--err" aria-hidden="true"
									></span>{/if}{live ? 'current' : rev.status}</span
							>
							{#if rev.status !== 'building' && !live}
								<button
									type="button"
									class="hk-btn-ghost hk-btn-sm rev-apply"
									disabled={applying}
									onclick={() => reapply(rev)}
									>{reapplying === rev.revision_id ? 'Re-applying…' : 'Re-apply'}</button
								>
							{/if}
						</li>
					{/each}
				</ul>
				{#if revisions.length > REVISIONS_SHOWN}
					<button
						type="button"
						class="hk-btn-ghost hk-btn-sm revs-toggle"
						aria-expanded={allRevisions}
						onclick={() => (allRevisions = !allRevisions)}
						>{allRevisions ? 'Show fewer' : `Show all ${revisions.length}`}</button
					>
				{/if}
			{/if}
		</section>
	{/if}
{/if}

<style>
	/* ---- Facts ---- */
	/* Two lines under the tabs' rule: the base and its hold, then
	   machine.nix and the way to edit it. */
	.cfacts {
		display: grid;
		margin-top: -0.75rem;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.cfact-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 1.5rem;
		min-height: 2.75rem;
		padding: 0.25rem 0;
		font-size: 0.875rem;
	}
	.cfact-row + .cfact-row {
		border-top: 1px solid var(--hk-hairline);
	}
	.cfact {
		margin: 0;
		color: var(--hk-text-3);
	}
	.cfact .hk-mono {
		color: var(--hk-text);
	}
	.mnix-link {
		font-size: 0.8125rem;
	}
	/* A newer base waiting for a restart (I-1490): one line under the
	   base it replaces, its action at the end. */
	.cbase {
		margin: 0 0 0.75rem;
	}
	.cbase-buttons {
		display: flex;
		gap: 0.25rem;
	}

	/* ---- Packages ---- */
	.chead {
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 1rem;
	}
	.applied {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.cpanel {
		margin-top: 1.25rem;
	}
	.cpanel > :global(.hk-banner) {
		margin-bottom: 1rem;
	}

	/* ---- Build ---- */
	.build {
		margin-top: 2rem;
	}
	.build-title {
		margin: 0 0 0.5rem;
		font-family: var(--hk-font-sans);
		font-size: 0.875rem;
		font-weight: 500;
	}
	.build-log {
		height: 14rem;
		margin: 0;
		white-space: pre;
	}
	.build-result {
		margin: 0.75rem 0 0;
	}
	.build-error {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		white-space: pre-wrap;
	}
	.build-ok {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0.75rem 0 0;
		font-size: 0.875rem;
		color: var(--hk-text-2);
	}

	/* ---- Apply bar ---- */
	.cbar-status {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.cbar-dirty {
		color: var(--hk-text);
	}
	.cguard {
		flex-basis: 100%;
		justify-content: space-between;
		margin: 0;
	}
	.cguard-buttons {
		display: flex;
		gap: 0.25rem;
	}

	/* ---- Revisions ---- */
	.rev-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.rev {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto auto;
		align-items: center;
		gap: 0.75rem;
		min-height: 2.5rem;
		border-top: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
	}
	.rev-date {
		font-variant-numeric: tabular-nums;
		color: var(--hk-text);
	}
	.rev-status {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		color: var(--hk-text-2);
	}
	.rev-apply {
		margin-right: -0.625rem;
	}
	.revs-toggle {
		margin: 0.5rem 0 0 -0.625rem;
	}
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.mnix-link {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
	/* Forced colours draw a ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.rev-apply {
			margin-inline: 0;
		}
	}
</style>
