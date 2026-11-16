<!--
  A project's webhook routes, under Webhooks on its Config tab
  (DECISIONS I-1110): the name, the port and path inside the machine,
  whether a delivery may start a stopped machine, and the last delivery.
  Copy puts the URL on the clipboard; Rotate and Remove ask in the row,
  as Delete does under Secrets.

  The URL holds the route's token, so it is shown as the route's name
  rather than printed: a dashboard on a shared screen is a screen, and
  the user who needs the characters copies them.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { listWebhooks, putWebhook, rotateWebhook, deleteWebhook } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { ApiError } from '$lib/api/errors';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { dateTime } from '$lib/format';
	import type { WebhookRoute } from '$lib/api/types';

	let { projectId }: { projectId: string } = $props();

	const NAME_RE = /^[a-z0-9][a-z0-9-]{0,31}$/;
	const TARGET_RE = /^:?([0-9]{1,5})(\/[^\s?#]*)?$/;

	let routes = $state<WebhookRoute[] | undefined>(undefined);
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);

	let adding = $state(false);
	let addName = $state('');
	let addTarget = $state('');
	let addStart = $state(false);
	let addError = $state<string | undefined>(undefined);
	let saving = $state(false);
	/** The route whose Rotate… or Remove… was pressed once. */
	let confirming = $state<{ name: string; what: 'rotate' | 'remove' } | undefined>(undefined);
	let busy = $state<string | undefined>(undefined);

	async function load() {
		try {
			routes = await listWebhooks(projectId);
			loadFailed = false;
		} catch (err) {
			if (routes === undefined) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load the webhook routes.');
			} else {
				toastApiError(err, 'Could not load the webhook routes.');
			}
		}
	}

	onMount(async () => {
		await load();
		if (location.hash === '#webhooks') {
			await tick();
			document.getElementById('webhooks')?.scrollIntoView({ block: 'start' });
		}
	});

	function target(r: WebhookRoute): string {
		return `:${r.port}${r.path === '/' ? '/' : r.path}`;
	}

	/** The status column of `repose webhooks`, in the same words. */
	function lastDelivery(r: WebhookRoute): string {
		const d = r.last_delivery;
		if (!d) return 'Never';
		if (d.outcome === 'delivered' || d.outcome === 'woke') return `${d.status}`;
		return `${d.status} ${d.outcome}`;
	}

	function openAdd() {
		adding = true;
		addName = '';
		addTarget = '';
		addStart = false;
		addError = undefined;
	}

	async function onAdd(event: SubmitEvent) {
		event.preventDefault();
		const name = addName.trim();
		const t = addTarget.trim();
		if (!NAME_RE.test(name)) {
			addError = 'A name is 1 to 32 of a-z, 0-9 and -, starting with a letter or digit.';
			return;
		}
		const m = TARGET_RE.exec(t);
		const port = m ? Number(m[1]) : 0;
		if (!m || port < 1 || port > 65535) {
			addError = 'A target is a port, or a port and a path: 3000/api/webhooks/stripe.';
			return;
		}
		saving = true;
		try {
			await putWebhook(projectId, name, t, addStart);
			toast.success(`Added ${name}.`);
			adding = false;
			await load();
			void focusAfterRender('webhook-add-open');
		} catch (err) {
			if (err instanceof ApiError && err.code === 'invalid') {
				addError = err.message;
			} else {
				toastApiError(err, 'Could not add the route.');
				addError = 'Could not add the route. Try again.';
			}
		} finally {
			saving = false;
		}
	}

	function keep(name: string, what: 'rotate' | 'remove') {
		confirming = undefined;
		void focusAfterRender(`${what}-${name}`);
	}

	async function onRotate(name: string) {
		busy = name;
		try {
			const r = await rotateWebhook(projectId, name);
			confirming = undefined;
			await load();
			await navigator.clipboard.writeText(r.url).catch(() => undefined);
			toast.success(`Rotated ${name}; the new URL is on your clipboard.`);
			void focusAfterRender(`rotate-${name}`);
		} catch (err) {
			toastApiError(err, 'Could not rotate the token.');
		} finally {
			busy = undefined;
		}
	}

	async function onRemove(name: string) {
		busy = name;
		const names = (routes ?? []).map((r) => r.name);
		const at = names.indexOf(name);
		const after = [names[at + 1], names[at - 1]].filter(Boolean).map((n) => `remove-${n}`);
		try {
			await deleteWebhook(projectId, name);
			confirming = undefined;
			await load();
			void focusAfterRender(...after, 'webhook-add-open');
		} catch (err) {
			toastApiError(err, 'Could not remove the route.');
		} finally {
			busy = undefined;
		}
	}
