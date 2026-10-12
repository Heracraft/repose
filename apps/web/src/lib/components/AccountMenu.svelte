<script lang="ts">
	import { tick } from 'svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { signOut } from '$lib/auth.svelte';
	import { FEEDBACK_URL } from '$lib/links';

	// The account slot on a signed-in page (DECISIONS I-655): a disclosure,
	// not an ARIA menu, holding Settings and Sign out, and on a phone the
	// header links that the one row has no room for (I-679). Sign out is one step
	// away from every page and never sits in a row where a stray tap ends
	// the session. No modal and no trap: Tab leaves it and it closes.
	const id = $props.id();
	const ITEM =
		'flex h-9 items-center px-3 text-sm text-ink hover:bg-sunken focus-visible:-outline-offset-2';
	let open = $state(false);
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
		items()[0]?.focus();
	}

	function hide(returnFocus: boolean) {
		if (!open) return;
		open = false;
		if (returnFocus) button?.focus();
	}

	// A navigation (Settings, or Back) closes it.
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
		list[(at + step + list.length) % list.length].focus();
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
		class="btn-quiet btn--sm min-w-26 gap-1"
		aria-expanded={open}
		aria-controls="{id}-panel"
		onclick={() => (open ? hide(true) : show())}
		>Account<svg
			viewBox="0 0 16 16"
			class="h-4 w-4 text-ink-muted transition-transform duration-150 motion-reduce:transition-none {open
				? 'rotate-180'
				: ''}"
			fill="none"
			stroke="currentColor"
			stroke-width="1.5"
			stroke-linecap="square"
			aria-hidden="true"><path d="M4 6l4 4 4-4" /></svg
		></button
	>
	<div
		bind:this={panel}
		id="{id}-panel"
		hidden={!open}
		class="absolute top-full right-0 z-40 mt-1.5 min-w-44 rounded-sm border border-rule-strong bg-surface py-1"
	>
		<a href={resolve('/billing')} class="{ITEM} min-[360px]:hidden">Billing</a>
		<a href={resolve('/docs')} class="{ITEM} sm:hidden">Docs</a>
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- the feedback board is off-site -->
		<a href={FEEDBACK_URL} class="{ITEM} sm:hidden">Feedback</a>
		<a
			href={resolve('/settings')}
			class="flex h-9 items-center px-3 text-sm text-ink hover:bg-sunken focus-visible:-outline-offset-2"
			>Settings</a
		>
		<button
			type="button"
			class="flex h-9 w-full cursor-pointer items-center px-3 text-left text-sm text-ink hover:bg-sunken focus-visible:-outline-offset-2"
			onclick={() => signOut()}>Sign out</button
		>
	</div>
</div>
