<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { getProject, listSecrets, putSecret, deleteSecret } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { encodeBase64 } from '$lib/base64';
	import { dateTime } from '$lib/format';
	import PageShell from '$lib/components/PageShell.svelte';
	import ProjectTabs from '$lib/components/ProjectTabs.svelte';
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

	let name = $state('');
	let value = $state('');
	let fileInput = $state<HTMLInputElement | undefined>(undefined);
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
			[project, secrets] = await Promise.all([getProject(id), listSecrets(id)]);
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
		if (RESERVED.has(n)) return "This name is reserved for the guest's SSH host material.";
		if (!NAME_RE.test(n))
			return 'Must match [A-Z][A-Z0-9_]{0,63}: uppercase letters, digits and underscores, starting with a letter.';
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
			toastApiError(err, 'Could not remove the secret.');
		} finally {
			deleting = undefined;
		}
	}
</script>

<svelte:head>
	<title>{project ? `${project.name} secrets — repose` : 'Secrets — repose'}</title>
</svelte:head>

<!-- The project's name is a plain crumb: Overview in the tabs is the way
     back to the project, so a second link to it would be a duplicate. -->
<PageShell
	title="Secrets"
	crumbs={[
		{ label: 'Projects', href: resolve('/projects') },
		// "…" while the name loads; after a failed load there is no name
		// coming, so the crumb says what it is.
		{ label: project?.name ?? (loadFailed ? 'Project' : '…') }
	]}
