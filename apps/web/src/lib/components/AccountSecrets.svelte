<script lang="ts">
	// The account's secrets (DECISIONS I-697): every project gets them with
	// its own, and a project's own secret of the same name wins. The same
	// names, limits and two-step delete as a project's Secrets page.
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
	let name = $state('');
	let value = $state('');
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
		if (SHELL.has(n)) return 'This name is reserved: the machine uses it to keep secrets current.';
		if (!NAME_RE.test(n))
			return 'Must match [A-Z][A-Z0-9_]{0,63}: uppercase letters, digits and underscores, starting with a letter.';
		return undefined;
	}

	async function onAdd() {
		name = name.trim().toUpperCase();
		nameError = validateName(name);
		valueError =
			new Blob([value]).size > MAX_BYTES ? 'Secret values are limited to 64 KB.' : undefined;
		if (nameError || valueError) return;
		saving = true;
		try {
			const res = await putAccountSecret(name, encodeBase64(value));
			const own = res?.overridden_by ?? [];
			// The projects that keep their own would otherwise read as
			// having this value.
			toast.success(`Stored ${name} for every project.`, {
				description: own.length
					? `${own.join(', ')} ${own.length === 1 ? 'keeps its' : 'keep their'} own.`
					: undefined
			});
			name = '';
			value = '';
			await load();
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
		deleting = n;
		const names = (secrets ?? []).map((s) => s.name);
		const at = names.indexOf(n);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((m) => `account-delete-${m}`);
		try {
			await deleteAccountSecret(n);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'account-secret-name');
		} catch (err) {
			toastApiError(err, 'Could not remove the secret.');
		} finally {
			deleting = undefined;
		}
	}
</script>

{#snippet secretName(n: string)}{#each n.split('_') as part, i (i)}{#if i > 0}_<wbr
			/>{/if}{part}{/each}{/snippet}

<section class="form-section lg:grid lg:grid-cols-[280px_minmax(0,1fr)] lg:gap-x-12" id="secrets">
	<div>
		<h2 class="text-xl font-semibold">Secrets</h2>
		<p class="mt-1 text-sm text-ink-muted">
			Every project gets these. A project's own secret of the same name wins.
		</p>
	</div>
	<div class="mt-4 min-w-0 max-w-[560px] lg:mt-0">
		<LoadState
			status={secrets !== undefined ? 'ready' : loadError ? 'failed' : 'loading'}
			error={loadError}
			onretry={load}
			shape="rows"
		>
			{#if secrets && secrets.length > 0}
				<table class="table -mt-1.5 mb-5" data-testid="account-secrets">
					<thead>
						<tr>
							<th scope="col">Name</th>
							<th scope="col" class="hidden w-px whitespace-nowrap sm:table-cell">Updated</th>
							<th scope="col" class="w-px"><span class="sr-only">Delete</span></th>
						</tr>
					</thead>
					<tbody>
						{#each secrets as s (s.name)}
							<tr>
								<td
									class="font-mono wrap-anywhere align-middle {confirming === s.name
										? 'border-b-0'
										: ''}"
									><span>{@render secretName(s.name)}</span><span
										class="block font-sans text-xs whitespace-nowrap text-ink-muted tabular-nums sm:hidden"
										>{dateTime(s.updated_at)}</span
									></td
								>
								<td
									class="hidden align-middle whitespace-nowrap text-ink-muted tabular-nums sm:table-cell {confirming ===
									s.name
										? 'border-b-0'
										: ''}">{dateTime(s.updated_at)}</td
								>
								<td class="text-right align-middle {confirming === s.name ? 'border-b-0' : ''}">
									{#if confirming !== s.name}
										<button
											type="button"
											id={`account-delete-${s.name}`}
											class="btn-ghost-danger -mr-2"
											aria-label={`Delete ${s.name}`}
											onclick={() => (confirming = s.name)}>Delete</button
										>
									{/if}
								</td>
							</tr>
							{#if confirming === s.name}
								<tr>
									<td colspan="3" class="pt-0" data-testid="confirm-delete-account-secret">
										<p class="text-ink-muted">
											Remove <span class="font-mono wrap-anywhere text-ink"
												>{@render secretName(s.name)}</span
											> from every project? A project with its own keeps that one.
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
			<form
				class="flex flex-col gap-4"
				onsubmit={(e) => {
					e.preventDefault();
					void onAdd();
				}}
			>
				<div>
					<label for="account-secret-name" class="block text-sm font-medium">Name</label>
					<input
						id="account-secret-name"
						class="field mt-1.5 w-full font-mono uppercase placeholder:normal-case sm:w-80"
						placeholder="COOLIFY_BASE_URL"
						autocomplete="off"
						autocapitalize="characters"
						spellcheck="false"
						bind:value={name}
						aria-invalid={nameError ? 'true' : undefined}
						aria-describedby={nameError ? 'account-secret-name-error' : undefined}
						oninput={() => (nameError = undefined)}
					/>
					{#if nameError}<p id="account-secret-name-error" class="field-error">
							{nameError}
						</p>{/if}
				</div>
				<div>
					<label for="account-secret-value" class="block text-sm font-medium">Value</label>
					<textarea
						id="account-secret-value"
						rows="2"
						class="field mt-1.5 block min-h-0 w-full resize-y font-mono"
						autocomplete="off"
						spellcheck="false"
						bind:value
						aria-invalid={valueError ? 'true' : undefined}
						aria-describedby={valueError ? 'account-secret-value-error' : undefined}
						oninput={() => (valueError = undefined)}
					></textarea>
					{#if valueError}<p id="account-secret-value-error" class="field-error">
							{valueError}
						</p>{/if}
				</div>
				<div>
					<button type="submit" class="btn-quiet" disabled={saving || !name}>
						{saving ? 'Storing…' : 'Add'}
					</button>
				</div>
			</form>
		</LoadState>
	</div>
</section>
