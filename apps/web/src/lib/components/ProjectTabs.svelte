<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { PROJECT_TABS, tabOf } from './project/tabs';

	// A project's tabs as links on the title's rule (PageShell's tabs
	// snippet draws the rule; DECISIONS I-655, I-837, I-841). They change
	// the URL, so they are links with aria-current, not ARIA tabs; .hk-tabs
	// draws the current one in ink with its 1px edge on the rule. All five
	// fit at 360 without scrolling.
	let { id }: { id: string } = $props();

	let base = $derived(resolve('/projects/[id]', { id }));
	let current = $derived(tabOf(page.url.pathname, id));
</script>

<nav aria-label="Project" class="hk-tabs project-tabs">
	{#each PROJECT_TABS as [label, path] (label)}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- base is built with resolve() above -->
		<a href={base + path} aria-current={current === label ? 'page' : undefined}>{label}</a>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/each}
</nav>

<style>
	/* Five tabs in the 305px column a 360 screen leaves: 0.625rem apart
	   below 400px, 1rem below 480px, the kit's 1.5rem from there. */
	.project-tabs {
		gap: 0.625rem;
	}
	@media (min-width: 400px) {
		.project-tabs {
			gap: 1rem;
		}
	}
	@media (min-width: 480px) {
		.project-tabs {
			gap: 1.5rem;
		}
	}
</style>