</script>

<section class="hk-section" id="webhooks" aria-labelledby="webhooks-title">
	<div class="hk-section-head">
		<h2 id="webhooks-title" class="hk-h2-app">
			{routes && routes.length > 0
				? `${routes.length} webhook ${routes.length === 1 ? 'route' : 'routes'}`
				: 'Webhooks'}
		</h2>
		{#if routes && !adding}
			<button
				type="button"
				id="webhook-add-open"
				class="hk-btn-secondary hk-btn-sm"
				onclick={openAdd}>Add route</button
			>
		{/if}
	</div>

	<LoadState
		status={routes !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={load}
		error={loadError}
		shape="rows"
	>
		{#if routes && routes.length === 0 && !adding}
			<div class="hk-empty-row wempty">
				<h3 class="hk-empty-title">No webhook routes yet</h3>
				<span>A route is a public URL a provider can POST to, forwarded to a port here.</span>
			</div>
		{:else if routes && routes.length > 0}
			<div class="hk-table-wrap">
				<table class="hk-table wtable">
					<thead>
						<tr>
							<th scope="col">Name</th>
							<th scope="col" class="hidden whitespace-nowrap sm:table-cell">Target</th>
							<th scope="col" class="hidden w-px whitespace-nowrap md:table-cell">Start</th>
							<th scope="col" class="hidden w-px whitespace-nowrap md:table-cell">Last delivery</th>
							<th scope="col" class="w-px"><span class="hk-sr">Actions</span></th>
						</tr>
					</thead>
					<tbody>
						{#each routes as r (r.route_id)}
							<tr>
								<td class="wname"
									><span>{r.name}</span><span class="wsub block sm:hidden">{target(r)}</span></td
								>
								<td class="hk-mono hidden whitespace-nowrap sm:table-cell">{target(r)}</td>
								<td class="hidden whitespace-nowrap md:table-cell"
									>{r.allow_start ? 'On' : 'Off'}</td
								>
								<td class="hidden whitespace-nowrap md:table-cell"
									>{lastDelivery(r)}{#if r.last_delivery}<span class="wwhen"
											>{dateTime(r.last_delivery.at)}</span
										>{/if}</td
								>
								<td class="wact">
									{#if confirming?.name !== r.name}
										<div class="wact-row">
											<CopyButton
												text={r.url}
												label="Copy"
												ariaLabel={`Copy the URL of ${r.name}`}
												size="sm"
											/>
											<button
												type="button"
												id={`rotate-${r.name}`}
												class="hk-btn-ghost hk-btn-sm"
												aria-label={`Rotate the token of ${r.name}`}
												onclick={() => (confirming = { name: r.name, what: 'rotate' })}
												>Rotate…</button
											>
											<button
												type="button"
												id={`remove-${r.name}`}
												class="hk-btn-ghost hk-btn-sm"
												aria-label={`Remove ${r.name}`}
												onclick={() => (confirming = { name: r.name, what: 'remove' })}
												>Remove…</button
											>
										</div>
									{/if}
								</td>
							</tr>
							{#if confirming?.name === r.name}
								<tr class="confirm-row">
									<td colspan="5" data-testid={`confirm-${confirming.what}-webhook`}>
										<div class="hk-confirm">
											{#if confirming.what === 'rotate'}
												<p>
													Rotate <span class="hk-mono">{r.name}</span>? The URL in the provider's
													dashboard stops working at once and answers 410 for 30 days. The new URL
													goes on your clipboard.
												</p>
											{:else}
												<p>
													Remove <span class="hk-mono">{r.name}</span>? Its URL answers 410 for 30
													days, then 404.
												</p>
											{/if}
											<div class="hk-confirm-actions">
												<button
													type="button"
													class={confirming.what === 'remove'
														? 'hk-btn-danger hk-btn-sm'
														: 'hk-btn-secondary hk-btn-sm'}
													disabled={busy === r.name}
													onclick={() =>
														confirming?.what === 'rotate' ? onRotate(r.name) : onRemove(r.name)}
													>{busy === r.name
														? confirming.what === 'rotate'
															? 'Rotating…'
															: 'Removing…'
														: confirming.what === 'rotate'
															? 'Rotate'
															: 'Remove'}</button
												>
												<button
													type="button"
													class="hk-btn-ghost hk-btn-sm"
													use:focusOnMount
													onclick={() => keep(r.name, confirming!.what)}>Keep it</button
												>
											</div>
										</div>
									</td>
								</tr>
							{/if}
						{/each}
					</tbody>
				</table>
			</div>
		{/if}

		{#if adding}
			<form class="wadd" onsubmit={onAdd}>
				<div class="wadd-fields">
					<div class="wadd-field">
						<label for="webhook-name" class="hk-field-label">Name</label>
						<!-- svelte-ignore a11y_autofocus -->
						<input
							id="webhook-name"
							class="hk-input"
							autofocus
							autocomplete="off"
							spellcheck="false"
							placeholder="stripe"
							bind:value={addName}
						/>
					</div>
					<div class="wadd-field">
						<label for="webhook-target" class="hk-field-label">Port and path in the machine</label>
						<input
							id="webhook-target"
							class="hk-input"
							autocomplete="off"
							spellcheck="false"
							placeholder="3000/api/webhooks/stripe"
							bind:value={addTarget}
						/>
					</div>
				</div>
				<label class="wadd-start">
					<input type="checkbox" bind:checked={addStart} />
					<span>Let a delivery start the machine while it is stopped</span>
				</label>
				{#if addError}<p class="hk-field-error">{addError}</p>{/if}
				<div class="hk-confirm-actions">
					<button type="submit" class="hk-btn-primary hk-btn-sm" disabled={saving}
						>{saving ? 'Adding…' : 'Add route'}</button
					>
					<button
						type="button"
						class="hk-btn-ghost hk-btn-sm"
						onclick={() => {
							adding = false;
							void focusAfterRender('webhook-add-open');
						}}>Cancel</button
					>
				</div>
			</form>
		{/if}
	</LoadState>
</section>

<style>
	.wtable td.wname {
		font-family: var(--hk-font-mono);
		overflow-wrap: anywhere;
	}
	/* No display here: `.wsub` carries Tailwind's `block sm:hidden`, and a
	   scoped rule would outrank the breakpoint and show it at every
	   width. `.wwhen` has no utility, so it sets its own. */
	.wsub,
	.wwhen {
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.wwhen {
		display: block;
	}
	.wtable td.wact {
		padding-block: 0.375rem;
	}
	/* The three actions keep one line and the name column takes what is
	   left: at 390 the target wraps under the name instead, which is why
	   it carries no nowrap. The table sits in hk-table-wrap, so a width
	   below 360 scrolls sideways rather than pushing the page. */
	.wact-row {
		display: flex;
		flex-wrap: nowrap;
		align-items: center;
		justify-content: flex-end;
		gap: 0.25rem;
	}
	/* Only the last one hangs into the frame's padding, so a middle
	   button cannot overlap the one after it. */
	.wact-row > :last-child.hk-btn-ghost {
		margin-right: -0.625rem;
	}
	.confirm-row td {
		padding-top: 0;
		border-top: 0;
	}
	.wempty {
		display: block;
	}
	.wadd {
		margin-top: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.wadd-fields {
		display: grid;
		gap: 0.75rem;
		grid-template-columns: 1fr;
	}
	@media (min-width: 640px) {
		.wadd-fields {
			grid-template-columns: minmax(0, 12rem) minmax(0, 1fr);
		}
	}
	.wadd-field {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.wadd-start {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.875rem;
		color: var(--hk-text-2);
	}
	/* Below 640 the row is a block: the name, the target under it, then
	   the actions on their own line. Three buttons and a target do not
	   fit a 390 row side by side, and squeezing them broke the target
	   mid-word and pushed Remove past the frame. */
	@media (max-width: 639.98px) {
		.wtable thead {
			display: none;
		}
		.wtable tbody tr {
			display: block;
			padding-block: 0.625rem;
			border-top: 1px solid var(--hk-hairline);
		}
		.wtable tbody tr:first-child {
			border-top: 0;
		}
		.wtable tbody td {
			padding: 0;
			border-top: 0;
		}
		/* Only these two: the target, Start and Last delivery carry
		   Tailwind's `hidden`, and a `display` here would outrank it and
		   show all three again. */
		.wtable tbody td.wname,
		.wtable tbody td.wact {
			display: block;
		}
		.wtable td.wname {
			overflow-wrap: break-word;
		}
		.wtable td.wact {
			margin-top: 0.5rem;
			padding-block: 0;
		}
		.wact-row {
			justify-content: flex-start;
		}
		.wact-row > :last-child.hk-btn-ghost {
			margin-right: 0;
		}
		.confirm-row td {
			padding-top: 0.5rem;
		}
	}
	@media (forced-colors: active) {
		.wact-row > :last-child.hk-btn-ghost {
			margin-inline: 0;
		}
	}
</style>
