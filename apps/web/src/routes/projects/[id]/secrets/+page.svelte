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

	async function onAdd() {
		nameError = validateName(name);
		valueError = undefined;
		let base64: string;
		if (fileInput?.files?.length) {
			const file = fileInput.files[0];
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
			if (fileInput) fileInput.value = '';
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

<PageShell
	title="Secrets"
	width="form"
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
		status={secrets !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
	>
		{#if secrets && secrets.length === 0}
			<!-- The empty state says so (DESIGN-LANGUAGE.md "States"); the form
			     to add one is right below, so the sentence is where a secret
			     lands, not how to add one (I-485). -->
			<h2 class="text-xl font-semibold">No secrets yet</h2>
			<p class="mt-2 text-sm text-ink-muted">
				The machine sees each as an environment variable and a file in
				<code>/run/repose/secrets/</code>.
			</p>
		{:else if secrets}
			<ul>
				{#each secrets as s (s.name)}
					<li class="row flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
						<span class="min-w-0 font-mono text-sm break-all">{s.name}</span>
						<span class="flex items-center gap-4">
							<span class="text-xs text-ink-muted tabular-nums"
								>updated {dateTime(s.updated_at)}</span
							>
							{#if confirming !== s.name}
								<button
									type="button"
									id={`delete-${s.name}`}
									class="btn-ghost-danger -mr-2"
									aria-label={`Delete ${s.name}`}
									onclick={() => (confirming = s.name)}>Delete</button
								>
							{/if}
						</span>
						{#if confirming === s.name}
							<div class="mt-2 basis-full text-sm" data-testid="confirm-delete-secret">
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
							</div>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}

		<!-- Each field has a visible label and its error is tied to it with
		     aria-describedby, so a screen reader hears why Add refused. The
		     placeholders stay as examples of the shape, not as the labels. -->
		<form
			class="form-section"
			onsubmit={(e) => {
				e.preventDefault();
				void onAdd();
			}}
		>
			<h2 class="text-xl font-semibold">Add a secret</h2>
			<div class="mt-3 flex flex-col gap-4">
				<div>
					<label for="secret-name" class="block text-sm font-medium">Name</label>
					<input
						id="secret-name"
						class="field mt-1.5 w-full font-mono"
						placeholder="NAME"
						autocomplete="off"
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
						class="field mt-1.5 w-full font-mono"
						placeholder="Value"
						autocomplete="off"
						spellcheck="false"
						bind:value
						aria-invalid={valueError ? 'true' : undefined}
						aria-describedby={valueError ? 'secret-value-error' : undefined}
						oninput={() => (valueError = undefined)}
					></textarea>
					<label for="secret-file" class="mt-3 block text-sm text-ink-muted"
						>Or read the value from a file</label
					>
					<!-- The file button drawn as a quiet button, so the one
					     browser-default widget on the dashboard follows the house
					     edge, radius and weight. -->
					<input
						id="secret-file"
						type="file"
						bind:this={fileInput}
						class="mt-1.5 block w-full text-sm text-ink-muted file:mr-3 file:cursor-pointer file:rounded-sm file:border file:border-solid file:border-control file:bg-surface file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-ink"
						aria-invalid={valueError ? 'true' : undefined}
						aria-describedby={valueError ? 'secret-value-error' : undefined}
						onchange={() => (valueError = undefined)}
					/>
					{#if valueError}<p id="secret-value-error" class="field-error">{valueError}</p>{/if}
				</div>
				<div>
					<button type="submit" class="btn" disabled={saving || !name}>
						{saving ? 'Storing…' : 'Add'}
					</button>
				</div>
			</div>
		</form>
	</LoadState>
</PageShell>
