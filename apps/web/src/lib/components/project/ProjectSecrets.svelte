<!--
  A project's secrets, under Secrets on its Config tab (DECISIONS I-697,
  I-841): the names and when each changed, Delete… asking in its row,
  and "Add secret", which opens SecretDialog over the page (I-929). Under
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
	import { ApiError } from '$lib/api/errors';
	import SecretDialog from '$lib/components/SecretDialog.svelte';
	import { dateTime } from '$lib/format';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { SecretMeta } from '$lib/api/types';

	let { projectId, projectName }: { projectId: string; projectName?: string } = $props();

	let secrets = $state<SecretMeta[] | undefined>(undefined);
	let account = $state<SecretMeta[]>([]);
	let own = $derived(new Set((secrets ?? []).map((s) => s.name)));
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);

	let adding = $state(false);
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

	/** Stores the dialog's secret; an error comes back to show under its field. */
	async function onAdd(name: string, base64: string): Promise<string | undefined> {
		try {
			await putSecret(projectId, name, base64);
			toast.success(`Stored ${name}.`);
			adding = false;
			await load();
			void focusAfterRender('secret-add-open');
			return undefined;
		} catch (err) {
			if (err instanceof ApiError && err.code === 'invalid') return err.message;
			toastApiError(err, 'Could not store the secret.');
			return 'Could not store the secret. Try again.';
		}
	}

	function replaces(n: string): string | undefined {
		const p = projectName ?? 'This project';
		if (own.has(n)) return `Replaces ${p}’s current ${n}.`;
		if (account.some((s) => s.name === n)) return `${p} uses it in place of your account’s ${n}.`;
		return undefined;
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
		<!-- The head is the count, as on Snapshots and Events: "3 secrets". -->
		<h2 id="secrets-title" class="hk-h2-app">
			{secrets && secrets.length > 0
				? `${secrets.length} ${secrets.length === 1 ? 'secret' : 'secrets'}`
				: 'Secrets'}
		</h2>
		{#if secrets}
			<button
				type="button"
				id="secret-add-open"
				class="hk-btn-secondary hk-btn-sm"
				aria-haspopup="dialog"
				onclick={() => (adding = true)}>Add secret</button
			>
		{/if}
	</div>

	<LoadState
		status={secrets !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
		shape="rows"
	>
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

<SecretDialog
	open={adding}
	mode="add"
	target={projectName}
	existing={replaces}
	onsave={onAdd}
	onclose={() => {
		adding = false;
		void focusAfterRender('secret-add-open');
	}}
/>

<style>
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
	/* A sub-list of the section, set apart by space rather than a rule. */
	.acct {
		margin-top: 2.5rem;
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
		.sact .hk-btn-ghost {
			margin-inline: 0;
		}
	}
</style>
