<script lang="ts">
	import { tick } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { signOut, signedInEmail } from '$lib/auth.svelte';
	import { FEEDBACK_URL } from '$lib/links';

	// The account slot on a signed-in page (DECISIONS I-655): a disclosure,
	// not an ARIA menu, holding who is signed in and Sign out, and below
	// 640px the header links the one row has no room for (I-679). The
	// places (Billing, Settings) are in the rail or strip at every width
	// (I-841), so they are not here. Sign out is one step
	// away from every page and never sits in a row where a stray tap ends
	// the session. No modal and no trap: Tab leaves it and it closes.
	const id = $props.id();
	// display comes from the utility, so max-sm:hidden and the like win.
	const ITEM = 'account-item flex';
	let open = $state(false);
	// The menu's first line: who is signed in, read from the ID token
	// Logto already holds (no request).
	let email = $state<string | undefined>();
	onMount(() => {
		void signedInEmail().then((e) => (email = e));
	});
	let wrap: HTMLElement | undefined = $state();
	let button: HTMLButtonElement | undefined = $state();
	let panel: HTMLElement | undefined = $state();

	function items(): HTMLElement[] {
		// Items a narrow screen shows here, hidden on a wide one, are skipped.
		return panel
			? [...panel.querySelectorAll<HTMLElement>('a, button')].filter((el) => el.checkVisibility())
			: [];
	}

	async function show() {
		open = true;
		await tick();
		items()[0]?.focus({ preventScroll: true });
	}

	function hide(returnFocus: boolean) {
		if (!open) return;
		open = false;
		// The button sits in the sticky header: a plain focus() scrolls the page
		// to the button's static place, the top (review r4).
		if (returnFocus) button?.focus({ preventScroll: true });
	}

	// A navigation (Docs, or Back) closes it.
	let lastHref = page.url.href;
	$effect(() => {
		const href = page.url.href;
		if (href === lastHref) return;
		lastHref = href;
		hide(wrap?.contains(document.activeElement) ?? false);
	});

	function onkeydown(e: KeyboardEvent) {
		if (!open) return;
		if (e.key === 'Escape') {
			e.preventDefault();
			hide(true);
			return;
		}
		if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
		const list = items();
		if (!list.length) return;
		e.preventDefault();
		const at = list.indexOf(document.activeElement as HTMLElement);
		const step = e.key === 'ArrowDown' ? 1 : -1;
		list[(at + step + list.length) % list.length].focus({ preventScroll: true });
	}
</script>

<svelte:window
	onpointerdown={(e) => {
		if (open && wrap && !wrap.contains(e.target as Node)) hide(false);
	}}
/>

<!-- The keys belong to the button and the panel's items, handled once here for all of them. -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	bind:this={wrap}
	class="relative"
	{onkeydown}
	onfocusout={(e) => {
		if (open && wrap && !wrap.contains(e.relatedTarget as Node | null)) hide(false);
	}}
>
	<button
		bind:this={button}
		type="button"
		class="account-button hk-btn-secondary hk-btn-sm min-w-26 gap-1"
		aria-expanded={open}
		aria-controls="{id}-panel"
		onclick={() => (open ? hide(true) : show())}
		>Account<svg
			viewBox="0 0 16 16"
			class="chevron"
			class:chevron--open={open}
			fill="none"
			stroke="currentColor"
			stroke-width="1.5"
			stroke-linecap="round"
			stroke-linejoin="round"
			aria-hidden="true"><path d="M4 6l4 4 4-4" /></svg
		></button
	>
	<div bind:this={panel} id="{id}-panel" hidden={!open} class="account-panel">
		{#if email}<p class="account-email">{email}</p>{/if}
		<a href={resolve('/docs')} class="{ITEM} sm:hidden">Docs</a>
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- the feedback board is off-site -->
		<a href={FEEDBACK_URL} class="{ITEM} sm:hidden">Feedback</a>
		<button type="button" class={ITEM} onclick={() => signOut()}>Sign out</button>
	</div>
</div>

<style>
	/* hk-btn-sm's 30px, the public header's Dashboard button's height, so
	   the slot does not move between the two headers (review r1 sys-6). */
	.account-button {
		font-size: 0.8125rem;
	}
	.chevron {
		width: 0.875rem;
		height: 0.875rem;
		color: var(--hk-text-3);
		transition: transform var(--hk-dur-fast) var(--hk-ease);
	}
	.chevron--open {
		transform: rotate(180deg);
	}
	.account-panel {
		position: absolute;
		top: 100%;
		right: 0;
		z-index: 40;
		min-width: 12rem;
		margin-top: 0.375rem;
		padding: 0.375rem;
		border-radius: var(--hk-radius);
		background: var(--hk-surface);
		box-shadow: var(--hk-shadow-pop);
	}
	.account-email {
		margin: 0 0 0.375rem;
		padding: 0.25rem 0.5rem 0.5rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.75rem;
		color: var(--hk-text-3);
		overflow-wrap: anywhere;
	}
	/* No display here: a component's style is unlayered and would beat the
	   items' max-sm:hidden utilities. */
	.account-panel :global(.account-item) {
		align-items: center;
		width: 100%;
		height: 2rem;
		padding: 0 0.5rem;
		border: 0;
		border-radius: var(--hk-radius-6);
		background: transparent;
		font: inherit;
		font-size: 0.8125rem;
		color: var(--hk-text);
		text-align: left;
		text-decoration: none;
		cursor: pointer;
	}
	.account-panel :global(.account-item:hover) {
		background: var(--hk-hover);
	}
	.account-panel :global(.account-item:focus-visible) {
		outline-offset: -2px;
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.account-button {
			height: 2.75rem;
		}
		.account-panel :global(.account-item) {
			height: 2.75rem;
		}
	}
</style>
