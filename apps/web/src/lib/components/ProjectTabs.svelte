<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';

	// A project's three pages as links on the title's rule (PageShell's tabs
	// snippet draws the rule; DECISIONS I-655, I-837). They change the URL,
	// so they are links with aria-current, not ARIA tabs; .hk-tabs draws the
	// current one in ink with its 1px edge on the rule.
	let { id }: { id: string } = $props();

	let links = $derived([
		{ href: resolve('/projects/[id]', { id }), label: 'Overview' },
		{ href: resolve('/projects/[id]/config', { id }), label: 'Config' },
		{ href: resolve('/projects/[id]/secrets', { id }), label: 'Secrets' }
	]);
</script>

<nav aria-label="Project" class="hk-tabs">
	{#each links as link (link.label)}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- link.href is built with resolve() above -->
		<a href={link.href} aria-current={page.url.pathname === link.href ? 'page' : undefined}
			>{link.label}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/each}
</nav>
