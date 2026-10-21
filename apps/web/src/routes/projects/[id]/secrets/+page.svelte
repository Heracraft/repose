<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import {
		getProject,
		listSecrets,
		putSecret,
		deleteSecret,
		listAccountSecrets
	} from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { encodeBase64 } from '$lib/base64';
	import { dateTime } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
	import ProjectMeta from '$lib/components/project/ProjectMeta.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { Project, SecretMeta } from '$lib/api/types';

	const id = page.params.id as string;

	const NAME_RE = /^[A-Z][A-Z0-9_]{0,63}$/;
	const RESERVED = new Set([
		'ssh_host_ed25519_key',
		'ssh_host_ed25519_key-cert.pub',
		'user_ca.pub'
	]);
	const MAX_BYTES = 64 * 1024;

	let project = $state<Project | undefined>(undefined);
	let secrets = $state<SecretMeta[] | undefined>(undefined);
	// The account's secrets reach this machine too, under its own (I-697).
	let account = $state<SecretMeta[]>([]);
	let own = $derived(new Set((secrets ?? []).map((s) => s.name)));

	let name = $state('');
	let value = $state('');
	let fileInput = $state<HTMLInputElement | undefined>(undefined);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	// The file Load from file picked, shown by name in place of a native
	// file widget; while one is picked it is the value, not the textarea.
	let file = $state<File | undefined>(undefined);
	let nameError = $state<string | undefined>(undefined);
	let valueError = $state<string | undefined>(undefined);
	let saving = $state(false);
	let deleting = $state<string | undefined>(undefined);
	// The secret whose Delete was pressed once: its row asks in place, the
	// same two-step as Cancel plan on billing, instead of a native confirm().
	let confirming = $state<string | undefined>(undefined);
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);

	async function load() {
		try {
			[project, secrets, account] = await Promise.all([
				getProject(id),
				listSecrets(id),
				// The project's page works without them.
				listAccountSecrets().catch(() => account)
			]);
			loadFailed = false;
		} catch (err) {
			if (secrets === undefined) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load secrets.');
			} else {
				toastApiError(err, 'Could not load secrets.');
			}
		}
	}

	onMount(load);

	function validateName(n: string): string | undefined {
		if (n === '') return 'Name is required.';
		if (RESERVED.has(n)) return 'This name is reserved for the guest’s SSH host material.';
		if (!NAME_RE.test(n))
			return 'Use A-Z, 0-9 and _, starting with a letter, at most 64 characters.';
		return undefined;
	}

	function pickFile() {
		file = fileInput?.files?.[0];
		valueError = undefined;
	}

	function clearFile() {
		file = undefined;
		if (fileInput) fileInput.value = '';
		valueError = undefined;
	}

	function bytes(n: number): string {
		return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`;
	}

	async function onAdd() {
		// The field shows what it holds in capitals; the name sent is that.
		name = name.trim().toUpperCase();
		nameError = validateName(name);
		valueError = undefined;
		let base64: string;
		if (file) {
			if (file.size > MAX_BYTES) {
				valueError = 'Secret values are limited to 64 KB.';
			}
			base64 = encodeBase64(await file.arrayBuffer());
		} else {
			if (new Blob([value]).size > MAX_BYTES) {
				valueError = 'Secret values are limited to 64 KB.';
			}
			base64 = encodeBase64(value);
		}
		if (nameError) nameField?.focus();
		if (nameError || valueError) return;

		saving = true;
		try {
			await putSecret(id, name, base64);
			toast.success(`Stored ${name}.`);
			name = '';
			value = '';
			clearFile();
			await load();
		} catch (err) {
			toastApiError(err, 'Could not store the secret.');
		} finally {
			saving = false;
		}
	}

	/** Close the two-step and give focus back to the row's Delete button. */
	function keep(n: string) {
		confirming = undefined;
		void focusAfterRender(`delete-${n}`);
	}

	async function onDelete(n: string) {
		deleting = n;
		// The row goes with the secret, so focus moves to the next row's
		// Delete, or the previous one, or the Add form when the list empties.
		const names = (secrets ?? []).map((s) => s.name);
		const at = names.indexOf(n);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((m) => `delete-${m}`);
		try {
			await deleteSecret(id, n);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'secret-name');
		} catch (err) {
			toastApiError(err, 'Could not delete the secret.');
		} finally {
			deleting = undefined;
		}
	}
</script>

<!-- A name breaks after an underscore before anywhere else: at 320
     STRIPE_WEBHOOK_SIGNING_SECRET_… split mid-word with break-all. -->
{#snippet secretName(n: string)}{#each n.split('_') as part, i (i)}{#if i > 0}_<wbr
			/>{/if}{part}{/each}{/snippet}

<svelte:head>
	<title>{project ? `Secrets · ${project.name} · repose` : 'Secrets · repose'}</title>
</svelte:head>

<PageShell
	title={project?.name ?? (loadFailed ? 'Secrets' : '…')}
	crumbs={[{ label: 'Projects', href: resolve('/projects') }]}
>
	{#snippet tabs()}
		{#if project}<ProjectMeta {project} />{/if}
		<ProjectTabs {id} />
	{/snippet}
	<LoadState
		status={secrets !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
	>
		<!-- The list and the form side by side from 1024px (I-664, I-837):
		     the list takes what the frame leaves, the form a 21rem column. -->
		<div class="sgrid">
			<div class="slist">
				{#if secrets && secrets.length === 0}
					<!-- The empty state says where a secret lands; the form to add
					     one is beside it (I-485). -->
					<div class="hk-empty-row">
						<h2 class="hk-empty-title">No secrets yet</h2>
						<span
							>The machine sees each as an environment variable and as a file at
							<code class="whitespace-nowrap">/run/repose/secrets/NAME</code>.</span
						>
					</div>
				{:else if secrets && secrets.length > 0}
					<section class="hk-section" aria-labelledby="secrets-title">
						<div class="hk-section-head">
							<h2 id="secrets-title" class="hk-h2-app">
								{secrets.length}
								{secrets.length === 1 ? 'secret' : 'secrets'}
							</h2>
						</div>
						<table class="hk-table stable">
							<thead>
								<tr>
									<th scope="col">Name</th>
									<th scope="col" class="hidden w-px whitespace-nowrap sm:table-cell">Updated</th>
									<th scope="col" class="w-px"><span class="hk-sr">Delete</span></th>
								</tr>
							</thead>
							<tbody>
								{#each secrets as s (s.name)}
									<tr class:asking={confirming === s.name}>
										<!-- Below 640px the date is a second line under the name, so
										     the name has the row's width and breaks at its
										     underscores. -->
										<td class="sname"
											><span>{@render secretName(s.name)}</span><span
												class="sdate-sub block whitespace-nowrap sm:hidden"
												>{dateTime(s.updated_at)}</span
											></td
										>
										<td class="sdate hidden whitespace-nowrap sm:table-cell"
											>{dateTime(s.updated_at)}</td
										>
										<td class="sact">
											{#if confirming !== s.name}
												<button
													type="button"
													id={`delete-${s.name}`}
													class="hk-btn-ghost hk-btn-sm"
													aria-label={`Delete ${s.name}`}
													onclick={() => (confirming = s.name)}>Delete…</button
												>
											{/if}
										</td>
									</tr>
									{#if confirming === s.name}
										<tr class="confirm-row">
											<td colspan="3" data-testid="confirm-delete-secret">
												<div class="hk-confirm">
													<p>
														Delete <span class="hk-mono">{@render secretName(s.name)}</span>?
														Running processes that already read it keep their copy until they
														restart.
													</p>
													<div class="hk-confirm-actions">
														<button
															type="button"
															class="hk-btn-danger hk-btn-sm"
															aria-label={`Delete ${s.name} now`}
															disabled={deleting === s.name}
															onclick={() => onDelete(s.name)}
															>{deleting === s.name ? 'Deleting…' : 'Delete'}</button
														>
														<!-- Focus lands on the safe choice when the question opens. -->
														<button
															type="button"
															class="hk-btn-ghost hk-btn-sm"
															use:focusOnMount
															onclick={() => keep(s.name)}>Keep it</button
														>
													</div>
												</div>
											</td>
										</tr>
									{/if}
								{/each}
							</tbody>
						</table>
					</section>
				{/if}
				{#if secrets && account.length > 0}
					<!-- Set on Settings for every project; a name the project has
					     itself is its own value here (I-697). -->
					<section class="hk-section" data-testid="account-secrets" aria-labelledby="acct-title">
						<div class="hk-section-head">
							<h2 id="acct-title" class="hk-h2-app">From your account</h2>
							<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
							<a class="hk-link settings-link" href={resolve('/settings') + '#secrets'}>Settings</a>
						</div>
						<table class="hk-table stable">
							<thead>
								<tr>
									<th scope="col">Name</th>
									<th scope="col" class="w-px whitespace-nowrap"
										><span class="hk-sr">On this machine</span></th
									>
								</tr>
							</thead>
							<tbody>
								{#each account as s (s.name)}
									<tr>
										<td class="sname" class:overridden={own.has(s.name)}
											><span>{@render secretName(s.name)}</span></td
										>
										<td class="soverride">
											{own.has(s.name) ? 'This project’s own is used' : ''}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</section>
				{/if}
			</div>

			<!-- Each field has a visible label and its error is tied to it with
			     aria-describedby, so a screen reader hears why Add refused.
			     Beside a long list the form stays in view from 1024px. -->
			<form
				class="sform"
				aria-labelledby="add-title"
				onsubmit={(e) => {
					e.preventDefault();
					void onAdd();
				}}
			>
				<h2 id="add-title" class="hk-h2-app">Add a secret</h2>
				<div class="hk-fields sfields">
					<div>
						<label for="secret-name" class="hk-field-label">Name</label>
						<input
							id="secret-name"
							class="hk-input sname-input"
							placeholder="API_KEY"
							autocomplete="off"
							autocapitalize="characters"
							spellcheck="false"
							bind:value={name}
							bind:this={nameField}
							aria-invalid={nameError ? 'true' : undefined}
							aria-describedby={nameError ? 'secret-name-error' : undefined}
							oninput={() => (nameError = undefined)}
						/>
						{#if nameError}<p id="secret-name-error" class="hk-field-error">{nameError}</p>{/if}
					</div>
					<div>
						<label for="secret-value" class="hk-field-label">Value</label>
						<textarea
							id="secret-value"
							rows="3"
							class="hk-textarea svalue"
							autocomplete="off"
							spellcheck="false"
							disabled={!!file}
							bind:value
							aria-invalid={valueError ? 'true' : undefined}
							aria-describedby={valueError ? 'secret-value-error' : undefined}
							oninput={() => (valueError = undefined)}
						></textarea>
						<!-- A ghost button opens the file picker, and the picked file
						     shows by name; the input keeps a real label. -->
						<label for="secret-file" class="hk-sr">Value from a file</label>
						<input
							id="secret-file"
							type="file"
							tabindex="-1"
							class="hk-sr"
							bind:this={fileInput}
							onchange={pickFile}
						/>
						<div class="sfile">
							{#if file}
								<span class="sfile-name" data-testid="secret-file-name">{file.name}</span>
								<span class="sfile-size">{bytes(file.size)}</span>
								<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={clearFile}
									>Clear</button
								>
							{:else}
								<button
									type="button"
									class="hk-btn-ghost hk-btn-sm sfile-load"
									onclick={() => fileInput?.click()}>Load from file</button
								>
							{/if}
						</div>
						{#if valueError}<p id="secret-value-error" class="hk-field-error">{valueError}</p>{/if}
					</div>
				</div>
				<button type="submit" class="hk-btn sadd" disabled={saving}>
					{saving ? 'Storing…' : 'Add'}
				</button>
			</form>
		</div>
	</LoadState>
</PageShell>

<style>
	.sgrid {
		display: grid;
		gap: 2.75rem 3rem;
		align-items: start;
	}
	@media (min-width: 1024px) {
		.sgrid {
			grid-template-columns: minmax(0, 1fr) 21rem;
		}
		.sform {
			position: sticky;
			top: calc(var(--hk-header-h) + 1.5rem);
		}
	}
	.slist {
		min-width: 0;
	}
	.slist :global(.hk-empty-row) {
		justify-content: flex-start;
		gap: 0.25rem 0.75rem;
	}
	/* First under the tabs: their rule is the row's top edge. */
	.slist > :global(.hk-empty-row:first-child) {
		border-top: 0;
		padding-top: 0;
	}
	.stable {
		border-bottom: 1px solid var(--hk-hairline);
	}
	.sname {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		font-weight: 500;
		overflow-wrap: anywhere;
	}
	.sdate,
	.sdate-sub {
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		font-weight: 400;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.sact {
		width: 1px;
		text-align: right;
		white-space: nowrap;
	}
	.stable td.sact {
		padding-block: 0.375rem;
	}
	.sact .hk-btn-ghost {
		margin-right: -0.625rem;
	}
	.confirm-row td {
		padding-top: 0;
		border-top: 0;
	}
	.hk-section-head > .settings-link {
		color: var(--hk-accent);
	}
	.overridden {
		color: var(--hk-text-3);
		text-decoration: line-through;
	}
	.soverride {
		white-space: nowrap;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.sfields {
		margin-top: 0.875rem;
	}
	.sname-input,
	.svalue {
		font-family: var(--hk-font-mono);
	}
	.sname-input {
		text-transform: uppercase;
	}
	.sname-input::placeholder {
		text-transform: none;
	}
	.svalue {
		min-height: 0;
		resize: vertical;
	}
	.sfile {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.75rem;
		min-height: 1.875rem;
		margin-top: 0.5rem;
	}
	.sfile-load {
		margin-left: -0.625rem;
	}
	.sfile-name {
		min-width: 0;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		word-break: break-all;
	}
	.sfile-size {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.sadd {
		margin-top: 1.25rem;
	}
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.hk-section-head > .settings-link {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
	/* Forced colours draw a ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.sact .hk-btn-ghost,
		.sfile-load {
			margin-inline: 0;
		}
	}
</style>
