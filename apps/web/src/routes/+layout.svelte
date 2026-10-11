<script lang="ts">
	import './layout.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { Toaster } from 'svelte-sonner';
	import { authState, initAuth } from '$lib/auth.svelte';
	import { reachability } from '$lib/api/reachability.svelte';
	import SiteHeader from '$lib/components/SiteHeader.svelte';
	import SiteFooter from '$lib/components/SiteFooter.svelte';
	import LoadState from '$lib/components/LoadState.svelte';

	let { children } = $props();

	// Routes reachable while signed out: these, and everything under /docs.
	const PUBLIC_PATHS = new Set(['/', '/callback', '/terms', '/privacy', '/refunds']);
	function isPublic(path: string): boolean {
		return PUBLIC_PATHS.has(path) || path === '/docs' || path.startsWith('/docs/');
	}

	onMount(() => {
		void initAuth();
	});

	// The skip link moves focus to the page's <main>, which carries
	// id="main" on every page (the landing, the dashboard, the docs, the
	// legal pages and /callback's "Signing in"), so the link is in the
	// prerendered HTML too. src/lib/skip-target.test.ts holds every route to it.
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
		if (!authState.authenticated && !isPublic(page.url.pathname)) {
			void goto(resolve('/'));
		}
	});

	let dashboard = $derived(!isPublic(page.url.pathname));
	let showChildren = $derived(authState.authenticated === true || !dashboard);
</script>

<!-- The first thing a keyboard reaches on every page: past the header's
     links to the page's own content (WCAG 2.4.1). Hidden until focused. -->
<a href="#main" class="skip-link" onclick={skipToMain}>Skip to content</a>

<!-- No richColors: layout.css gives each toast type its banner's colours.
     From 600px up the toast's right edge is the content column's (the
     .frame's 1200px box less its gutter), not the window's, so at 1440 it
     lines up under the header's account button instead of 160px past it.
     On a phone it spans the width between the page's 20px gutters
     (sonner's own 16px missed the column by 4px on each side), at the
     bottom, where it can be swiped away (DECISIONS I-391 keeps it off the
     header). -->
<Toaster
	theme="system"
	position="bottom-right"
	offset={{
		right: 'max(var(--frame-x), calc((100% - var(--frame-w)) / 2 + var(--frame-x)))',
		bottom: '24px'
	}}
	mobileOffset={{ left: '20px', right: '20px', top: '12px', bottom: '20px' }}
/>

<!-- The live region is always in the page and only its contents change:
     a role=status inserted with its text already in it is often read by
     no screen reader (I-393). It appears after the page has loaded, and a
     screen reader should hear it without losing its place. Empty, it
     draws nothing. -->
<div id="outage" role="status">
	{#if !reachability.ok}
		<!-- The .banner--error colours, as a strip across the top: square,
		     no side edges and no margin, since it is the page's edge and
		     not a box in the column. -->
		<div
			class="banner banner--error mb-0 rounded-none border-x-0 border-t-0 py-2 text-center font-medium"
		>
			{reachability.reason === 'network'
				? 'Cannot reach the API. Retrying…'
				: 'The API is failing right now. Retrying…'}
		</div>
	{/if}
</div>

<!-- The header is on every page from the first paint, before the sign-in
     check answers, and the footer under every page; a short page keeps
     the footer at the window's foot (DECISIONS I-655). A private page
     whose check has not answered shows its skeleton in the frame where
     its content will be, not bare paper. -->
<div class="flex min-h-dvh flex-col">
	<SiteHeader {dashboard} />
	<div class="flex-1">
		{#if showChildren}
			{@render children()}
		{:else if authState.authenticated === undefined}
			<main id="main" class="frame pt-10 pb-24">
				<LoadState status="loading" shape="page" onretry={() => {}} />
			</main>
		{/if}
	</div>
	<SiteFooter />
</div>
