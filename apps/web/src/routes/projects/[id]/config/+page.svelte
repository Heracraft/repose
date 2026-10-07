<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
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

	// Nix tab state.
	let fragmentText = $state('');
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
	const tabButtons: Record<string, HTMLButtonElement | undefined> = {};
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
	<title>{project ? `${project.name} config — repose` : 'Config — repose'}</title>
</svelte:head>

<PageShell
	title="Config"
	crumbs={[
		{ label: 'Projects', href: resolve('/projects') },
		// "…" while the name loads; after a failed load there is no name
		// coming, so the crumb says what it links to.
		{
			label: project?.name ?? (loadFailed ? 'Project' : '…'),
			href: resolve('/projects/[id]', { id })
		}
	]}
>
	<LoadState
		status={project && config ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
	>
		{#if project && config}
			<!-- The current tab is marked the way the header marks the current
		     page: ink with a 1px underline, no accent colour and no change of
		     weight (DESIGN-LANGUAGE.md). -->
			<!-- The keys are handled on each tab, not on the tablist, so the
			     list itself needs no tabindex and a click on its empty space
			     does not focus it. Only the current tab names a panel in
			     aria-controls: the other panel is not in the DOM, and an id
			     that points at nothing is a broken reference. -->
			<div
				class="flex gap-6 border-b border-rule text-sm"
				role="tablist"
				aria-label="Config editor"
			>
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
						class="-mb-px cursor-pointer border-b py-2 {activeTab === tab
							? 'border-current text-ink'
							: 'border-transparent text-ink-muted hover:text-ink'}"
						onclick={() => (activeTab = tab)}>{name}</button
					>
				{/each}
			</div>

			{#if activeTab === 'menu'}
				<div class="mt-4" role="tabpanel" id="config-panel-menu" aria-labelledby="config-tab-menu">
					{#if config.menu === null || config.menu === undefined}
						<p class="banner banner--warn">
							This project's config was edited by hand; applying from the menu will replace it.
						</p>
					{/if}
					<input
						type="search"
						class="field w-full"
						placeholder="Search packages and services…"
						aria-label="Search packages and services"
						bind:value={search}
					/>
					{#each [...groups.entries()] as [group, items] (group)}
						<div class="form-section">
							<h2 class="text-xl font-semibold">{group}</h2>
							<!-- Their own box, so the first row is :first-child and draws
						     no rule straight under the heading. -->
							<div class="mt-2">
								{#each items as item (item.id)}
									<label class="check-list-row">
										<input
											type="checkbox"
											checked={isSelected(item)}
											onchange={(e) => toggleItem(item, e.currentTarget.checked)}
										/>
										<span class="flex-1">
											<span class="block text-sm font-medium">{item.label}</span>
											<span class="block text-sm text-ink-muted">{item.description}</span>
											{#if item.options?.length && isSelected(item)}
												<!-- Inside the row's label, whose control is the
												     checkbox, so the select is named on its own. -->
												<select
													class="field mt-2 w-48"
													aria-label={`${item.label} ${item.options[0].id}`}
													value={menuOptions[item.id] ?? item.options[0].default}
													onchange={(e) => (menuOptions[item.id] = e.currentTarget.value)}
												>
													{#each item.options[0].values as v (v)}
														<option value={v}>{v}</option>
													{/each}
												</select>
											{/if}
										</span>
									</label>
								{/each}
							</div>
						</div>
					{/each}
					{#if extraPackages.length}
						<div class="form-section">
							<h2 class="text-xl font-semibold">Extra packages</h2>
							<ul class="mt-2">
								{#each extraPackages as pkg (pkg)}
									<li class="check-list-row">
										<span class="flex-1 font-mono text-sm">{pkg}</span>
										<!-- Removal from the list, undone by adding the package
									     back before Apply: the reversible-destructive style. -->
										<button
											type="button"
											class="btn-ghost-danger -mr-2"
											aria-label={`Remove ${pkg}`}
											onclick={() => (extraPackages = extraPackages.filter((p) => p !== pkg))}
											>Remove</button
										>
									</li>
								{/each}
							</ul>
						</div>
					{/if}
					<div class="form-section">
						<button type="button" class="btn" disabled={applying} onclick={applyMenu}>
							{applying ? 'Applying…' : 'Apply'}
						</button>
					</div>
				</div>
			{:else}
				<div class="mt-4" role="tabpanel" id="config-panel-nix" aria-labelledby="config-tab-nix">
					{#if nixReadonly}
						<p class="banner banner--warn">
							This project is managed by the menu. Editing here takes over from the menu.
							<button type="button" class="link" onclick={editAsNix}>Edit as Nix</button>
						</p>
					{/if}
					<NixEditor
						bind:value={fragmentText}
						readonly={nixReadonly}
						errorLine={fragmentErrorLine(buildError)}
					/>
					<div class="form-section">
						<button type="button" class="btn" disabled={applying || nixReadonly} onclick={applyNix}>
							{applying ? 'Applying…' : 'Apply'}
						</button>
					</div>
				</div>
			{/if}

			{#if currentOpId}
				<div class="form-section">
					<h2 class="text-xl font-semibold">Build log</h2>
					<pre class="codeblock mt-2 h-56 overflow-y-auto" bind:this={logEl}>{buildLines.join(
							'\n'
						)}</pre>
					{#if buildDone && buildError}
						<div class="banner banner--error mt-3">
							<p class="font-mono text-compact whitespace-pre-wrap">{buildError}</p>
						</div>
					{:else if buildDone}
						<p class="mt-3 text-sm text-emerald-700 dark:text-emerald-400">Applied.</p>
					{/if}
				</div>
			{/if}

			<div class="form-section">
				<h2 class="text-xl font-semibold">machine.nix</h2>
				<label class="check-row mt-2">
					<input
						type="checkbox"
						checked={!project.personal_opt_out}
						disabled={personalSaving}
						onchange={(e) => void togglePersonal(e.currentTarget)}
					/>
					Use your machine.nix on this machine
				</label>
				<p class="mt-1 text-sm text-ink-muted">
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
					<a class="link" href={resolve('/settings') + '#machine-nix'}
						>Edit machine.nix on your account</a
					>
				</p>
			</div>

			<div class="form-section">
				<h2 class="text-xl font-semibold">Base updates</h2>
				<label class="check-row mt-2">
					<input
						type="checkbox"
						checked={project.hold_base_updates}
						disabled={holdSaving}
						onchange={(e) => void toggleHold(e.currentTarget)}
					/>
					Hold base updates (currently on {project.base_version})
				</label>
			</div>

			<div class="form-section">
				<h2 class="text-xl font-semibold">Revisions</h2>
				{#if revisions.length === 0}
					<p class="mt-2 text-sm text-ink-muted">No revisions yet.</p>
				{:else}
					<ul class="mt-2">
						{#each revisions as rev (rev.revision_id)}
							<li class="row flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-sm">
								<span class="tabular-nums">
									{dateTime(rev.created_at)}
									<span
										class="badge ml-2"
										class:badge--new={rev.status === 'applied'}
										class:badge--error={rev.status === 'failed'}>{rev.status}</span
									>
								</span>
								{#if rev.status !== 'building'}
									<button
										type="button"
										class="btn-ghost -mr-2"
										disabled={applying}
										onclick={() => reapply(rev)}
										>{reapplying === rev.revision_id ? 'Re-applying…' : 'Re-apply'}</button
									>
								{/if}
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		{/if}
	</LoadState>
</PageShell>