>
	{#snippet tabs()}<ProjectTabs {id} />{/snippet}
	<LoadState
		status={secrets !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
	>
		<!-- The list and the form side by side from lg (I-664): the list
		     takes what the frame has left, the form a 22rem card. -->
		<div class="grid items-start gap-10 lg:grid-cols-[minmax(0,1fr)_22rem] lg:gap-12">
			<div class="min-w-0">
				{#if secrets && secrets.length === 0}
					<!-- The empty state says so (DESIGN-LANGUAGE.md "States"); the form
					     to add one is beside it, so the sentence is where a secret
					     lands, not how to add one (I-485). -->
					<div class="rounded-sm border border-dashed border-rule-strong px-6 py-12 text-center">
						<h2 class="text-xl font-semibold">No secrets yet</h2>
						<p class="mx-auto mt-2 max-w-md text-sm text-ink-muted">
							The machine sees each as an environment variable and as a file at
							<code class="font-mono text-compact whitespace-nowrap text-ink"
								>/run/repose/secrets/NAME</code
							>.
						</p>
					</div>
				{:else if secrets}
					<!-- pt-5 puts this h2 level with the card's. -->
					<h2 class="text-xl font-semibold lg:pt-5">
						{secrets.length}
						{secrets.length === 1 ? 'secret' : 'secrets'}
					</h2>
					<table class="table mt-3">
						<thead>
							<tr>
								<th scope="col">Name</th>
								<th scope="col" class="w-px whitespace-nowrap">Updated</th>
								<th scope="col" class="w-px"><span class="sr-only">Delete</span></th>
							</tr>
						</thead>
						<tbody>
							{#each secrets as s (s.name)}
								<tr>
									<td
										class="font-mono break-all align-middle {confirming === s.name
											? 'border-b-0'
											: ''}">{s.name}</td
									>
									<td
										class="align-middle whitespace-nowrap text-ink-muted tabular-nums {confirming ===
										s.name
											? 'border-b-0'
											: ''}">{dateTime(s.updated_at)}</td
									>
									<td class="text-right align-middle {confirming === s.name ? 'border-b-0' : ''}">
										{#if confirming !== s.name}
											<button
												type="button"
												id={`delete-${s.name}`}
												class="btn-ghost-danger -mr-2"
												aria-label={`Delete ${s.name}`}
												onclick={() => (confirming = s.name)}>Delete</button
											>
										{/if}
									</td>
								</tr>
								{#if confirming === s.name}
									<tr>
										<td colspan="3" class="pt-0" data-testid="confirm-delete-secret">
											<p class="text-ink-muted">
												Remove <span class="font-mono break-all text-ink">{s.name}</span>? Running
												processes that already read it keep their copy until they restart.
											</p>
											<div class="mt-2 flex items-center gap-2">
												<button
													type="button"
													class="btn-danger btn--sm"
													aria-label={`Delete ${s.name} now`}
													disabled={deleting === s.name}
													onclick={() => onDelete(s.name)}
													>{deleting === s.name ? 'Deleting…' : 'Delete'}</button
												>
												<!-- Focus lands on the safe choice when the question opens. -->
												<button
													type="button"
													class="btn-ghost"
													use:focusOnMount
													onclick={() => keep(s.name)}>Keep it</button
												>
											</div>
										</td>
									</tr>
								{/if}
							{/each}
						</tbody>
					</table>
				{/if}
			</div>

			<!-- Each field has a visible label and its error is tied to it with
			     aria-describedby, so a screen reader hears why Add refused. -->
			<form
				class="card"
				onsubmit={(e) => {
					e.preventDefault();
					void onAdd();
				}}
			>
				<h2 class="text-xl font-semibold">Add a secret</h2>
				<div class="mt-4 flex flex-col gap-4">
					<div>
						<label for="secret-name" class="block text-sm font-medium">Name</label>
						<input
							id="secret-name"
							class="field mt-1.5 w-full font-mono uppercase placeholder:normal-case"
							placeholder="API_KEY"
							autocomplete="off"
							autocapitalize="characters"
							spellcheck="false"
							bind:value={name}
							aria-invalid={nameError ? 'true' : undefined}
							aria-describedby={nameError ? 'secret-name-error' : undefined}
							oninput={() => (nameError = undefined)}
						/>
						{#if nameError}<p id="secret-name-error" class="field-error">{nameError}</p>{/if}
					</div>
					<div>
						<label for="secret-value" class="block text-sm font-medium">Value</label>
						<textarea
							id="secret-value"
							rows="3"
							class="field mt-1.5 block min-h-0 w-full resize-y font-mono disabled:cursor-not-allowed disabled:opacity-60"
							autocomplete="off"
							spellcheck="false"
							disabled={!!file}
							bind:value
							aria-invalid={valueError ? 'true' : undefined}
							aria-describedby={valueError ? 'secret-value-error' : undefined}
							oninput={() => (valueError = undefined)}
						></textarea>
						<!-- The native file widget's "Choose File" and its grey box are
						     the browser's, not the house's: a ghost button opens the
						     same picker, and the picked file shows by name. The input
						     keeps a real label for anything that reaches it. -->
						<label for="secret-file" class="sr-only">Value from a file</label>
						<input
							id="secret-file"
							type="file"
							tabindex="-1"
							class="sr-only"
							bind:this={fileInput}
							onchange={pickFile}
						/>
						<div class="mt-2 flex min-h-8 flex-wrap items-center gap-x-3 gap-y-1">
							{#if file}
								<span
									class="min-w-0 font-mono text-compact break-all text-ink"
									data-testid="secret-file-name">{file.name}</span
								>
								<span class="text-xs text-ink-muted tabular-nums">{bytes(file.size)}</span>
								<button type="button" class="btn-ghost -ml-1" onclick={clearFile}>Clear</button>
							{:else}
								<button type="button" class="btn-ghost -ml-2" onclick={() => fileInput?.click()}
									>Load from file</button
								>
							{/if}
						</div>
						{#if valueError}<p id="secret-value-error" class="field-error">{valueError}</p>{/if}
					</div>
				</div>
				<div class="mt-5 border-t border-rule pt-4">
					<button type="submit" class="btn" disabled={saving || !name}>
						{saving ? 'Storing…' : 'Add'}
					</button>
				</div>
			</form>
		</div>
	</LoadState>
</PageShell>
