<!--
  The account's secrets, Settings → Secrets (DECISIONS I-697, I-841):
  every project gets them with its own, and a project's own secret of the
  same name wins. The same names, limits, inline "Add secret" form and
  two-step delete as a project's Secrets on its Config tab. Values never
  come back from the api, so none is ever in the page.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { listAccountSecrets, putAccountSecret, deleteAccountSecret } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { encodeBase64 } from '$lib/base64';
	import { dateTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { SecretMeta } from '$lib/api/types';

	const NAME_RE = /^[A-Z][A-Z0-9_]{0,63}$/;
	const SHELL = new Set(['BASH_ENV', 'ENV', 'REPOSE_ENV_GEN']);
	const MAX_BYTES = 64 * 1024;

	let secrets = $state<SecretMeta[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let adding = $state(false);
	let name = $state('');
	let value = $state('');
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);
	// The file Load from file picked; while one is picked it is the value.
	let file = $state<File | undefined>(undefined);
	let nameError = $state<string | undefined>(undefined);
	let valueError = $state<string | undefined>(undefined);
	let saving = $state(false);
	let deleting = $state<string | undefined>(undefined);
	let confirming = $state<string | undefined>(undefined);

	async function load() {
		try {
			secrets = await listAccountSecrets();
			loadError = undefined;
		} catch (err) {
			if (secrets === undefined) loadError = loadErrorText(err, 'Could not load secrets.');
			else toastApiError(err, 'Could not load secrets.');
		}
	}

	onMount(load);

	function validateName(n: string): string | undefined {
		if (n === '') return 'Name is required.';
		if (SHELL.has(n)) return `${n} is reserved.`;
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

	/** Closes the form, cleared, and gives focus back to Add secret. */
	function closeForm() {
		adding = false;
		name = '';
		value = '';
		nameError = undefined;
		valueError = undefined;
		clearFile();
		void focusAfterRender('account-secret-add-open');
	}

	async function onAdd() {
		if (saving) return;
		name = name.trim().toUpperCase();
		nameError = validateName(name);
		const size = file ? file.size : new Blob([value]).size;
		valueError = size > MAX_BYTES ? 'Secret values are limited to 64 KB.' : undefined;
		if (nameError || valueError) {
			if (nameError) nameField?.focus();
			else document.getElementById('account-secret-value')?.focus();
			return;
		}
		const base64 = file ? encodeBase64(await file.arrayBuffer()) : encodeBase64(value);
		saving = true;
		try {
			const stored = name;
			const res = await putAccountSecret(stored, base64);
			const own = res?.overridden_by ?? [];
			// The projects that keep their own would otherwise read as
			// having this value.
			toast.success(`Stored ${stored} for every project.`, {
				description: own.length
					? `${own.join(', ')} ${own.length === 1 ? 'keeps its' : 'keep their'} own.`
					: undefined
			});
			await load();
			closeForm();
		} catch (err) {
			toastApiError(err, 'Could not store the secret.');
		} finally {
			saving = false;
		}
	}

	function keep(n: string) {
		confirming = undefined;
		void focusAfterRender(`account-delete-${n}`);
	}

	async function onDelete(n: string) {
		if (deleting) return;
		deleting = n;
		// The row goes with the secret, so focus moves to the next row's
		// Delete, or the previous one, or Add secret when the list empties.
		const names = (secrets ?? []).map((s) => s.name);
		const at = names.indexOf(n);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((m) => `account-delete-${m}`);
		try {
			await deleteAccountSecret(n);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'account-secret-add-open');
		} catch (err) {
			toastApiError(err, 'Could not delete the secret.');
		} finally {
			deleting = undefined;
		}
	}
</script>

<!-- A name breaks after an underscore before anywhere else. -->
{#snippet secretName(n: string)}{#each n.split('_') as part, i (i)}{#if i > 0}_<wbr
			/>{/if}{part}{/each}{/snippet}

<section class="hk-section" id="secrets" aria-labelledby="secrets-h">
	<div class="hk-section-head">
		<div class="stitle">
			<h2 id="secrets-h" class="hk-h2-app">Secrets</h2>
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
				id="account-secret-add-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-expanded={adding}
				aria-controls="account-secret-add"
				onclick={() => (adding ? closeForm() : (adding = true))}>Add secret</button
			>
		{/if}
	</div>
	<p class="lede">Every project gets these. A project's own secret of the same name wins.</p>
	<LoadState
		status={secrets !== undefined ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
		shape="rows"
	>
		{#if adding}
			<form
				id="account-secret-add"
				class="sform"
				aria-label="Add an account secret"
				onsubmit={(e) => {
					e.preventDefault();
					void onAdd();
				}}
			>
				<div class="sfields">
					<div>
						<label for="account-secret-name" class="hk-field-label">Name</label>
						<input
							id="account-secret-name"
							class="hk-input mono name"
							placeholder="API_KEY"
							autocomplete="off"
							autocapitalize="characters"
							spellcheck="false"
							bind:value={name}
							bind:this={nameField}
							use:focusOnMount
							aria-invalid={nameError ? 'true' : undefined}
							aria-describedby={nameError ? 'account-secret-name-error' : undefined}
							oninput={() => (nameError = undefined)}
						/>
						{#if nameError}<p id="account-secret-name-error" class="hk-field-error">
								{nameError}
							</p>{/if}
					</div>
					<div>
						<label for="account-secret-value" class="hk-field-label">Value</label>
						<textarea
							id="account-secret-value"
							rows="2"
							class="hk-textarea mono value"
							autocomplete="off"
							spellcheck="false"
							disabled={!!file}
							bind:value
							aria-invalid={valueError ? 'true' : undefined}
							aria-describedby={valueError ? 'account-secret-value-error' : undefined}
							oninput={() => (valueError = undefined)}
						></textarea>
						<label for="account-secret-file" class="hk-sr">Value from a file</label>
						<input
							id="account-secret-file"
							type="file"
							tabindex="-1"
							class="hk-sr"
							bind:this={fileInput}
							onchange={pickFile}
						/>
						<div class="sfile">
							{#if file}
								<span class="sfile-name" data-testid="account-secret-file-name">{file.name}</span>
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
						{#if valueError}<p id="account-secret-value-error" class="hk-field-error">
								{valueError}
							</p>{/if}
					</div>
				</div>
				<div class="sbuttons">
					<button
						type="submit"
						class="hk-btn-secondary"
						aria-disabled={saving ? 'true' : undefined}
					>
						{saving ? 'Storing…' : 'Add'}
					</button>
					<button type="button" class="hk-btn-ghost" onclick={closeForm}>Cancel</button>
				</div>
			</form>
		{/if}
		{#if secrets && secrets.length === 0}
			<div class="hk-empty-row">
				<h3 class="hk-empty-title">No account secrets.</h3>
			</div>
		{:else if secrets}
			<table class="hk-table list" data-testid="account-secrets" aria-label="Account secrets">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col" class="c-updated">Updated</th>
						<th scope="col" class="c-act"><span class="hk-sr">Delete</span></th>
					</tr>
				</thead>
				<tbody>
					{#each secrets as s (s.name)}
						<tr class:open={confirming === s.name}>
							<td class="c-name"
								><span>{@render secretName(s.name)}</span><span class="when-phone"
									>{dateTime(s.updated_at)}</span
								></td
							>
							<td class="c-updated">{dateTime(s.updated_at)}</td>
							<td class="c-act">
								{#if confirming !== s.name}
									<button
										type="button"
										id={`account-delete-${s.name}`}
										class="hk-btn-ghost hk-btn-sm hk-btn-ghost--touch"
										aria-label={`Delete ${s.name}…`}
										onclick={() => (confirming = s.name)}>Delete…</button
									>
								{/if}
							</td>
						</tr>
						{#if confirming === s.name}
							<tr class="confirm-row">
								<td colspan="3">
									<div class="hk-confirm" data-testid="confirm-delete-account-secret">
										<p>
											Delete <code>{@render secretName(s.name)}</code> from every project? A project with
											its own keeps that one.
										</p>
										<div class="hk-confirm-actions">
											<button
												type="button"
												class="hk-btn-danger hk-btn-sm"
												aria-label={`Delete ${s.name} now`}
												aria-disabled={deleting === s.name ? 'true' : undefined}
												onclick={() => onDelete(s.name)}
												>{deleting === s.name ? 'Deleting…' : 'Delete'}</button
											>
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
	.lede {
		margin: -0.25rem 0 1.25rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	/* The add form: one panel under the head, the fields side by side
	   from a 640px column, as on a project's Config tab. */
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
	.list {
		border-bottom: 1px solid var(--hk-hairline);
	}
	.c-name {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		font-weight: 500;
		overflow-wrap: anywhere;
	}
	.c-updated {
		width: 1%;
		white-space: nowrap;
		color: var(--hk-text-3);
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
	}
	.c-act {
		width: 1%;
		text-align: right;
		white-space: nowrap;
	}
	.list td.c-act {
		padding-block: 0.375rem;
	}
	.c-act button {
		margin-right: -0.625rem;
	}
	.list td {
		vertical-align: middle;
	}
	tr.open td {
		border-bottom: 0;
	}
	.confirm-row td {
		padding-top: 0;
		border-top: 0;
	}
	.hk-confirm code {
		overflow-wrap: anywhere;
	}
	.when-phone {
		display: none;
	}
	@media (max-width: 639.98px) {
		.list thead .c-updated,
		.list td.c-updated {
			display: none;
		}
		.when-phone {
			display: block;
			margin-top: 0.125rem;
			font-family: var(--hk-font-sans);
			font-size: 0.75rem;
			font-weight: 400;
			color: var(--hk-text-3);
			font-variant-numeric: tabular-nums;
		}
	}
	.mono {
		font-family: var(--hk-font-mono);
	}
	.name {
		text-transform: uppercase;
	}
	.name::placeholder {
		text-transform: none;
	}
	.value {
		display: block;
		min-height: 0;
		resize: vertical;
	}
	.sfile {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.75rem;
		margin-top: 0.5rem;
		min-height: 1.875rem;
		font-size: 0.8125rem;
	}
	.sfile-name {
		min-width: 0;
		font-family: var(--hk-font-mono);
		overflow-wrap: anywhere;
		color: var(--hk-text);
	}
	.sfile-size {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	/* A ghost button's text on the field's left edge. */
	.sfile-load {
		margin-left: -0.625rem;
	}
	@media (forced-colors: active) {
		.c-act button,
		.sfile-load {
			margin-inline: 0;
		}
	}
</style>
