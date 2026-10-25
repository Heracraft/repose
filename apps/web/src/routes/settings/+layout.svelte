<script lang="ts">
	// Settings' four tabs (DECISIONS I-841): General, Notifications, Secrets
	// and machine.nix, links on PageShell's hairline as a project's tabs
	// are. The layout draws the title and the tabs, so a tab switch moves
	// nothing above the content; each page renders its sections only.
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import PageShell from '$lib/components/PageShell.svelte';

	let { children }: { children: Snippet } = $props();

	const tabs = [
		{ href: resolve('/settings'), label: 'General' },
		{ href: resolve('/settings/notifications'), label: 'Notifications' },
		{ href: resolve('/settings/secrets'), label: 'Secrets' },
		{ href: resolve('/settings/machine-nix'), label: 'machine.nix' }
	];
</script>

{#snippet nav()}
	<nav aria-label="Settings" class="hk-tabs settings-tabs">
		{#each tabs as tab (tab.label)}
			<!-- eslint-disable svelte/no-navigation-without-resolve -- tab.href is built with resolve() above -->
			<a href={tab.href} aria-current={page.url.pathname === tab.href ? 'page' : undefined}
				>{tab.label}</a
			>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
	</nav>
{/snippet}

<PageShell title="Settings" tabs={nav}>
	<div class="settings">
		{@render children()}
	</div>
</PageShell>

<style>
	/* All four fit from 360 without sideways scroll; at 320 they scroll
	   sideways inside their own row (.hk-tabs). */
	@media (max-width: 479.98px) {
		.hk-tabs.settings-tabs {
			gap: 0.75rem;
		}
	}

	/* A settings row (General, Notifications): the h2 and at most one
	   note in 12rem on the left, the controls in at most 40rem on the
	   right, from a 640px column (I-841). One column below. */
	.settings :global(.set-row) {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		row-gap: 1rem;
	}
	.settings :global(.set-row:not(:first-child)) {
		margin-top: 2.5rem;
		padding-top: 2rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.settings :global(.set-head) {
		min-width: 0;
	}
	.settings :global(.set-note) {
		margin: 0.375rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
		overflow-wrap: anywhere;
	}
	.settings :global(.set-body) {
		min-width: 0;
	}
	@container dash (min-width: 640px) {
		.settings :global(.set-row) {
			grid-template-columns: 12rem minmax(0, 40rem);
			column-gap: 3rem;
		}
	}
	/* Inside a row the first fact drops its own top rule: the row's
	   hairline is directly above it. */
	.settings :global(.set-body > .hk-facts > div:first-child) {
		border-top: 0;
		padding-top: 0;
	}
</style>
