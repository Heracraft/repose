<!--
  A project's secrets, under Secrets on its Config tab (DECISIONS I-697,
  I-841): the names and when each changed, Delete… asking in its row,
  and "Add secret", which opens the form inline under the head. Under
  them, the account's secrets, which reach this machine too; a name the
  project sets itself is struck there, since the project's own wins.
  Values never come back from the api, so none is ever in the page.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { listSecrets, putSecret, deleteSecret, listAccountSecrets } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { encodeBase64 } from '$lib/base64';
	import { dateTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { SecretMeta } from '$lib/api/types';

	let { projectId }: { projectId: string } = $props();

	const NAME_RE = /^[A-Z][A-Z0-9_]{0,63}$/;
	const RESERVED = new Set([
		'ssh_host_ed25519_key',
		'ssh_host_ed25519_key-cert.pub',
		'user_ca.pub'
	]);
	const MAX_BYTES = 64 * 1024;

	let secrets = $state<SecretMeta[] | undefined>(undefined);
	let account = $state<SecretMeta[]>([]);
	let own = $derived(new Set((secrets ?? []).map((s) => s.name)));
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);

	let adding = $state(false);
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
	/** The secret whose Delete was pressed once: its row asks in place. */
	let confirming = $state<string | undefined>(undefined);

	async function load() {
		try {
			[secrets, account] = await Promise.all([
				listSecrets(projectId),
				// The project's list works without them.
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

	onMount(async () => {
		await load();
		// /projects/<id>/secrets and the old #secrets links land here: the
		// section arrives after the navigation's own scroll, so it scrolls
		// itself into view.
		if (location.hash === '#secrets') {
			await tick();
			document.getElementById('secrets')?.scrollIntoView({ block: 'start' });
		}
	});

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

	function size(n: number): string {
		return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`;
	}

	/** Closes the form, cleared, and gives focus back to Add secret. */
	function closeForm() {
		adding = false;
		name = '';
		value = '';
		nameError = undefined;
		valueError = undefined;
		clearFile();
		void focusAfterRender('secret-add-open');
	}

	async function onAdd() {
		// The field shows what it holds in capitals; the name sent is that.
		name = name.trim().toUpperCase();
		nameError = validateName(name);
		valueError = undefined;
		let base64: string;
		if (file) {
			if (file.size > MAX_BYTES) valueError = 'Secret values are limited to 64 KB.';
			base64 = encodeBase64(await file.arrayBuffer());
		} else {
			if (new Blob([value]).size > MAX_BYTES) valueError = 'Secret values are limited to 64 KB.';
			base64 = encodeBase64(value);
		}
		if (nameError) nameField?.focus();
		if (nameError || valueError) return;

		saving = true;
		try {
			const stored = name;
			await putSecret(projectId, stored, base64);
			toast.success(`Stored ${stored}.`);
			await load();
			closeForm();
		} catch (err) {
			toastApiError(err, 'Could not store the secret.');
		} finally {
			saving = false;
		}
	}

	/** Closes the two-step and gives focus back to the row's Delete. */
	function keep(n: string) {
		confirming = undefined;
		void focusAfterRender(`delete-${n}`);
	}

	async function onDelete(n: string) {
		deleting = n;
		// The row goes with the secret, so focus moves to the next row's
		// Delete, or the previous one, or Add secret when the list empties.
		const names = (secrets ?? []).map((s) => s.name);
		const at = names.indexOf(n);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((m) => `delete-${m}`);
		try {
			await deleteSecret(projectId, n);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'secret-add-open');
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

<section class="hk-section" id="secrets" aria-labelledby="secrets-title">
	<div class="hk-section-head">
		<div class="stitle">
			<h2 id="secrets-title" class="hk-h2-app">Secrets</h2>
			{#if secrets && secrets.length > 0}
				<span class="count"
					>{secrets.length}<span class="hk-sr">
						{secrets.length === 1 ? 'secret' : 'secrets'}</span
					></span
				>
			{/if}
		</div>
		{#if secrets}
			<button
				type="button"
				id="secret-add-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-expanded={adding}
				aria-controls="secret-add"
				onclick={() => (adding ? closeForm() : (adding = true))}>Add secret</button
			>
		{/if}
	</div>

	<LoadState
		status={secrets !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
		shape="rows"
	>
		{#if adding}
			<!-- Each field has a visible label and its error is tied to it,
			     so a screen reader hears why Add refused. -->
			<form
				id="secret-add"
				class="sform"
				aria-labelledby="add-title"
				onsubmit={(e) => {
					e.preventDefault();
					void onAdd();
				}}
			>
				<h3 id="add-title" class="hk-sr">Add a secret</h3>
				<div class="sfields">
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
							use:focusOnMount
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
							rows="2"
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
								<span class="sfile-size">{size(file.size)}</span>
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
				<div class="sbuttons">
					<button type="submit" class="hk-btn-secondary" disabled={saving}>
						{saving ? 'Storing…' : 'Add'}
					</button>
					<button type="button" class="hk-btn-ghost" onclick={closeForm}>Cancel</button>
				</div>
			</form>
		{/if}

		{#if secrets && secrets.length === 0}
			<div class="hk-empty-row sempty">
				<h3 class="hk-empty-title">No secrets yet</h3>
				<span
					>The machine sees each as an environment variable and as a file at
					<code class="whitespace-nowrap">/run/repose/secrets/NAME</code>.</span
				>
			</div>
		{:else if secrets}
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
						<tr>
							<!-- Below 640px the date is a second line under the name, so
							     the name has the row's width and breaks at its underscores. -->
							<td class="sname"
								><span>{@render secretName(s.name)}</span><span
									class="sdate-sub block whitespace-nowrap sm:hidden">{dateTime(s.updated_at)}</span
								></td
							>
							<td class="sdate hidden whitespace-nowrap sm:table-cell">{dateTime(s.updated_at)}</td>
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
											Delete <span class="hk-mono">{@render secretName(s.name)}</span>? Running
											processes that already read it keep their copy until they restart.
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
		{/if}

		{#if secrets && account.length > 0}
			<!-- Set under Settings for every project; a name the project has
			     itself is its own value here (I-697). -->
			<div class="acct" data-testid="account-secrets">
				<div class="acct-head">
					<h3 class="acct-title">From your account</h3>
					<a class="hk-link settings-link" href={resolve('/settings/secrets')}>Settings</a>
				</div>
				<ul class="acct-list">
					{#each account as s (s.name)}
						<li>
							<span class="sname" class:overridden={own.has(s.name)}
								>{@render secretName(s.name)}</span
							>
							{#if own.has(s.name)}<span class="soverride">This project’s own is used</span>{/if}
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</LoadState>
</section>

<style>
	.stitle {
		display: flex;
		align-items: baseline;
		gap: 0.625rem;
	}
	.count {
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	/* The add form: one panel under the head, the fields side by side
	   from a 640px column. */
	.sform {
		margin-bottom: 1.25rem;
		padding: 1rem;
		border-radius: var(--hk-radius);
		background: var(--hk-surface-2);
	}
	.sfields {
		display: grid;
		gap: 1rem 1.25rem;
	}
	@container dash (min-width: 640px) {
		.sfields {
			grid-template-columns: 16rem minmax(0, 1fr);
		}
	}
	.sbuttons {
		display: flex;
		gap: 0.5rem;
		margin-top: 0.75rem;
	}
	.sempty {
		justify-content: flex-start;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
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
	.acct {
		margin-top: 1.75rem;
	}
	.acct-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		margin-bottom: 0.5rem;
	}
	/* Guide B's group header: 11px caps, in the sans. */
	.acct-title {
		margin: 0;
		font-family: var(--hk-font-sans);
		font-size: 0.6875rem;
		font-weight: 500;
		line-height: 1rem;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.settings-link {
		font-size: 0.8125rem;
		color: var(--hk-accent);
	}
	.acct-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.acct-list li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.25rem 1rem;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
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
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.settings-link {
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
