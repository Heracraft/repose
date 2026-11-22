<script lang="ts">
	// Settings' six tabs (DECISIONS I-841, I-1322, I-1366): General,
	// Notifications, Secrets, API tokens, machine.nix and Hosts, links on PageShell's hairline as
	// a project's tabs are. machine.nix is listed only for an account with a
	// Nix project (I-1490): it reaches no other machine. Its page still
	// opens from a direct link, and its tab shows while it is open. The layout draws the title and the tabs, so a tab switch moves
	// nothing above the content; each page renders its sections only.
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import PageShell from '$lib/components/PageShell.svelte';
	import { projectsStore } from '$lib/projects.svelte';
	import { onMount } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const machineNix = resolve('/settings/machine-nix');
	const allTabs = [
		{ href: resolve('/settings'), label: 'General' },
		{ href: resolve('/settings/notifications'), label: 'Notifications' },
		{ href: resolve('/settings/secrets'), label: 'Secrets' },
		{ href: resolve('/settings/tokens'), label: 'API tokens' },
		{ href: machineNix, label: 'machine.nix' },
		{ href: resolve('/settings/hosts'), label: 'Hosts' }
	];
	/** A project from before I-1462 has no guest_runtime and is a Nix one. */
	let hasNix = $derived(!!projectsStore.list?.some((p) => (p.guest_runtime ?? 'nix') === 'nix'));
	let tabs = $derived(
		allTabs.filter((t) => t.href !== machineNix || hasNix || page.url.pathname === machineNix)
	);
	onMount(() => {
		void projectsStore.refresh(30_000);
	});
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
	/* Five labels measure 383px and a 360 column holds 305, so below 480
	   the row wraps to two instead of scrolling sideways (I-1322): every
	   tab stays on the screen, which a scrolled row does not -- the fifth
	   one was off the edge with nothing to say so. Each row carries its
	   own current-tab edge; the last row's lies on the head's rule, as
	   the single row's does. Measured: one row is 431px wide at 480,
	   where the column is 425, and fits from 520 (465 of 465), so 520
	   is where the wrap stops and not the 480 the eye would guess. */
	@media (max-width: 519.98px) {
		.hk-tabs.settings-tabs {
			flex-wrap: wrap;
			overflow-x: visible;
			column-gap: 1rem;
			row-gap: 0;
		}
	}

	/* The tab's content stands clear of the tabs' rule. */
	.settings {
		margin-top: 1rem;
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
