<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { authState, signIn } from '$lib/auth.svelte';
	import { FEEDBACK_URL, SOURCE_URL, pricingHref } from '$lib/links';
	import Logo from './Logo.svelte';
	import AccountMenu from './AccountMenu.svelte';
	import DashNav from './DashNav.svelte';

	let {
		dashboard
	}: {
		/** A signed-in page (projects, billing, settings): the account slot is the Account menu and DashNav sits beside the logo. */
		dashboard: boolean;
	} = $props();

	/** A signed-in page with its own places beside the logo (I-679). */
	let dash = $derived(dashboard && authState.authenticated);
	let onDocs = $derived(page.url.pathname === '/docs' || page.url.pathname.startsWith('/docs/'));

	// The links' one look, as the dashboard's row and a project's tabs draw
	// it: full height, so the current one's 1px ink edge lies on the
	// header's rule; ink when current, muted otherwise. The focus ring is
	// drawn inside the link, so the rule under it does not cut it.
	const LINK =
		'-mb-px flex items-center border-b transition-[color,border-color] duration-150 focus-visible:-outline-offset-2';
	const IDLE = 'border-transparent text-ink-muted hover:text-ink';
	const CURRENT = 'border-ink text-ink';
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
  fits a 320px phone. A signed-in page puts Projects, Billing and
  Settings beside the logo in the same row (I-679), drops Pricing, and
  below sm moves Docs and Feedback into the Account menu.
-->
<header class="sticky top-0 z-30 border-b border-rule bg-page">
	<div class="frame flex h-14 items-center justify-between gap-4">
		<div class="flex h-full min-w-0 items-stretch gap-5 sm:gap-8">
			<a
				href={resolve('/')}
				aria-label="repose, home"
				class="-m-2 flex shrink-0 items-center self-center p-2 focus-visible:-outline-offset-2"
				><span class="sm:hidden"><Logo size="sm" mark word={false} /></span><span
					class="hidden sm:inline"><Logo mark /></span
				></a
			>
			{#if dashboard && authState.authenticated}
				<DashNav />
			{/if}
		</div>
		<nav aria-label="Site" class="flex h-full items-stretch gap-4 text-sm sm:gap-6">
			<a
				href={resolve('/docs')}
				aria-current={onDocs ? 'page' : undefined}
				class="{LINK} {onDocs ? CURRENT : IDLE} {dash ? 'max-sm:hidden' : ''}">Docs</a
			>
			<!-- eslint-disable svelte/no-navigation-without-resolve -- pricingHref() builds it with resolve(); the other two are off-site -->
			{#if !dash}
				<a href={pricingHref()} class="{LINK} {IDLE} max-[399px]:hidden">Pricing</a>
			{/if}
			<a href={FEEDBACK_URL} class="{LINK} {IDLE} {dash ? 'max-sm:hidden' : ''}">Feedback</a>
			<a href={SOURCE_URL} class="{LINK} {IDLE} hidden sm:flex">GitHub</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
			<!-- One slot, one size: the three variants share a minimum width,
			     so the links beside it do not move when the sign-in check
			     answers. Unknown (and in the prerendered HTML) it is Sign in. -->
			<div class="flex items-center self-center">
				{#if authState.authenticated && dashboard}
					<AccountMenu />
				{:else if authState.authenticated}
					<a class="btn-quiet btn--sm min-w-26" href={resolve('/projects')}>Dashboard</a>
				{:else}
					<button type="button" class="btn-quiet btn--sm min-w-26" onclick={() => signIn()}
						>Sign in</button
					>
				{/if}
			</div>
		</nav>
	</div>
</header>
