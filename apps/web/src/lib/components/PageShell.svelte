<script lang="ts">
	import type { Snippet } from 'svelte';
	import { inDash } from '$lib/dash';

	interface Crumb {
		label: string;
		href?: string;
	}

	let {
		title,
		crumbs = [],
		width = 'full',
		lede,
		action,
		tabs,
		frozen = false,
		children
	}: {
		title: string;
		crumbs?: Crumb[];
		/** 'full' spans the frame; 'form' holds the content to 42rem, flush left. 'list' is 'full', kept so older callers compile. */
		width?: 'full' | 'form' | 'list';
		/** A line under the title: text, or a snippet for markup (a status line). */
		lede?: string | Snippet;
		action?: Snippet;
		/** Links under the title, on a full-width hairline (ProjectTabs, .hk-tabs): the current one's edge lies on it. */
		tabs?: Snippet;
		/** The page is a frozen machine's: a frost look may draw on the head (lib/hk/frost.css). */
		frozen?: boolean;
		children: Snippet;
	} = $props();

	// A long slug breaks at its separators: a <wbr> after each -, _ and .
	// (overflow-wrap is the last resort). The parts are text nodes, so
	// nothing is parsed as markup.
	let parts = $derived(title.split(/(?<=[-_.])/));

	// In the dashboard's grid the grid is the frame (I-841); a layout may
	// draw this shell for its pages (projects/[id], settings), which then
	// render sections only.
	const dash = inDash();
</script>

<!-- The page's one <main>, on the shared .frame (I-656), so the title
     starts under the logo on every page. The root layout's sign-in
     skeleton uses the same padding, so the swap to the page moves nothing
     (I-664). -->
<main id="main" tabindex="-1" class="page-shell" class:frame={!dash.on}>
	{#if crumbs.length}
		<p class="crumbs">
			{#each crumbs as crumb, i (crumb.label)}
				{#if i > 0}<span aria-hidden="true">·</span>{/if}
				{#if crumb.href}
					<!-- eslint-disable svelte/no-navigation-without-resolve -- callers build crumb.href with $app/paths' resolve() -->
					<a href={crumb.href}>{crumb.label}</a>
					<!-- eslint-enable svelte/no-navigation-without-resolve -->
				{:else}
					<span>{crumb.label}</span>
				{/if}
			{/each}
		</p>
	{/if}
	<div class="head" class:head--tabs={tabs} data-frost-on={frozen ? 'head' : undefined}>
		<div class="title-row">
			<div class="min-w-0">
				<h1 class="hk-h1-app title">
					{#each parts as part, i (i)}{part}{#if i < parts.length - 1}<wbr />{/if}{/each}
				</h1>
				{#if typeof lede === 'string'}<p class="lede">{lede}</p>
				{:else if lede}<div class="lede">{@render lede()}</div>{/if}
			</div>
			{#if action}<div class="action">{@render action()}</div>{/if}
		</div>
		{#if tabs}<div class="tabs">{@render tabs()}</div>{/if}
	</div>
	<div class="body" class:body--form={width === 'form'}>
		{@render children()}
	</div>
</main>

<style>
	.page-shell {
		padding-top: 1.75rem;
		padding-bottom: 6rem;
	}
	@media (min-width: 640px) {
		.page-shell {
			padding-top: 2.25rem;
		}
	}
	.crumbs {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 0.5rem;
		margin: 0 0 0.75rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.crumbs a {
		color: inherit;
		text-decoration: none;
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	.crumbs a:hover {
		color: var(--hk-text);
	}
	/* A thumb's 44px; the negative block margin keeps the line's height. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.crumbs a {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
	.title-row {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	@media (min-width: 640px) {
		.title-row {
			flex-direction: row;
			align-items: baseline;
			justify-content: space-between;
			gap: 1rem;
		}
	}
	.title {
		margin: 0;
		text-wrap: balance;
		overflow-wrap: break-word;
	}
	.lede {
		margin: 0.375rem 0 0;
		font-size: 0.875rem;
		color: var(--hk-text-3);
	}
	.action {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
	}
	/* Over tabs the action adds no height to the title row, so the strip
	   sits at one y on a tab with an action and on one without (review r3
	   sys3-1). It stays on the title's baseline. */
	@media (min-width: 640px) {
		.head--tabs .action {
			margin-block: -0.75rem;
		}
	}
	.tabs {
		margin-top: 1.25rem;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.body {
		margin-top: 1.75rem;
	}
	.body--form {
		max-width: 42rem;
	}
</style>
