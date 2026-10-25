<script lang="ts" module>
	import { errorText } from '$lib/api/errors';

	/**
	 * The sentence a failed first load shows: errorText, the one toastApiError
	 * uses, so the banner and the toast never disagree about one failure.
	 */
	export function loadErrorText(err: unknown, fallback: string): string {
		return errorText(err, fallback);
	}
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';

	/*
	 * The one wrapper for a page's first load. While it runs the page says
	 * "Loading…"; if it fails the page says why and offers Retry, which
	 * re-runs the same load. Without this a page whose first fetch failed
	 * sat on "Loading…" for good, and the toast that named the error was
	 * gone after a few seconds.
	 *
	 * Only the first load belongs here. A later refresh that fails keeps
	 * the content already on screen and reports through a toast, so the
	 * page never blanks out under someone who is reading it.
	 */
	let {
		status,
		onretry,
		label = 'Loading…',
		error,
		shape = 'content',
		children
	}: {
		status: 'loading' | 'failed' | 'ready';
		onretry: () => void | Promise<void>;
		/** What the page says while the load runs. */
		label?: string;
		/** The reason the load failed, as the api or network put it. */
		error?: string;
		/**
		 * The skeleton drawn while it loads: 'page' adds the title's bar
		 * (the root layout, before PageShell exists), 'content' is what
		 * sits under PageShell's title, and 'rows' is a section's rows.
		 */
		shape?: 'page' | 'content' | 'rows';
		children?: Snippet;
	} = $props();

	let retrying = $state(false);

	async function retry() {
		if (retrying) return;
		retrying = true;
		try {
			await onretry();
		} finally {
			retrying = false;
		}
	}
</script>

{#if status === 'ready'}
	{@render children?.()}
{:else if status === 'failed'}
	<!-- role=alert so a screen reader announces the failure, which arrives
	     after the page has already said it is loading. -->
	<div class="hk-banner hk-banner--err load-failed" role="alert">
		<p>{error ?? 'This page could not be loaded.'}</p>
		<!-- aria-disabled rather than disabled while the retry runs: a
		     disabled button loses focus (the browser moves it to <body>), and
		     the person who pressed Retry with the keyboard would start over
		     from the top of the page. It looks waiting and ignores presses. -->
		<button
			type="button"
			class="hk-btn-secondary hk-btn-sm"
			aria-disabled={retrying ? 'true' : undefined}
			onclick={retry}>{retrying ? 'Retrying…' : 'Retry'}</button
		>
	</div>
{:else}
	<!-- role=status is polite: the word is read once, and the content that
	     replaces it is not interrupted. The eye gets the page's shape: a
	     title bar in a box the height of the h1's line, so the swap moves
	     nothing, then rows of 56px with a hairline under each, in still
	     surface-2 bars. No shimmer: a load is not a state you can act on. -->
	<p class="hk-sr" role="status">{label}</p>
	<div aria-hidden="true" data-testid="skeleton" class="skeleton">
		{#if shape === 'page'}
			<div class="title-box"><span class="hk-skel title-bar"></span></div>
		{/if}
		<div>
			{#each shape === 'rows' ? [0, 1, 2] : [0, 1, 2, 3] as row (row)}
				<div class="hk-skel-row">
					<span class="hk-skel" style="height: 0.625rem; width: 6rem"></span>
					<span class="hk-skel" style="height: 0.75rem; width: 10rem"></span>
				</div>
			{/each}
		</div>
	</div>
{/if}

<style>
	.load-failed {
		justify-content: space-between;
	}
	.load-failed p {
		margin: 0;
	}
	.title-box {
		display: flex;
		align-items: center;
		height: 2rem;
		margin-bottom: 1.75rem;
	}
	@media (min-width: 640px) {
		.title-box {
			height: 2.25rem;
		}
	}
	.title-bar {
		width: 12rem;
		height: 1.25rem;
	}
</style>
