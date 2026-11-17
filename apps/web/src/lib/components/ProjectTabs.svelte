<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { PROJECT_TABS, tabOf } from './project/tabs';

	// A project's tabs as links on the title's rule (PageShell's tabs
	// snippet draws the rule; DECISIONS I-655, I-837, I-841). They change
	// the URL, so they are links with aria-current, not ARIA tabs; .hk-tabs
	// draws the current one in ink with its 1px edge on the rule. Six tabs
	// since I-1216; the row scrolls sideways where they do not fit.
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
	/* Six tabs are 318px of labels (measured): with the kit's 1.5rem gaps
	   they need 438px, which a row holds from about 500px of screen up.
	   Below that the row scrolls, which .hk-tabs is built for, and the
	   narrower gaps keep more of it in view: 1rem below 480 (398px of row
	   against the 335px a 390 screen leaves), 0.625rem below 385 (368
	   against 305). Five tabs fitted a 360 screen; the sixth is worth the
	   scroll (I-1216). */
	.project-tabs {
		gap: 0.625rem;
	}
	@media (min-width: 385px) {
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
