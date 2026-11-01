<!--
  The one place a secret's value is typed or pasted on the dashboard
  (DECISIONS I-929): a modal over a blurred page, so nothing else on it
  competes with the paste. Two uses:

  - ask: an agent asked for a named secret with `repose-ask --secret`.
    The name is fixed and the agent's reason is shown; Save answers the
    question with the value, which the api stores as the project's secret.
  - add: "Add secret" on a project's Config tab or under Settings, with a
    name field.

  The value is masked while it is typed, and its size shows under the field
  so a paste can be checked without showing it. One trailing newline from a
  copy is dropped, as `repose secrets set` drops it from a pipe; a loaded
  file is sent as its bytes. The value lives in this component only while
  it is open and is cleared on close.
-->
<script lang="ts">
	import { tick } from 'svelte';
	import { encodeBase64 } from '$lib/base64';

	const NAME_RE = /^[A-Z][A-Z0-9_]{0,63}$/;
	const SHELL = new Set(['BASH_ENV', 'ENV', 'REPOSE_ENV_GEN']);
	const MAX_BYTES = 64 * 1024;

	let {
		open = false,
		mode,
		name: fixedName = '',
		agent = '',
		reason = '',
		target,
		existing,
		fileTestId = 'secret-file-name',
		onsave,
		onclose
	}: {
		open?: boolean;
		mode: 'ask' | 'add';
		/** The secret an agent asks for (ask). */
		name?: string;
		/** Who asks (ask). */
		agent?: string;
		/** The agent's words: what the secret is for and where to get it (ask). */
		reason?: string;
		/** Where it lands: a project's name, or undefined for the account. */
		target?: string;
		/** What setting this name would replace, or a word on what it overrides. */
		existing?: (name: string) => string | undefined;
		/** The picked file's name, for tests. */
		fileTestId?: string;
		/** Stores it; resolves with an error to show under the field, or nothing on success. */
		onsave: (name: string, base64: string) => Promise<string | undefined>;
		onclose: () => void;
	} = $props();

	let dialog = $state<HTMLDialogElement | undefined>(undefined);
	let valueField = $state<HTMLTextAreaElement | undefined>(undefined);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let typedName = $state('');
	let value = $state('');
	let file = $state<File | undefined>(undefined);
	let shown = $state(false);
	let saving = $state(false);
	let nameError = $state<string | undefined>(undefined);
	let valueError = $state<string | undefined>(undefined);

	// Chromium and Safari mask a textarea; elsewhere the value shows, and
	// there is nothing to toggle.
	const canMask = typeof CSS !== 'undefined' && CSS.supports('-webkit-text-security', 'disc');

	let name = $derived(mode === 'ask' ? fixedName : typedName.trim().toUpperCase());
	let sent = $derived(value.replace(/\r?\n$/, ''));
	let bytes = $derived(file ? file.size : new Blob([sent]).size);
	let lines = $derived(file ? 0 : sent === '' ? 0 : sent.split('\n').length);
	let replaces = $derived(name && NAME_RE.test(name) ? existing?.(name) : undefined);
	let where = $derived(target ? `${target}’s secret` : 'your account’s secret');
	let titleId = `secret-dialog-title-${Math.random().toString(36).slice(2, 8)}`;

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			dialog.showModal();
			void tick().then(() => (mode === 'ask' ? valueField : nameField)?.focus());
		} else if (!open && dialog.open) {
			dialog.close();
		}
	});

	function reset() {
		typedName = '';
		value = '';
		file = undefined;
		if (fileInput) fileInput.value = '';
		shown = false;
		nameError = undefined;
		valueError = undefined;
		saving = false;
	}

	function close() {
		reset();
		onclose();
	}

	function size(n: number): string {
		return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KB`;
	}

	function checkName(n: string): string | undefined {
		if (n === '') return 'Name is required.';
		if (SHELL.has(n)) return `${n} is taken: the machine uses it to load each command’s secrets.`;
		if (!NAME_RE.test(n))
			return 'Use A-Z, 0-9 and _, starting with a letter, at most 64 characters.';
		return undefined;
	}

	async function save() {
		if (saving) return;
		nameError = mode === 'add' ? checkName(name) : undefined;
		valueError = undefined;
		if (!file && sent === '') valueError = 'Paste the value.';
		else if (bytes > MAX_BYTES) valueError = 'Secret values are limited to 64 KB.';
		if (nameError) nameField?.focus();
		else if (valueError) valueField?.focus();
		if (nameError || valueError) return;
		saving = true;
		const base64 = file ? encodeBase64(await file.arrayBuffer()) : encodeBase64(sent);
		const err = await onsave(name, base64);
		saving = false;
		if (err) {
			valueError = err;
			valueField?.focus();
			return;
		}
		reset();
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing) {
			e.preventDefault();
			void save();
		}
	}

	function pickFile() {
		file = fileInput?.files?.[0];
		valueError = undefined;
	}
	function clearFile() {
		file = undefined;
		if (fileInput) fileInput.value = '';
		void tick().then(() => valueField?.focus());
	}
</script>

<dialog
	bind:this={dialog}
	class="hk-modal sdlg"
	aria-labelledby={titleId}
	data-testid="secret-dialog"
	oncancel={(e) => {
		e.preventDefault();
		close();
	}}
	onclick={(e) => {
		// A click on the blurred page around the box closes it.
		if (e.target === dialog) close();
	}}
>
	{#if open}
		<form
			method="dialog"
			onsubmit={(e) => {
				e.preventDefault();
				void save();
			}}
		>
			<div class="sdlg-head">
				{#if mode === 'ask'}
					<p class="sdlg-eyebrow">
						<span class="sdlg-agent">{agent}</span> asks for a secret{target ? ` on ${target}` : ''}
					</p>
					<h2 id={titleId} class="sdlg-name">{name}</h2>
				{:else}
					<h2 id={titleId} class="hk-modal-title">Add a secret</h2>
				{/if}
			</div>

			<div class="sdlg-body">
				{#if mode === 'ask' && reason}
					<p class="sdlg-reason">{reason}</p>
				{/if}

				{#if mode === 'add'}
					<label for="sdlg-name" class="hk-field-label">Name</label>
					<input
						id="sdlg-name"
						class="hk-input sdlg-name-input"
						placeholder="API_KEY"
						autocomplete="off"
						autocapitalize="characters"
						spellcheck="false"
						bind:value={typedName}
						bind:this={nameField}
						aria-invalid={nameError ? 'true' : undefined}
						aria-describedby={nameError ? 'sdlg-name-error' : undefined}
						oninput={() => (nameError = undefined)}
					/>
					{#if nameError}<p id="sdlg-name-error" class="hk-field-error">{nameError}</p>{/if}
				{/if}

				<div class="sdlg-value-head">
					<label for="sdlg-value" class="hk-field-label">Value</label>
					<span class="sdlg-tools">
						{#if canMask && !file}
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								aria-pressed={shown}
								aria-controls="sdlg-value"
								onclick={() => (shown = !shown)}>{shown ? 'Hide' : 'Show'}</button
							>
						{/if}
						<label for="sdlg-file" class="hk-sr">Value from a file</label>
						<input
							id="sdlg-file"
							type="file"
							tabindex="-1"
							class="hk-sr"
							bind:this={fileInput}
							onchange={pickFile}
						/>
						{#if !file}
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								onclick={() => fileInput?.click()}>Load from file</button
							>
						{/if}
					</span>
				</div>
				<textarea
					id="sdlg-value"
					class="hk-textarea sdlg-value"
					class:sdlg-value--masked={canMask && !shown}
					disabled={!!file}
					placeholder={file ? `From ${file.name}` : 'Paste the value'}
					rows="4"
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					data-1p-ignore
					data-lpignore="true"
					bind:value
					bind:this={valueField}
					{onkeydown}
					aria-invalid={valueError ? 'true' : undefined}
					aria-describedby={`sdlg-value-meta${valueError ? ' sdlg-value-error' : ''}`}
					aria-keyshortcuts="Control+Enter Meta+Enter"
					oninput={() => (valueError = undefined)}
				></textarea>
				{#if file}
					<div class="sdlg-file">
						<span class="sdlg-file-name" data-testid={fileTestId}>{file.name}</span>
						<span class="sdlg-meta">{size(file.size)}</span>
						<button type="button" class="hk-btn-ghost hk-btn-sm sdlg-file-clear" onclick={clearFile}
							>Clear</button
						>
					</div>
				{/if}
				{#if valueError}<p id="sdlg-value-error" class="hk-field-error">{valueError}</p>{/if}
				<p id="sdlg-value-meta" class="sdlg-meta sdlg-count" aria-live="polite">
					{#if !file && sent !== ''}{size(bytes)}{lines > 1 ? `, ${lines} lines` : ''}{/if}
				</p>

				<p class="sdlg-where">
					Stored as {where}
					<span class="sdlg-where-name">{name || 'NAME'}</span>.
					{target
						? 'Every command on the machine can read it.'
						: 'Every project gets it, unless it sets its own.'}
					{#if replaces}<span data-testid="secret-replaces">{replaces}</span>{/if}
				</p>
			</div>

			<div class="hk-modal-foot sdlg-foot">
				<button type="button" class="hk-btn-ghost" onclick={close}>Cancel</button>
				<button type="submit" class="hk-btn" disabled={saving} data-testid="secret-save"
					>{saving ? 'Saving…' : mode === 'ask' ? `Save ${name}` : 'Add'}</button
				>
			</div>
		</form>
	{/if}
</dialog>

<style>
	/* A heavier blur than the kit's modal: the page behind is out of reach
	   and out of mind while the value is pasted. */
	.sdlg::backdrop {
		background: color-mix(in srgb, var(--hk-scrim) 80%, transparent);
		backdrop-filter: blur(14px) saturate(0.8);
		-webkit-backdrop-filter: blur(14px) saturate(0.8);
	}
	.sdlg {
		max-width: 30rem;
	}
	.sdlg-head {
		padding: 1.25rem 1.25rem 0;
	}
	.sdlg-eyebrow {
		margin: 0 0 0.25rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.sdlg-agent {
		font-family: var(--hk-font-mono);
		color: var(--hk-text-2);
	}
	.sdlg-name {
		margin: 0;
		font-family: var(--hk-font-mono);
		font-size: 1.125rem;
		font-weight: 500;
		line-height: 1.75rem;
		overflow-wrap: anywhere;
	}
	.sdlg-body {
		display: flex;
		flex-direction: column;
		padding: 0.875rem 1.25rem 1.25rem;
	}
	.sdlg-reason {
		margin: 0 0 1rem;
		font-size: 0.875rem;
		line-height: 1.5;
		color: var(--hk-text-2);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.sdlg-name-input {
		font-family: var(--hk-font-mono);
		text-transform: uppercase;
	}
	.sdlg-name-input::placeholder {
		text-transform: none;
	}
	.sdlg-name-input + .sdlg-value-head,
	.hk-field-error + .sdlg-value-head {
		margin-top: 1rem;
	}
	/* The label and its two ghost tools on one line, the tools flush right
	   so their text meets the field's edge. */
	.sdlg-value-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		min-height: 1.875rem;
		margin-bottom: 0.25rem;
	}
	.sdlg-value-head .hk-field-label {
		margin: 0;
	}
	.sdlg-tools {
		display: flex;
		gap: 0.25rem;
		margin-right: -0.625rem;
	}
	.sdlg-value {
		min-height: 6rem;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		resize: vertical;
		overflow-wrap: anywhere;
	}
	.sdlg-value--masked {
		-webkit-text-security: disc;
	}
	.sdlg-file {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		min-height: 1.875rem;
		margin-top: 0.375rem;
	}
	.sdlg-file-name {
		min-width: 0;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		word-break: break-all;
	}
	.sdlg-file-clear {
		margin-left: auto;
		margin-right: -0.625rem;
	}
	.sdlg-meta {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.sdlg-count {
		min-height: 1.125rem;
		margin: 0.375rem 0 0;
		text-align: right;
	}
	.sdlg-where {
		margin: 0.25rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.5;
		color: var(--hk-text-3);
	}
	.sdlg-where-name {
		font-family: var(--hk-font-mono);
		color: var(--hk-text-2);
	}
	@media (forced-colors: active) {
		.sdlg-tools {
			margin-right: 0;
		}
	}
</style>
