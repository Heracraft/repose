<!--
  The one place a secret's value is typed or pasted on the dashboard
  (DECISIONS I-929): a modal over a blurred page, so nothing else on it
  competes with the paste. Two uses:

  - ask: an agent asked for a named secret with `repose-ask --secret`.
    The name is fixed and the agent's reason is shown; Save answers the
    question with the value, which the api stores as the project's secret.
  - add: "Add secret" on a project's Config tab or under Settings, with a
    name field.

  The value is a password field, so it is masked everywhere and a screen
  reader does not read it out. A paste of several lines (a PEM key, a JSON
  credential) turns it into a textarea, masked where the browser can mask
  one. Its size shows under it, and a value that starts or ends with a
  space says so, so a paste can be checked without showing it. One
  trailing newline from a copy is dropped, as `repose secrets set` drops
  it from a pipe; a loaded file is sent as its bytes. The value lives here
  only while the dialog is open.
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
		notice,
		saveLabel = 'Save',
		fileTestId = 'secret-file-name',
		onsave,
		onclose
	}: {
		open?: boolean;
		mode: 'ask' | 'add';
		/** The secret an agent asks for (ask). */
		name?: string;
		/** The agent that asks (ask). */
		agent?: string;
		/** The agent's words: what the secret is for and where to get it (ask). */
		reason?: string;
		/** Where it lands: a project's name, or undefined for the account. */
		target?: string;
		/** What saving this name would replace, said under the field; undefined for nothing. */
		existing?: (name: string) => string | undefined;
		/** A line over the field, when the save is not what the dialog first offered. */
		notice?: string;
		saveLabel?: string;
		/** The picked file's name, for tests. */
		fileTestId?: string;
		/** Stores it; resolves with an error to show under the field, or nothing on success. */
		onsave: (name: string, base64: string) => Promise<string | undefined>;
		onclose: () => void;
	} = $props();

	let dialog = $state<HTMLDialogElement | undefined>(undefined);
	let valueField = $state<HTMLInputElement | HTMLTextAreaElement | undefined>(undefined);
	let nameField = $state<HTMLInputElement | undefined>(undefined);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let typedName = $state('');
	let value = $state('');
	let multiline = $state(false);
	let file = $state<File | undefined>(undefined);
	let shown = $state(false);
	let saving = $state(false);
	let nameError = $state<string | undefined>(undefined);
	let valueError = $state<string | undefined>(undefined);

	// Chromium and Safari mask a textarea; elsewhere one of several lines
	// shows, which the toggle cannot change.
	const canMaskArea = typeof CSS !== 'undefined' && CSS.supports('-webkit-text-security', 'disc');

	let name = $derived(mode === 'ask' ? fixedName : typedName.trim().toUpperCase());
	let sent = $derived(value.replace(/\r?\n$/, ''));
	let bytes = $derived(file ? file.size : new Blob([sent]).size);
	let lines = $derived(file || sent === '' ? 0 : sent.split('\n').length);
	let edges = $derived(
		sent !== '' && /^\s/.test(sent)
			? /\s$/.test(sent)
				? 'starts and ends with a space'
				: 'starts with a space'
			: sent !== '' && /\s$/.test(sent)
				? 'ends with a space'
				: ''
	);
	let replaces = $derived(name && NAME_RE.test(name) ? existing?.(name) : undefined);
	let canToggle = $derived(!file && (!multiline || canMaskArea));
	const uid = Math.random().toString(36).slice(2, 8);
	const ids = {
		title: `sdlg-title-${uid}`,
		about: `sdlg-about-${uid}`,
		value: `sdlg-value-${uid}`,
		name: `sdlg-name-${uid}`,
		nameError: `sdlg-name-error-${uid}`,
		status: `sdlg-status-${uid}`,
		file: `sdlg-file-${uid}`
	};

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			dialog.showModal();
			void tick().then(() => (mode === 'ask' ? valueField : nameField)?.focus());
		} else if (!open && dialog.open) {
			// Closed by its owner after a save: nothing pasted stays here.
			dialog.close();
			reset();
		}
	});

	function reset() {
		typedName = '';
		value = '';
		multiline = false;
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
		}
	}

	// A password field keeps one line of a paste; a paste of several goes
	// to the textarea whole, the caret at its end.
	async function onpaste(e: ClipboardEvent) {
		if (multiline) return;
		const text = e.clipboardData?.getData('text/plain') ?? '';
		if (!/\r|\n/.test(text.replace(/\r?\n$/, ''))) return;
		e.preventDefault();
		value = text;
		multiline = true;
		valueError = undefined;
		await tick();
		valueField?.focus();
		if (valueField) valueField.selectionStart = valueField.selectionEnd = value.length;
	}

	function onareakeydown(e: KeyboardEvent) {
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

<!-- A name breaks after an underscore before anywhere else. -->
{#snippet secretName(n: string)}{#each n.split('_') as part, i (i)}{#if i > 0}_<wbr
			/>{/if}{part}{/each}{/snippet}

<dialog
	bind:this={dialog}
	class="hk-modal sdlg"
	aria-labelledby={ids.title}
	aria-describedby={mode === 'ask' ? ids.about : undefined}
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
					<h2 id={ids.title} class="sdlg-name">{@render secretName(name)}</h2>
				{:else}
					<h2 id={ids.title} class="hk-modal-title">Add a secret</h2>
				{/if}
			</div>

			<div class="sdlg-body">
				{#if mode === 'ask'}
					<div id={ids.about}>
						<p class="sdlg-who">
							<span class="sdlg-agent">{agent}</span> asks for it on {target}
						</p>
						{#if reason}<p class="sdlg-reason">{reason}</p>{/if}
					</div>
				{/if}
				{#if notice}<p class="sdlg-notice" role="status">{notice}</p>{/if}

				{#if mode === 'add'}
					<label for={ids.name} class="hk-field-label">Name</label>
					<input
						id={ids.name}
						class="hk-input sdlg-name-input"
						placeholder="API_KEY"
						autocomplete="off"
						autocapitalize="characters"
						spellcheck="false"
						bind:value={typedName}
						bind:this={nameField}
						aria-invalid={nameError ? 'true' : undefined}
						aria-describedby={nameError ? ids.nameError : undefined}
						oninput={() => (nameError = undefined)}
					/>
					{#if nameError}<p id={ids.nameError} class="hk-field-error">{nameError}</p>{/if}
				{/if}

				<div class="sdlg-value-head" class:sdlg-value-head--after={mode === 'add'}>
					<label for={ids.value} class="hk-field-label">Value</label>
					<span class="sdlg-tools">
						{#if canToggle}
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								aria-pressed={shown}
								aria-controls={ids.value}
								onclick={() => (shown = !shown)}>{shown ? 'Hide' : 'Show'}</button
							>
						{/if}
						<label for={ids.file} class="hk-sr">Value from a file</label>
						<input
							id={ids.file}
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

				{#if file}
					<output id={ids.value} class="sdlg-file" aria-describedby={ids.status}>
						<span class="sdlg-file-name" data-testid={fileTestId}>{file.name}</span>
						<span class="sdlg-meta sdlg-file-size">{size(file.size)}</span>
						<button type="button" class="hk-btn-ghost hk-btn-sm sdlg-file-clear" onclick={clearFile}
							>Clear</button
						>
					</output>
				{:else if multiline}
					<textarea
						id={ids.value}
						class="hk-textarea sdlg-value sdlg-value--area"
						class:sdlg-value--masked={canMaskArea && !shown}
						rows="5"
						autocomplete="off"
						autocapitalize="off"
						spellcheck="false"
						data-1p-ignore
						data-lpignore="true"
						bind:value
						bind:this={valueField}
						onkeydown={onareakeydown}
						aria-invalid={valueError ? 'true' : undefined}
						aria-describedby={ids.status}
						aria-keyshortcuts="Control+Enter Meta+Enter"
						oninput={() => (valueError = undefined)}
					></textarea>
				{:else}
					<input
						id={ids.value}
						class="hk-input sdlg-value"
						type={shown ? 'text' : 'password'}
						placeholder="Paste the value"
						autocomplete="off"
						autocapitalize="off"
						spellcheck="false"
						data-1p-ignore
						data-lpignore="true"
						bind:value
						bind:this={valueField}
						{onpaste}
						aria-invalid={valueError ? 'true' : undefined}
						aria-describedby={ids.status}
						oninput={() => (valueError = undefined)}
					/>
				{/if}

				<!-- One line under the field: the error, else what the value is. -->
				<p
					id={ids.status}
					class="sdlg-status"
					class:hk-field-error={!!valueError}
					aria-live="polite"
				>
					{#if valueError}{valueError}{:else if !file && sent !== ''}{size(bytes)}{lines > 1
							? `, ${lines} lines`
							: ''}{edges ? `, ${edges}` : ''}{/if}
				</p>

				{#if replaces}
					<p class="sdlg-replaces" data-testid="secret-replaces">{replaces}</p>
				{:else if mode === 'add' && !target}
					<p class="sdlg-where">Every project gets it, unless it sets its own.</p>
				{/if}
			</div>

			<div class="hk-modal-foot">
				<button type="button" class="hk-btn-ghost" onclick={close}>Cancel</button>
				<button type="submit" class="hk-btn" disabled={saving} data-testid="secret-save"
					>{saving ? 'Saving…' : saveLabel}</button
				>
			</div>
		</form>
	{/if}
</dialog>

<style>
	/* A heavier blur than the kit's modal: the page behind is out of reach
	   while the value is pasted. */
	.sdlg::backdrop {
		background: var(--hk-scrim);
		backdrop-filter: blur(12px);
		-webkit-backdrop-filter: blur(12px);
	}
	.sdlg {
		max-width: 30rem;
	}
	.sdlg-head {
		padding: 1.25rem 1.25rem 0;
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
		padding: 0.5rem 1.25rem 1.25rem;
	}
	.sdlg-who {
		margin: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.sdlg-agent {
		font-family: var(--hk-font-mono);
		color: var(--hk-text-2);
	}
	.sdlg-reason {
		margin: 0.75rem 0 0;
		font-size: 0.875rem;
		line-height: 1.5;
		color: var(--hk-text-2);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.sdlg-notice {
		margin: 0.75rem 0 0;
		font-size: 0.875rem;
		line-height: 1.5;
		color: var(--hk-text);
	}
	.sdlg-name-input {
		font-family: var(--hk-font-mono);
		text-transform: uppercase;
	}
	.sdlg-name-input::placeholder {
		text-transform: none;
	}
	/* The label and its two ghost tools on one line, the tools inside the
	   box so their hover and focus stay clear of the field. */
	.sdlg-value-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		min-height: 1.875rem;
		margin: 1.25rem 0 0.5rem;
	}
	.sdlg-value-head--after {
		margin-top: 1rem;
	}
	.sdlg-value-head .hk-field-label {
		margin: 0;
	}
	.sdlg-tools {
		display: flex;
		gap: 0.25rem;
	}
	.sdlg-tools :global(.hk-btn-ghost:focus-visible) {
		outline-offset: 0;
	}
	.sdlg-value {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	.sdlg-value--area {
		min-height: 7rem;
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
		min-height: 2.5rem;
		padding: 0.25rem 0.25rem 0.25rem 0.75rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius);
	}
	.sdlg-file-name {
		min-width: 0;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		overflow-wrap: anywhere;
	}
	.sdlg-file-size {
		flex-shrink: 0;
		white-space: nowrap;
	}
	.sdlg-file-clear {
		margin-left: auto;
	}
	.sdlg-meta,
	.sdlg-status:not(.hk-field-error) {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	/* The error takes the count's line, so the box does not grow. */
	.sdlg-status {
		min-height: 1.125rem;
		margin: 0.375rem 0 0;
		line-height: 1.125rem;
	}
	.sdlg-replaces,
	.sdlg-where {
		margin: 0.5rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.5;
	}
	.sdlg-replaces {
		color: var(--hk-text-2);
	}
	.sdlg-where {
		color: var(--hk-text-3);
	}
</style>
