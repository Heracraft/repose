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
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { dateTime, relativeTime } from '$lib/format';
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
			} else if (err instanceof ApiError && err.code === 'invalid') {
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
	<section class="mt-16" aria-labelledby="recently-destroyed">
		<h2 id="recently-destroyed" class="text-xl font-semibold">Recently destroyed</h2>
		<p class="mt-1 text-sm text-ink-muted">Each keeps its last snapshot for 30 days.</p>
		<ul class="mt-4 border-t border-rule-strong">
			{#each visible as d (d.id)}
				<li class="row" data-testid="destroyed-row">
					<div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
						<div class="min-w-0">
							<span class="font-medium">{d.name}</span>
							{#if !d.name_free}
								<span class="badge ml-2">name in use</span>
							{/if}
						</div>
						{#if openFor !== d.id}
							<!-- -mx-2 takes back the ghost button's padding, so "Restore…"
							     lines up with the list's edge at the right and, on a phone,
							     under the name when it wraps. -->
							<button
								type="button"
								id={`restore-open-${d.id}`}
								class="btn-ghost -mx-2"
								disabled={busy}
								onclick={() => open(d)}>Restore…</button
							>
						{/if}
					</div>
					<!-- The size is mono text, as in the projects table above: a
					     badge is for a tag like "name in use", not for a value. -->
					<p class="mt-0.5 text-sm text-ink-muted tabular-nums">
						<span class="font-mono text-compact text-ink">{d.class}</span> · destroyed {relativeTime(
							d.destroyed_at
						)} · snapshot {dateTime(d.snapshot.created_at)}, {mb(d.snapshot.bytes)}
						{#if d.restorable_until}
							· restorable until {dateTime(d.restorable_until).slice(0, 10)} ({timeLeft(
								d.restorable_until
							)})
						{/if}
					</p>
					{#if openFor === d.id}
						<RestoreNameForm
							id={d.id}
							bind:value={name}
							error={nameError}
							{busy}
							onsubmit={(n) => void restore(d, n)}
							oncancel={() => close(d)}
						/>
					{/if}
				</li>
			{/each}
		</ul>
		{#if more > 0 || hasMore}
			<div class="mt-3 flex items-baseline gap-3">
				<button type="button" class="btn-ghost px-0" disabled={loadingMore} onclick={showMore}
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
