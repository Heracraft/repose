<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { afterNavigate } from '$app/navigation';
	import { pollWhileVisible } from '$lib/poll';
	import { pendingQuestions } from '$lib/questions.svelte';

	// The dashboard's own row, under the site header on signed-in pages
	// (DECISIONS I-655): the three places a signed-in visitor goes. It
	// scrolls away with the page; the header above it stays.
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

<nav aria-label="Dashboard" class="border-b border-rule">
	<div class="frame flex h-11 items-stretch gap-6 text-sm">
		{#each links as link (link.href)}
			<!-- eslint-disable svelte/no-navigation-without-resolve -- link.href is built with resolve() in the links array above -->
			<a
				href={link.href}
				aria-current={isCurrent(link.href) ? 'page' : undefined}
				class="-mb-px flex items-center gap-2 border-b transition-[color,border-color] duration-150 focus-visible:-outline-offset-2 {isCurrent(
					link.href
				)
					? 'border-ink text-ink'
					: 'border-transparent text-ink-muted hover:text-ink'}"
				aria-label={link.label === 'Projects' && waiting > 0
					? `Projects, ${waiting} ${waiting === 1 ? 'question' : 'questions'} waiting`
					: undefined}
				>{link.label}{#if link.label === 'Projects' && waiting > 0}<span
						class="badge badge--warn tabular-nums"
						aria-hidden="true">{waiting}</span
					>{/if}</a
			>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
	</div>
</nav>
