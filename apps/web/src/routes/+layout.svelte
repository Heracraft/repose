<script lang="ts">
	import './layout.css';
	import { onMount, setContext } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { afterNavigate, goto } from '$app/navigation';
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
	import DashRail from '$lib/components/DashRail.svelte';
	import { legacyTarget } from '$lib/legacyUrls';
	import { billingStore } from '$lib/billing.svelte';
	import { DASH, type DashContext } from '$lib/dash';

	let { children } = $props();

	// Routes reachable while signed out: these, and everything under /docs.
	const PUBLIC_PATHS = new Set(['/', '/callback', '/terms', '/privacy', '/refunds']);
	function isPublic(path: string): boolean {
		return PUBLIC_PATHS.has(path) || path === '/docs' || path.startsWith('/docs/');
	}
	// The signed-in places. Only these send a signed-out visitor home; any
	// other path renders (an unknown one, its 404) for everyone.
	const isPrivate = (p: string) => /^\/(projects|questions|billing|settings)(\/|$)/.test(p);

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

	// An old URL whose section became a tab or a page of its own moves
	// there, the first load included, replacing the history entry so Back
	// leaves instead of bouncing (DECISIONS I-841, lib/legacyUrls.ts).
	afterNavigate(({ to }) => {
		const target = to ? legacyTarget(to.url) : null;
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- legacyTarget answers a same-origin path built from the table
		if (target) void goto(target, { replaceState: true, noScroll: false });
	});

	let showChildren = $derived(authState.authenticated === true || !isPrivate(page.url.pathname));
	// A dashboard page: a private one, or any non-public page (a 404) for a
	// signed-in visitor, so a signed-in 404 keeps the dashboard's header.
	let dashboard = $derived(
		isPrivate(page.url.pathname) ||
			(authState.authenticated === true && !isPublic(page.url.pathname))
	);

	// Below 1024px the plan line sits over the page title and arrives with
	// billing. On the first load the page waits for that answer, hidden,
	// so the line paints with it and pushes nothing down (I-664, ia-final
	// 1.3): a fresh browser has no last mode to reserve room by. A billing
	// read slower than 2.5s lets the page show and takes the shift.
	const strip = new MediaQuery('max-width: 1023.98px');
	let billingLate = $state(false);
	onMount(() => {
		const t = setTimeout(() => (billingLate = true), 2500);
		return () => clearTimeout(t);
	});
	// The outage bar's height, taken off the grid's and the rail's so a
	// short page with the bar above the header still ends at the window's
	// foot (review r4).
	let outageH = $state(0);

	let holdMain = $derived(
		dashboard && strip.current && billingStore.mode === 'unknown' && !billingLate
	);

	// Inside the dashboard's grid the page's <main> drops its own frame:
	// the grid is the frame (PageShell, ErrorView read this). A page still
	// filling in holds the footer under the fold (DashContext.hold).
	let holds = $state(0);
	const dashContext: DashContext = {
		get on() {
			return dashboard;
		},
		hold() {
			holds += 1;
			let held = true;
			return () => {
				if (!held) return;
				held = false;
				holds -= 1;
			};
		}
	};
	setContext(DASH, dashContext);
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

{#snippet body()}
	{#if showChildren}
		<AppBoundary>{@render children()}</AppBoundary>
	{:else if authState.authenticated === undefined}
		<main id="main" tabindex="-1" class="auth-wait" class:frame={!dashboard}>
			<LoadState status="loading" shape="page" onretry={() => {}} />
		</main>
	{/if}
{/snippet}

<!-- The header is on every page from the first paint, before the sign-in
     check answers, and the footer under every page; a short page keeps
     the footer at the window's foot (DECISIONS I-655), with the outage
     strip in the same column so it never pushes the footer past the
     window (review r4). A private page
     whose check has not answered shows its skeleton in the frame where
     its content will be. -->
<div class="flex min-h-dvh flex-col">
	<!-- The live region is always in the page and only its contents change:
	     a role=status inserted with its text already in it is often read by
	     no screen reader (I-393). Empty, it draws nothing. -->
	<div id="outage" role="status" bind:offsetHeight={outageH}>
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
	<SiteHeader {dashboard} />
	<div class="flex-1" class:dash-fill={dashboard}>
		{#if dashboard}
			<!-- The dashboard (I-841): the rail and the page in one frame, a
			     grid from 1024px. The page column is the size container
			     `dash`, so pages lay out by its width, not the window's. -->
			<div class="frame dash" class:dash--filling={holds > 0} style:--outage-h="{outageH}px">
				<DashRail />
				<div class="dash-main" class:dash-main--hold={holdMain}>
					{@render body()}
				</div>
			</div>
		{:else}
			{@render body()}
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
	/* The grid fills the space between header and footer, so the rail can
	   take the row's height without setting the page's own. */
	.dash-fill {
		display: flex;
		flex-direction: column;
	}
	/* The grid is at least the window under the header (less the
	   compact footer from 1024px, where the rail counts on it), so the
	   footer starts under the fold until a page's content arrives, and
	   the sticky rail always has the room it needs (review IA). */
	.dash {
		display: grid;
		flex: 1;
		min-height: calc(100dvh - var(--header-h) - var(--outage-h, 0px));
		grid-template-columns: minmax(0, 1fr);
		/* Below 1024 the strip's row and the plan line's keep their height
		   and the page's row takes the room a short page leaves. */
		grid-template-rows: auto auto 1fr;
	}
	.dash-main {
		min-width: 0;
		container: dash / inline-size;
	}
	.dash-main--hold {
		visibility: hidden;
	}
	@media (min-width: 1024px) {
		.dash {
			min-height: calc(100dvh - var(--header-h) - var(--dash-foot-h) - var(--outage-h, 0px));
			grid-template-columns: 12.5rem minmax(0, 1fr);
			grid-template-rows: 1fr;
			column-gap: 3rem;
		}
		.dash-main {
			grid-column: 2;
			grid-row: 1;
		}
		/* A page filling in: the footer waits under the fold (I-664). */
		.dash--filling {
			min-height: calc(100dvh - var(--header-h) - var(--outage-h, 0px));
		}
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
