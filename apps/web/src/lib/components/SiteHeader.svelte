<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { authState, signIn } from '$lib/auth.svelte';
	import { FEEDBACK_URL, SOURCE_URL, pricingHref } from '$lib/links';
	import Logo from './Logo.svelte';
	import AccountMenu from './AccountMenu.svelte';

	let {
		dashboard
	}: {
		/** A dashboard page: the account slot is the Account menu, and the rail or strip under the header holds the places. */
		dashboard: boolean;
	} = $props();

	/** A signed-in dashboard page: no Pricing, and below sm Docs and Feedback move into the Account menu (I-679, I-841). */
	let dash = $derived(dashboard && authState.authenticated);
	let onDocs = $derived(page.url.pathname === '/docs' || page.url.pathname.startsWith('/docs/'));

	// The links' one look: full row height, so the
	// current one's 1px accent edge lies on the header's rule; accent when
	// current, with the edge as its non-colour cue, muted otherwise, and no
	// weight change. The focus ring is drawn inside the link, so the rule
	// under it does not cut it.
	const LINK = 'hk-navlink';
</script>

<!--
  The one header (DECISIONS I-655): every page draws this, from the root
  layout, at once, without waiting for the sign-in check, so the first
  paint is the header in its final place. Sticky on every page, on a
  full-bleed hairline, its row on the shared .frame, so the logo stands at
  the content's left edge (x=160 at 1440, x=20 at 390) wherever you are,
  and the logo goes home, to /, from every page (owner's asks).
  The items are the same everywhere and in one order: Docs, Pricing,
  Feedback, GitHub, then one account slot. Below 400px Pricing goes (the
  footer has it), and below sm GitHub and the logo's word go, so the row
  fits a 320px phone. A dashboard page drops Pricing and below sm moves
  Docs and Feedback into the Account menu (I-679); its places are in the
  rail beside the page, or the strip under this header below 1024px
  (DashRail, I-841).
-->
<header class="site-header">
	<div class="frame flex h-14 items-center justify-between gap-4">
		<a
			href={resolve('/')}
			aria-label="repose, home"
			class="-m-2 flex shrink-0 items-center self-center p-2 focus-visible:-outline-offset-2"
			><span class="sm:hidden"><Logo size="sm" mark word={false} /></span><span
				class="hidden sm:inline"><Logo mark /></span
			></a
		>
		<nav aria-label="Site" class="flex h-full items-stretch gap-4 sm:gap-6">
			<a
				href={resolve('/docs')}
				aria-current={onDocs ? 'page' : undefined}
				class="{LINK} {dash ? 'max-sm:hidden' : ''}">Docs</a
			>
			<!-- eslint-disable svelte/no-navigation-without-resolve -- pricingHref() builds it with resolve(); the other two are off-site -->
			{#if !dash}
				<a href={pricingHref()} class="{LINK} max-[399px]:hidden">Pricing</a>
			{/if}
			<a href={FEEDBACK_URL} class="{LINK} {dash ? 'max-sm:hidden' : ''}">Feedback</a>
			<a href={SOURCE_URL} class="{LINK} hidden sm:flex">GitHub</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
			<!-- One slot, one size: the three variants share a minimum width,
			     so the links beside it do not move when the sign-in check
			     answers. Unknown (and in the prerendered HTML) it is Sign in. -->
			<div class="flex items-center self-center">
				{#if authState.authenticated && dashboard}
					<AccountMenu />
				{:else if authState.authenticated}
					<a class="hk-btn-secondary hk-btn-sm min-w-26" href={resolve('/projects')}>Dashboard</a>
				{:else}
					<button type="button" class="hk-btn-secondary hk-btn-sm min-w-26" onclick={() => signIn()}
						>Sign in</button
					>
				{/if}
			</div>
		</nav>
	</div>
</header>

<style>
	.site-header {
		position: sticky;
		top: 0;
		z-index: 30;
		border-bottom: 1px solid var(--hk-hairline);
		background: color-mix(in srgb, var(--hk-bg) 88%, transparent);
		backdrop-filter: saturate(1.4) blur(10px);
		-webkit-backdrop-filter: saturate(1.4) blur(10px);
	}
</style>
