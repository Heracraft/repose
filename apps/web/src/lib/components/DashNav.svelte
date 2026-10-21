<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { afterNavigate } from '$app/navigation';
	import { pollWhileVisible } from '$lib/poll';
	import { pendingQuestions } from '$lib/questions.svelte';

	// The dashboard's places, in the site header beside the logo on
	// signed-in pages (DECISIONS I-655, I-679: one row, not a second bar).
	// Below sm Settings is only in the Account menu, and below 360px
	// Billing too, so the row fits a 320px phone.
	const links = [
		{ href: resolve('/projects'), label: 'Projects' },
		{ href: resolve('/billing'), label: 'Billing' },
		{ href: resolve('/settings'), label: 'Settings' }
	];

	function isCurrent(href: string): boolean {
		return page.url.pathname === href || page.url.pathname.startsWith(`${href}/`);
	}

	// Questions an agent is waiting on, across every project, so the count
	// shows from billing or settings too. Every 30s while the tab is in
	// view: pollWhileVisible ticks every 10s, and two of three ticks pass.
	// Every navigation reads it again, and a page that answers a question
	// or reads the pending list sets the shared count itself, so the badge
	// never outlives the question it counted.
	$effect(() => pollWhileVisible(() => pendingQuestions.refresh(29_000)));
	afterNavigate((nav) => {
		if (nav.type !== 'enter') void pendingQuestions.refresh();
	});
	let waiting = $derived(pendingQuestions.count);
</script>

<nav aria-label="Dashboard" class="flex h-full items-stretch gap-4 sm:gap-6">
	{#each links as link (link.href)}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- link.href is built with resolve() in the links array above -->
		<a
			href={link.href}
			aria-current={isCurrent(link.href) ? 'page' : undefined}
			class="hk-navlink {link.label === 'Settings'
				? 'max-sm:hidden'
				: link.label === 'Billing'
					? 'max-[359px]:hidden'
					: ''}"
			aria-label={link.label === 'Projects' && waiting > 0
				? `Projects, ${waiting} ${waiting === 1 ? 'question' : 'questions'} waiting`
				: undefined}
			>{link.label}{#if link.label === 'Projects' && waiting > 0}<span
					class="hk-badge hk-badge--warn hk-num"
					aria-hidden="true">{waiting}</span
				>{/if}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/each}
</nav>
