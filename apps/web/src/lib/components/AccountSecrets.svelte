<!--
  The account's secrets, Settings → Secrets (DECISIONS I-697, I-841):
  every project gets them with its own, and a project's own secret of the
  same name wins. The same names, limits, "Add secret" dialog (I-929) and
  two-step delete as a project's Secrets on its Config tab. Values never
  come back from the api, so none is ever in the page.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { listAccountSecrets, putAccountSecret, deleteAccountSecret } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError } from '$lib/api/errors';
	import SecretDialog from '$lib/components/SecretDialog.svelte';
	import { dateTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { SecretMeta } from '$lib/api/types';

	let secrets = $state<SecretMeta[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let adding = $state(false);
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

	/** Stores the dialog's secret; an error comes back to show under its field. */
	async function onAdd(name: string, base64: string): Promise<string | undefined> {
		try {
			const res = await putAccountSecret(name, base64);
			const own = res?.overridden_by ?? [];
			// The projects that keep their own would otherwise read as
			// having this value.
			toast.success(`Stored ${name} for every project.`, {
				description: own.length
					? `${own.join(', ')} ${own.length === 1 ? 'keeps its' : 'keep their'} own.`
					: undefined
			});
			adding = false;
			await load();
			void focusAfterRender('account-secret-add-open');
			return undefined;
		} catch (err) {
			if (err instanceof ApiError && err.code === 'invalid') return err.message;
			toastApiError(err, 'Could not store the secret.');
			return 'Could not store the secret. Try again.';
		}
	}

	function replaces(n: string): string | undefined {
		return secrets?.some((s) => s.name === n) ? 'It replaces the current value.' : undefined;
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
		<!-- The head is the count, as on Snapshots and Events: "3 secrets". -->
		<h2 id="secrets-h" class="hk-h2-app">
			{secrets && secrets.length > 0
				? `${secrets.length} ${secrets.length === 1 ? 'secret' : 'secrets'}`
				: 'Secrets'}
		</h2>
		{#if secrets}
			<button
				type="button"
				id="account-secret-add-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-haspopup="dialog"
				onclick={() => (adding = true)}>Add secret</button
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

<SecretDialog
	open={adding}
	mode="add"
	existing={replaces}
	fileTestId="account-secret-file-name"
	onsave={onAdd}
	onclose={() => {
		adding = false;
		void focusAfterRender('account-secret-add-open');
	}}
/>

<style>
	.lede {
		margin: -0.25rem 0 1.25rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
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
	@media (forced-colors: active) {
		.c-act button {
			margin-inline: 0;
		}
	}
</style>
