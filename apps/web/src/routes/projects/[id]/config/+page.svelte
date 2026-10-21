<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { leaveAnyway, leavingAnyway } from '$lib/leave';
	import { MediaQuery } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import {
		getConfig,
		putConfig,
		getCatalog,
		listRevisions,
		applyRevision,
		getProject,
		patchProject,
		opLogUrl,
		getOp
	} from '$lib/api/client';
	import { toast } from 'svelte-sonner';
	import { toastApiError } from '$lib/api/toast';
	import { dateTime } from '$lib/format';
	import {
		toggleSelection,
		groupCatalog,
		buildMenuSelection,
		parseMenuSelection
	} from '$lib/menuSelection';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
	import NixEditor from '$lib/components/NixEditor.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { CatalogItem, Config, Project, Revision } from '$lib/api/types';

	const id = page.params.id as string;

	let project = $state<Project | undefined>(undefined);
	let config = $state<Config | undefined>(undefined);
	let catalog = $state<CatalogItem[]>([]);
	let revisions = $state<Revision[]>([]);
	const TABS = [
		['menu', 'Menu'],
		['nix', 'Nix']
	] as const;
	let activeTab = $state<'menu' | 'nix'>('nix');
	let search = $state('');
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	/** The revision a Re-apply was sent for, until its build starts. */
	let reapplying = $state<string | undefined>(undefined);
	let holdSaving = $state(false);
	let personalSaving = $state(false);

	// Menu tab state.
	let selectedPackages = $state<Set<string>>(new Set());
	let selectedServices = $state<Set<string>>(new Set());
	let menuOptions = $state<Record<string, string>>({});
	// nixpkgs packages added by name (`repose config add gcc`, I-220).
	let extraPackages = $state<string[]>([]);

	// What the menu held at load, to tell an unapplied change from none.
	let savedMenuKey = $state('');
	let menuKey = $derived(
		JSON.stringify([
			[...selectedPackages, ...selectedServices].sort(),
			Object.entries(menuOptions).sort(),
			[...extraPackages].sort()
		])
	);
	let menuDirty = $derived(menuKey !== savedMenuKey);
	let selectedCount = $derived(
		selectedPackages.size + selectedServices.size + extraPackages.length
	);

	// The rail lists the newest revisions; the rest wait behind one toggle.
	const REVISIONS_SHOWN = 8;
	let allRevisions = $state(false);
	let shownRevisions = $derived(allRevisions ? revisions : revisions.slice(0, REVISIONS_SHOWN));

	// From lg the editor is the page's subject beside a short rail, so it
	// opens with room to write (28rem) whatever the fragment's length.
	const wide = new MediaQuery('min-width: 64rem');

	/**
	 * The revisions box takes a tab stop while its list is taller than it
	 * (Show all, from lg) and gives it up when the list fits, so a
	 * keyboard can scroll to the oldest revision (axe
	 * scrollable-region-focusable).
	 */
	function tabStopWhenTall(node: HTMLElement) {
		const mark = () => {
			if (node.scrollHeight > node.clientHeight) node.tabIndex = 0;
			else node.removeAttribute('tabindex');
		};
		mark();
		const ro = new ResizeObserver(mark);
		ro.observe(node);
		if (node.firstElementChild) ro.observe(node.firstElementChild);
		return { destroy: () => ro.disconnect() };
	}

	// Nix tab state.
	let fragmentText = $state('');
	let nixDirty = $derived(!!config && fragmentText !== (config.fragment ?? ''));
	let nixOverrideEditable = $state(false);
	let nixReadonly = $derived(!!config?.menu && !nixOverrideEditable);

	// Build/apply state, shared by both tabs.
	let applying = $state(false);
	let buildLines = $state<string[]>([]);
	let buildDone = $state(false);
	let buildError = $state<string | undefined>(undefined);
	let currentOpId = $state<string | undefined>(undefined);
	let eventSource: EventSource | undefined;
	let logEl = $state<HTMLPreElement | undefined>(undefined);

	function fragmentErrorLine(message: string | undefined): number | undefined {
		if (!message) return undefined;
		const m = /fragment\.nix:(\d+):/.exec(message);
		return m ? Number(m[1]) : undefined;
	}

	async function load() {
		try {
			[project, config, catalog, revisions] = await Promise.all([
				getProject(id),
				getConfig(id),
				getCatalog(),
				listRevisions(id)
			]);
			activeTab = config?.menu ? 'menu' : 'nix';
			fragmentText = config?.fragment ?? '';
			const menu = parseMenuSelection(config?.menu);
			const services = new Set(catalog.filter((c) => c.kind === 'service').map((c) => c.id));
			selectedPackages = new Set(menu.ids.filter((i) => !services.has(i)));
			selectedServices = new Set(menu.ids.filter((i) => services.has(i)));
			menuOptions = menu.options;
			extraPackages = menu.packages;
			savedMenuKey = menuKey;
			loadFailed = false;
		} catch (err) {
			if (!project || !config) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load the config.');
			} else {
				toastApiError(err, 'Could not load the config.');
			}
		}
	}

	onMount(load);

	// The two tabs switch a panel in place (the URL stays), so they are
	// ARIA tabs: one tab stop for the pair, and the arrow keys, Home and
	// End move between them and show the panel at once.
	let tabButtons = $state<Record<string, HTMLButtonElement | undefined>>({});
	function onTabKey(e: KeyboardEvent) {
		const i = TABS.findIndex(([t]) => t === activeTab);
		let next: number;
		if (e.key === 'ArrowRight') next = (i + 1) % TABS.length;
		else if (e.key === 'ArrowLeft') next = (i - 1 + TABS.length) % TABS.length;
		else if (e.key === 'Home') next = 0;
		else if (e.key === 'End') next = TABS.length - 1;
		else return;
		e.preventDefault();
		activeTab = TABS[next][0];
		tabButtons[activeTab]?.focus();
	}
	onDestroy(() => eventSource?.close());

	// An edit not applied yet holds a navigation away (I-578): the bar asks,
	// Stay keeps the page and Leave goes on. Closing the tab gets the
	// browser's own question.
	let dirty = $derived(!applying && (menuDirty || nixDirty));
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

	let groups = $derived(groupCatalog(catalog, search));

	function toggleItem(item: CatalogItem, checked: boolean) {
		if (item.kind === 'service') {
			selectedServices = toggleSelection(selectedServices, item.id, checked);
		} else {
			selectedPackages = toggleSelection(selectedPackages, item.id, checked);
		}
	}

	function isSelected(item: CatalogItem): boolean {
		return item.kind === 'service' ? selectedServices.has(item.id) : selectedPackages.has(item.id);
	}

	function startBuild(opId: string) {
		currentOpId = opId;
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
				buildDone = true;
				applying = false;
				try {
					const op = await getOp(id, opId);
					if (op.state === 'error') buildError = op.error;
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

	async function applyMenu() {
		applying = true;
		const menu = buildMenuSelection(
			catalog,
			new Set([...selectedPackages, ...selectedServices]),
			menuOptions,
			extraPackages
		);
		try {
			const { op_id } = await putConfig(id, { menu });
			startBuild(op_id);
		} catch (err) {
			applying = false;
			toastApiError(err, 'Could not apply the menu selection.');
		}
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
		activeTab = 'nix';
	}

	// The machine.nix switch (DECISIONS I-490) follows the project like the
	// hold flag; a change rebuilds the machine with or without the layer.
	async function togglePersonal(box: HTMLInputElement) {
		if (!project) return;
		const on = box.checked;
		personalSaving = true;
		try {
			project = await patchProject(id, { personal_opt_out: !on });
			toast.success(
				on
					? 'machine.nix is on for this machine; it switches in the background.'
					: 'machine.nix is off for this machine; it switches in the background.'
			);
			revisions = await listRevisions(id);
		} catch (err) {
			box.checked = !project.personal_opt_out;
			toastApiError(err, 'Could not change the machine.nix switch.');
		} finally {
			personalSaving = false;
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
			project = await patchProject(id, { hold_base_updates: next });
		} catch (err) {
			box.checked = project.hold_base_updates;
			toastApiError(err, 'Could not change the hold flag.');
		} finally {
			holdSaving = false;
		}
	}
</script>

<svelte:head>
	<title>{project ? `Config · ${project.name} · repose` : 'Config · repose'}</title>
</svelte:head>

<PageShell
	title={project?.name ?? (loadFailed ? 'Config' : '…')}
	crumbs={[{ label: 'Projects', href: resolve('/projects') }]}
>
	{#snippet tabs()}<ProjectTabs {id} />{/snippet}
	<LoadState
		status={project && config ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
	>
		{#if project && config}
			<!-- Two columns from 1024px: what you edit, and a rail of the
			     switches and the history (I-664, I-837). -->
			<div class="cgrid">
				<div class="cmain">
					<!-- The two tabs switch a panel in place (the URL stays), so they
					     are ARIA tabs: one tab stop for the pair, the arrow keys, Home
					     and End move between them. Only the current tab names a panel. -->
					<div class="chead">
						<div class="hk-tablist" role="tablist" aria-label="Config editor">
							{#each TABS as [tab, name] (tab)}
								<button
									type="button"
									role="tab"
									id="config-tab-{tab}"
									aria-selected={activeTab === tab}
									aria-controls={activeTab === tab ? `config-panel-${tab}` : undefined}
									tabindex={activeTab === tab ? 0 : -1}
									bind:this={tabButtons[tab]}
									onkeydown={onTabKey}
									onclick={() => (activeTab = tab)}>{name}</button
								>
							{/each}
						</div>
						{#if config.applied_at}
							<span class="applied">Applied {dateTime(config.applied_at)}</span>
						{/if}
					</div>

					{#if activeTab === 'menu'}
						<div
							class="cpanel"
							role="tabpanel"
							id="config-panel-menu"
							aria-labelledby="config-tab-menu"
						>
							{#if config.menu === null || config.menu === undefined}
								<div class="hk-banner" role="status">
									<p>
										This project's config was edited by hand; applying from the menu will replace
										it.
									</p>
								</div>
							{/if}
							<input
								type="search"
								class="hk-input hk-input--search"
								placeholder="Search packages and services…"
								aria-label="Search packages and services"
								bind:value={search}
							/>
							{#each [...groups.entries()] as [group, items] (group)}
								<div class="cgroup">
									<h2 class="group-title">
										{group}<span class="group-n">{items.length}</span>
									</h2>
									<div class="citems">
										{#each items as item (item.id)}
											<div class="citem">
												<label class="hk-check citem-check">
													<input
														type="checkbox"
														checked={isSelected(item)}
														onchange={(e) => toggleItem(item, e.currentTarget.checked)}
													/>
													<span class="citem-text">
														<span class="citem-name">{item.label}</span>
														<span class="citem-desc">{item.description}</span>
													</span>
												</label>
												{#if item.options?.length && isSelected(item)}
													<select
														class="hk-select citem-select"
														aria-label={`${item.label} ${item.options[0].id}`}
														value={menuOptions[item.id] ?? item.options[0].default}
														onchange={(e) => (menuOptions[item.id] = e.currentTarget.value)}
													>
														{#each item.options[0].values as v (v)}
															<option value={v}>{v}</option>
														{/each}
													</select>
												{/if}
											</div>
										{/each}
									</div>
								</div>
							{/each}
							{#if extraPackages.length}
								<div class="cgroup">
									<h2 class="group-title">
										Extra packages<span class="group-n">{extraPackages.length}</span>
									</h2>
									<ul class="extras">
										{#each extraPackages as pkg (pkg)}
											<li class="extra">
												<span class="hk-mono">{pkg}</span>
												<!-- Undone by adding the package back before Apply. -->
												<button
													type="button"
													class="hk-btn-ghost hk-btn-sm extra-remove"
													aria-label={`Remove ${pkg}`}
													onclick={() => (extraPackages = extraPackages.filter((p) => p !== pkg))}
													>Remove</button
												>
											</li>
										{/each}
									</ul>
								</div>
							{/if}
						</div>
					{:else}
						<div
							class="cpanel"
							role="tabpanel"
							id="config-panel-nix"
							aria-labelledby="config-tab-nix"
						>
							{#if nixReadonly}
								<div class="hk-banner" role="status">
									<p>This project is managed by the menu. Editing here takes over from the menu.</p>
									<button type="button" class="hk-banner-action" onclick={editAsNix}
										>Edit as Nix</button
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
					{/if}

					{#if currentOpId}
						<section class="hk-section build" aria-labelledby="build-title">
							<div class="hk-section-head">
								<h2 id="build-title" class="hk-h2-app">Build log</h2>
							</div>
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

					<!-- One bar for both tabs, held to the foot of the window, so
					     Apply is in reach from any group (Guide B 12.23). -->
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
									<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}>Leave</button
									>
								</span>
							</div>
						{/if}
						<span class="cbar-status" role="status">
							{#if activeTab === 'menu'}
								{selectedCount} selected{#if applying}&nbsp;· Building…{:else if menuDirty}&nbsp;·
									<span class="cbar-dirty">Unsaved changes</span>{/if}
							{:else if applying}
								Building…
							{:else if nixReadonly}
								Managed by the menu
							{:else if nixDirty}
								<span class="cbar-dirty">Unsaved changes</span>
							{:else}
								Saved
							{/if}
						</span>
						<button
							type="button"
							class="hk-btn"
							disabled={applying || (activeTab === 'nix' && nixReadonly)}
							onclick={activeTab === 'menu' ? applyMenu : applyNix}
						>
							{applying ? 'Applying…' : 'Apply'}
						</button>
					</div>
				</div>

				<!-- The rail: what changes how the machine is built, and what it
				     was built from. From 1024px it stays in view beside the
				     editor, and the full revision list scrolls inside itself. -->
				<div class="crail">
					<section class="hk-section" aria-labelledby="base-title">
						<div class="hk-section-head">
							<h2 id="base-title" class="hk-h2-app">Base updates</h2>
						</div>
						<p class="rail-line">On <span class="hk-mono">{project.base_version}</span>.</p>
						<label class="hk-check">
							<input
								type="checkbox"
								checked={project.hold_base_updates}
								disabled={holdSaving}
								onchange={(e) => void toggleHold(e.currentTarget)}
							/>
							Hold base updates
						</label>
					</section>

					<section class="hk-section" aria-labelledby="mnix-title">
						<div class="hk-section-head">
							<h2 id="mnix-title" class="hk-h2-app">machine.nix</h2>
						</div>
						<label class="hk-check">
							<input
								type="checkbox"
								checked={!project.personal_opt_out}
								disabled={personalSaving}
								onchange={(e) => void togglePersonal(e.currentTarget)}
							/>
							Use your machine.nix on this machine
						</label>
						<p class="rail-link">
							<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
							<a class="hk-link" href={resolve('/settings') + '#machine-nix'}
								>Edit machine.nix on your account</a
							>
						</p>
					</section>

					<section class="hk-section" aria-labelledby="rev-title">
						<div class="hk-section-head">
							<h2 id="rev-title" class="hk-h2-app">Revisions</h2>
						</div>
						{#if revisions.length === 0}
							<div class="hk-empty-row"><span>No revisions yet.</span></div>
						{:else}
							<!-- The full list scrolls inside itself from 1024px; while it
							     does, the box takes a tab stop so a keyboard reaches its end. -->
							<div
								class="revs"
								class:revs--all={allRevisions}
								use:tabStopWhenTall
								role={allRevisions && wide.current ? 'region' : undefined}
								aria-label={allRevisions && wide.current ? 'Revisions' : undefined}
							>
								<ul class="rev-list">
									{#each shownRevisions as rev (rev.revision_id)}
										<li class="rev">
											<span class="rev-date">{dateTime(rev.created_at)}</span>
											<span class="rev-status"
												>{#if rev.status === 'failed'}<span
														class="hk-dot hk-dot--err"
														aria-hidden="true"
													></span>{/if}{rev.status}</span
											>
											{#if rev.status !== 'building'}
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
							</div>
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
				</div>
			</div>
		{/if}
	</LoadState>
</PageShell>

<style>
	.cgrid {
		display: grid;
		gap: 2.75rem 3rem;
		align-items: start;
	}
	@media (min-width: 1024px) {
		.cgrid {
			grid-template-columns: minmax(0, 1fr) 18rem;
		}
		.crail {
			position: sticky;
			top: calc(var(--hk-header-h) + 1.5rem);
		}
	}
	.cmain,
	.crail {
		min-width: 0;
	}
	.chead {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
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
	@media (max-width: 639.98px) {
		.chead .hk-tablist {
			display: grid;
			grid-template-columns: 1fr 1fr;
			width: 100%;
		}
	}

	/* ---- Menu ---- */
	.cgroup {
		margin-top: 2rem;
	}
	/* Guide B's group header: 11px caps with a count. A heading element
	   (tests read "Extra packages" as one), in the sans. */
	.group-title {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		margin: 0 0 0.25rem;
		font-family: var(--hk-font-sans);
		font-size: 0.6875rem;
		font-weight: 500;
		line-height: 1rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.group-n {
		font-variant-numeric: tabular-nums;
		letter-spacing: 0;
	}
	.citems {
		display: grid;
		border-bottom: 1px solid var(--hk-hairline);
	}
	@media (min-width: 768px) {
		.citems {
			grid-template-columns: repeat(2, minmax(0, 1fr));
			column-gap: 2rem;
		}
	}
	.citem {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: 0.25rem 0.75rem;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.citem-check {
		align-items: flex-start;
		min-width: 0;
	}
	.citem-check :global(input) {
		margin-top: 0.1875rem;
	}
	.citem-text {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.citem-name {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.citem-desc {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.citem-select {
		width: auto;
		height: 1.875rem;
		font-size: 0.8125rem;
	}
	.extras {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.extra {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.375rem 0;
		border-top: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
	}
	.extra-remove {
		margin-right: -0.625rem;
	}

	/* ---- Build ---- */
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

	/* ---- Rail ---- */
	.rail-line {
		margin: 0 0 0.5rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.rail-line .hk-mono {
		color: var(--hk-text);
	}
	.crail :global(.hk-check) {
		font-size: 0.875rem;
	}
	.rail-link {
		margin: 0.5rem 0 0;
		font-size: 0.8125rem;
	}
	@media (min-width: 1024px) {
		.revs--all {
			max-height: 24rem;
			overflow-y: auto;
		}
	}
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
</style>
