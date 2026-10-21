<script lang="ts">
	import './layout.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { Toaster } from 'svelte-sonner';
	import { authState, initAuth } from '$lib/auth.svelte';
	import { reachability } from '$lib/api/reachability.svelte';
	import { init as initTheme, theme } from '$lib/theme.svelte';
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import LoadState from '$lib/components/LoadState.svelte';
	import AppBoundary from '$lib/components/AppBoundary.svelte';

	let { children } = $props();

	// Routes reachable while signed out: these, and everything under /docs.
	const PUBLIC_PATHS = new Set(['/', '/callback', '/terms', '/privacy', '/refunds']);
	function isPublic(path: string): boolean {
		return PUBLIC_PATHS.has(path) || path === '/docs' || path.startsWith('/docs/');
	}
	// The signed-in places. Only these send a signed-out visitor home; any
	// other path renders (an unknown one, its 404) for everyone.
	const isPrivate = (p: string) => /^\/(projects|billing|settings)(\/|$)/.test(p);

	onMount(() => {
		void initAuth();
		return initTheme();
	});

	// The skip link moves focus to the page's <main id="main">, which every
	// page has (src/lib/skip-target.test.ts holds every route to it).
	function skipToMain(e: MouseEvent) {
		const main = document.querySelector('main');
		if (!main) return;
		e.preventDefault();
		main.tabIndex = -1;
		main.focus();
	}

	// Signed-out visitors on a private route go to the landing page. A
	// signed-in visitor may read the landing page too (DECISIONS I-330); its
	// header offers the dashboard instead of sign-in, and signing in itself
	// still lands on /projects (routes/callback).
	$effect(() => {
		if (authState.authenticated === undefined) return;
		if (!authState.authenticated && isPrivate(page.url.pathname)) {
			void goto(resolve('/'));
		}
	});

	let showChildren = $derived(authState.authenticated === true || !isPrivate(page.url.pathname));
	// A dashboard page: a private one, or any non-public page (a 404) for a
	// signed-in visitor, so a signed-in 404 keeps the dashboard's header.
	let dashboard = $derived(
		isPrivate(page.url.pathname) ||
			(authState.authenticated === true && !isPublic(page.url.pathname))
	);
</script>

<!-- The first thing a keyboard reaches on every page: past the header's
     links to the page's own content (WCAG 2.4.1). Hidden until focused. -->
<a href="#main" class="hk-skip" onclick={skipToMain}>Skip to content</a>

<!-- Neutral toasts in the resolved theme (layout.css colours only an
     error). From 600px up the toast's right edge is the content column's,
     under the header's account button; on a phone it spans the width
     between the page's 20px gutters, at the bottom, where it can be swiped
     away (DECISIONS I-391 keeps it off the header). -->
<Toaster
	theme={theme.resolved}
	position="bottom-right"
	offset={{
		right: 'max(var(--frame-x), calc((100% - var(--frame-w)) / 2 + var(--frame-x)))',
		bottom: '24px'
	}}
	mobileOffset={{ left: '20px', right: '20px', top: '12px', bottom: '20px' }}
/>

<!-- The live region is always in the page and only its contents change:
     a role=status inserted with its text already in it is often read by
     no screen reader (I-393). Empty, it draws nothing. -->
<div id="outage" role="status">
	{#if !reachability.ok}
		<!-- A full-bleed strip above the header: the page's edge, not a box
		     in the column, so square and borderless. -->
		<div class="outage">
			<p class="frame">
				{reachability.reason === 'network'
					? 'Cannot reach the API. Retrying…'
					: 'The API is failing right now. Retrying…'}
			</p>
		</div>
	{/if}
</div>

<!-- The header is on every page from the first paint, before the sign-in
     check answers, and the footer under every page; a short page keeps
     the footer at the window's foot (DECISIONS I-655). A private page
     whose check has not answered shows its skeleton in the frame where
     its content will be. -->
<div class="flex min-h-dvh flex-col">
	<SiteHeader {dashboard} />
	<div class="flex-1">
		{#if showChildren}
			<AppBoundary>{@render children()}</AppBoundary>
		{:else if authState.authenticated === undefined}
			<main id="main" tabindex="-1" class="frame auth-wait">
				<LoadState status="loading" shape="page" onretry={() => {}} />
			</main>
		{/if}
	</div>
	<SiteFooter compact={dashboard} />
</div>

<style>
	.outage {
		background: var(--hk-err-wash);
		color: var(--hk-err);
		font-size: 0.8125rem;
		font-weight: 500;
	}
	.outage p {
		margin-block: 0;
		padding-block: 0.5rem;
		text-align: center;
	}
	/* PageShell's padding, so the swap to the page moves nothing (I-664). */
	.auth-wait {
		padding-top: 1.75rem;
		padding-bottom: 6rem;
	}
	@media (min-width: 640px) {
		.auth-wait {
			padding-top: 2.25rem;
		}
	}
</style>
