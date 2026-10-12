<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';

	// A project's three pages as links under its title, on the title's rule
	// (PageShell's tabs snippet; DECISIONS I-655). They change the URL, so
	// they are links with aria-current, not ARIA tabs (DESIGN-LANGUAGE.md,
	// "Tabs"), drawn like the header's links.
	let { id }: { id: string } = $props();

	let links = $derived([
		{ href: resolve('/projects/[id]', { id }), label: 'Overview' },
		{ href: resolve('/projects/[id]/config', { id }), label: 'Config' },
		{ href: resolve('/projects/[id]/secrets', { id }), label: 'Secrets' }
	]);
</script>

<nav aria-label="Project" class="-mb-px mt-5 flex h-10 items-stretch gap-6 text-sm">
	{#each links as link (link.label)}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- link.href is built with resolve() above -->
		<a
			href={link.href}
			aria-current={page.url.pathname === link.href ? 'page' : undefined}
			class="flex items-center border-b transition-[color,border-color] duration-150 focus-visible:-outline-offset-2 {page
				.url.pathname === link.href
				? 'border-ink text-ink'
				: 'border-transparent text-ink-muted hover:text-ink'}">{link.label}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/each}
</nav>
