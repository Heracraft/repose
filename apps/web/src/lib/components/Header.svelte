<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { signOut } from '$lib/auth.svelte';
	import HeaderFrame from './HeaderFrame.svelte';

	const links = [
		{ href: resolve('/projects'), label: 'Projects' },
		{ href: resolve('/billing'), label: 'Billing' },
		{ href: resolve('/settings'), label: 'Settings' },
		{ href: resolve('/account'), label: 'Account' }
	];

	function isCurrent(href: string): boolean {
		return page.url.pathname === href || page.url.pathname.startsWith(`${href}/`);
	}
</script>

<!-- Below sm the five links and the logo share 350px: the logo is the
     mark alone and the links close up, so the page never scrolls sideways
     on a phone (judged at 390 and 360; CLAUDE.md "Judge visuals at real size").
     Docs is a sixth from sm up, as GitHub is on the landing: with it the
     row measures about 379px in Arial's metrics, past 360 (I-568). -->
<HeaderFrame home={resolve('/projects')} label="repose, projects">
	<nav
		class="flex h-full items-stretch gap-2.5 text-compact whitespace-nowrap sm:gap-6 sm:text-sm"
		aria-label="Main"
	>
		{#each links as link (link.href)}
			<!-- eslint-disable svelte/no-navigation-without-resolve -- link.href is built with resolve() in the links array above -->
			<a
				href={link.href}
				aria-current={isCurrent(link.href) ? 'page' : undefined}
				class="-mb-px flex items-center border-b {isCurrent(link.href)
					? 'border-ink text-ink'
					: 'border-transparent text-ink-muted hover:text-ink'}">{link.label}</a
			>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
		<a
			href={resolve('/docs')}
			class="-mb-px hidden items-center border-b border-transparent text-ink-muted hover:text-ink sm:flex"
			>Docs</a
		>
		<button
			type="button"
			class="cursor-pointer text-ink-muted hover:text-ink"
			onclick={() => signOut()}>Sign out</button
		>
	</nav>
</HeaderFrame>
