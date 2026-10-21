<script lang="ts">
	// The one error page (Guide B 12.22, I-837): the status in mono as the
	// eyebrow, the serif title, at most one line or a form, then the way
	// back. Signed in it offers Projects; signed out, the docs and home. It
	// owns the page's one <main>; the root layout draws the header and the
	// footer around it, so an error keeps its shell. Never a framework's
	// raw message.
	import type { Snippet } from 'svelte';
	import { resolve } from '$app/paths';
	import { authState } from '$lib/auth.svelte';

	let {
		status,
		title,
		children
	}: {
		status: number;
		title: string;
		/** One line, or a control such as Reload. */
		children?: Snippet;
	} = $props();
</script>

<main id="main" tabindex="-1" class="frame error-view">
	<p class="hk-eyebrow"><span class="hk-mono">{status}</span></p>
	<h1 class="hk-h2 title">{title}</h1>
	{#if children}<div class="extra">{@render children()}</div>{/if}
	<div class="links">
		{#if authState.authenticated}
			<a href={resolve('/projects')} class="hk-btn-secondary">Back to Projects</a>
		{:else}
			<a href={resolve('/docs')} class="hk-more">Docs</a>
			<a href={resolve('/')} class="hk-more">Home</a>
		{/if}
	</div>
</main>

<style>
	.error-view {
		padding-top: 5rem;
		padding-bottom: 6rem;
	}
	.title {
		margin: 0.75rem 0 0;
	}
	.extra {
		margin-top: 1rem;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
	}
	.links {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.75rem 1.5rem;
		margin-top: 1.75rem;
	}
	.links .hk-more {
		margin-top: 0;
	}
	/* 44px touch targets, as the landing's section links have. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.links {
			row-gap: 0;
		}
		.links .hk-more {
			padding-block: 0.6875rem;
		}
	}
</style>
