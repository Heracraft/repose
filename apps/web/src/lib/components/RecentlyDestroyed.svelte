<!-- "Recently destroyed" on /projects (DECISIONS I-167): every destroyed
     project that still has a snapshot, when that snapshot goes, and a
     Restore that brings it back as a new project. The CLI's `repose
     projects --destroyed` and `repose restore NAME` are the same list and
     the same route. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { restoreProject } from '$lib/api/client';
	import { ApiError, projectLimitText } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { dateTime, gb, relativeTime } from '$lib/format';
	import {
		DESTROYED_FIRST,
		DESTROYED_STEP,
		defaultRestoreName,
		moreToShow,
		timeLeft
	} from '$lib/destroyed';
	import type { DestroyedProject } from '$lib/api/types';
	import RestoreNameForm from '$lib/components/RestoreNameForm.svelte';
	import { focusAfterRender } from '$lib/focus';

	let {
		destroyed,
		hasMore = false,
		onmore,
		liveSlugs,
		onrestored
	}: {
		destroyed: DestroyedProject[];
		/** The api has rows past `destroyed`; onmore fetches the next page (I-420). */
		hasMore?: boolean;
		onmore?: () => Promise<void>;
		liveSlugs: string[];
		onrestored?: () => void;
	} = $props();

	// The row whose name field is open, the name in it, and why the api
	// refused it last time.
	let openFor = $state<string | undefined>(undefined);
	let name = $state('');
	let nameError = $state<string | undefined>(undefined);
	let busy = $state(false);

	// The newest first, then more on request (DECISIONS I-333). The count
	// survives the list's refreshes, so a poll never folds it back.
	let shown = $state(DESTROYED_FIRST);
	let visible = $derived(destroyed.slice(0, shown));
	let more = $derived(moreToShow(shown, destroyed.length));
	let loadingMore = $state(false);

	async function showMore() {
		if (more < DESTROYED_STEP && hasMore && onmore) {
			loadingMore = true;
			try {
				await onmore();
			} catch (err) {
				toastApiError(err, 'Could not load more destroyed projects.');
				return;
			} finally {
				loadingMore = false;
			}
		}
		shown += DESTROYED_STEP;
	}

	function open(d: DestroyedProject) {
		openFor = d.id;
		name = defaultRestoreName(d, liveSlugs);
		nameError = undefined;
	}

	/** Close the name field and give focus back to the row's Restore button. */
	function close(d: DestroyedProject) {
		openFor = undefined;
		void focusAfterRender(`restore-open-${d.id}`);
	}

	function mb(bytes: number): string {
		if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
		if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
		return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
	}

	async function restore(d: DestroyedProject, restoreName: string) {
		busy = true;
		nameError = undefined;
		try {
			const res = await restoreProject({ project_id: d.id, name: restoreName });
			toast.success(
				`Restoring ${res.name} from its snapshot of ${dateTime(res.snapshot_created_at)}.`
			);
			openFor = undefined;
			onrestored?.();
			await goto(resolve('/projects/[id]', { id: res.project_id }));
		} catch (err) {
			if (
				err instanceof ApiError &&
				err.code === 'conflict' &&
				err.detail?.reason === 'name_taken'
			) {
				nameError = `A project called ${String(err.detail?.name ?? name)} already exists; pick another name.`;
			} else if (err instanceof ApiError && err.code === 'invalid' && !projectLimitText(err)) {
				nameError = err.message;
			} else {
				toastApiError(err, 'Could not restore the project.');
			}
		} finally {
			busy = false;
		}
	}
</script>

{#if destroyed.length > 0}
	<section class="mt-14" aria-labelledby="recently-destroyed">
		<h2 id="recently-destroyed" class="text-xl font-semibold">Recently destroyed</h2>
		<p class="mt-1 text-sm text-ink-muted">Each keeps its last snapshot for 30 days.</p>
		<!-- The projects table's style: the same heads, rules and 16px inset.
		     One tbody per project, so the name field a Restore opens sits in
		     the project's own group, on a row of its own under it. -->
		<table class="table dtable mt-4">
			<thead>
				<tr>
					<th>Project</th>
					<th class="hidden md:table-cell">Size</th>
					<th class="hidden sm:table-cell">Destroyed</th>
					<th class="hidden lg:table-cell">Snapshot</th>
					<th class="hidden sm:table-cell">Restore</th>
					<th><span class="sr-only">Actions</span></th>
				</tr>
			</thead>
			{#each visible as d (d.id)}
				<tbody data-testid="destroyed-row">
					<tr class:dopen={openFor === d.id}>
						<td>
							<span class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
								<span class="font-medium">{d.name}</span>
								{#if !d.name_free}
									<span class="badge">name in use</span>
								{/if}
							</span>
							<!-- Below sm the other columns are not drawn; their figures
							     go under the name. -->
							<span class="mt-0.5 block text-xs text-ink-muted tabular-nums sm:hidden">
								<span class="font-mono">{d.class}</span> · destroyed {relativeTime(d.destroyed_at)}
								{#if d.restorable_until}
									· restorable until {dateTime(d.restorable_until).slice(0, 10)}
									({timeLeft(d.restorable_until)})
								{/if}
							</span>
						</td>
						<td class="hidden whitespace-nowrap md:table-cell">
							<span class="font-mono text-sm">{d.class}</span>
							<span class="text-ink-muted"> · {gb(d.volume_bytes)}</span>
						</td>
						<td class="hidden whitespace-nowrap text-ink-muted sm:table-cell">
							<time datetime={d.destroyed_at} title={dateTime(d.destroyed_at)}
								>{relativeTime(d.destroyed_at)}</time
							>
						</td>
						<td class="hidden whitespace-nowrap text-ink-muted tabular-nums lg:table-cell">
							{dateTime(d.snapshot.created_at)}
							<span class="mt-0.5 block font-mono text-compact">{mb(d.snapshot.bytes)}</span>
						</td>
						<td class="hidden whitespace-nowrap tabular-nums sm:table-cell">
							{#if d.restorable_until}
								{timeLeft(d.restorable_until)}
								<span class="mt-0.5 block text-xs text-ink-muted"
									>restorable until {dateTime(d.restorable_until).slice(0, 10)}</span
								>
							{/if}
						</td>
						<td class="dcell-action">
							{#if openFor !== d.id}
								<button
									type="button"
									id={`restore-open-${d.id}`}
									class="btn-ghost hit -mr-2"
									disabled={busy}
									onclick={() => open(d)}>Restore…</button
								>
							{/if}
						</td>
					</tr>
					{#if openFor === d.id}
						<tr>
							<td colspan="6" class="dform">
								<RestoreNameForm
									id={d.id}
									bind:value={name}
									error={nameError}
									{busy}
									onsubmit={(n) => void restore(d, n)}
									oncancel={() => close(d)}
								/>
							</td>
						</tr>
					{/if}
				</tbody>
			{/each}
		</table>
		{#if more > 0 || hasMore}
			<div class="mt-3 flex items-baseline gap-3 pl-4">
				<button type="button" class="btn-ghost -ml-2" disabled={loadingMore} onclick={showMore}
					>{loadingMore ? 'Loading…' : hasMore ? 'Show more' : `Show ${more} more`}</button
				>
				<span class="text-sm text-ink-muted tabular-nums"
					>{hasMore
						? `${visible.length} shown`
						: `${visible.length} of ${destroyed.length} shown`}</span
				>
			</div>
		{/if}
	</section>
{/if}

<style>
	.dtable td {
		vertical-align: baseline;
		padding-top: 0.875rem;
		padding-bottom: 0.875rem;
	}
	.dtable :is(th, td):first-child {
		padding-left: 1rem;
	}
	.dtable :is(th, td):last-child {
		padding-right: 1rem;
	}
	/* The row whose name field is open and the field's row read as one. */
	.dtable tr.dopen td {
		border-bottom: 0;
		padding-bottom: 0;
	}
	.dtable td.dform {
		padding-top: 0;
	}
	.dcell-action {
		width: 1px;
		white-space: nowrap;
		text-align: right;
	}
</style>
