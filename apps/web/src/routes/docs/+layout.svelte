<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { docsBySection, search } from '$lib/docs';

	let { children } = $props();

	const groups = docsBySection();
	let query = $state('');
	let hits = $derived(search(query));
	let menuOpen = $state(false);
	let nav: HTMLElement | undefined = $state();
	let searchBox: HTMLElement | undefined = $state();
	let menuButton: HTMLButtonElement | undefined = $state();

	// The sidebar lists pages and nothing else, so its height is the same on
	// every page; the open page's sections are in DocPage's rail (I-396).
	let current = $derived(page.params.slug ?? 'index');
	let currentDoc = $derived(groups.flatMap((g) => g.docs).find((d) => d.slug === current));

	// Tutorials fold, so the list fits a 1440x900 window whole (I-660): open
	// on a tutorial's page, closed elsewhere, and the reader can open it.
	const FOLDED = 'Tutorials';
	const isFolded = (slug: string) =>
		groups.some((g) => g.section === FOLDED && g.docs.some((d) => d.slug === slug));
	let foldOpen = $derived(isFolded(current));

	// The list's two edges: a rule under the search once the list has
	// scrolled under it, and a 24px fade at the foot while more of the list
	// is below (layout.css, #docs-nav[data-fade]).
	let scrolled = $state(false);
	let fade = $state(false);
	function measure() {
		if (!nav) return;
		scrolled = nav.scrollTop > 0;
		fade = nav.scrollTop + nav.clientHeight < nav.scrollHeight - 1;
	}

	function href(slug: string): string {
		return slug === 'index' ? resolve('/docs') : resolve('/docs/[slug]', { slug });
	}

	// The sidebar stays mounted at every width (DECISIONS I-344): below lg it
	// is a drawer moved off screen, never display:none, so its scroll and the
	// search keep their state across opening, closing and navigating. The
	// scroll also survives a reload, per tab. Only closing animates
	// visibility: opening shows the drawer at once, so it can take focus.
	const SCROLL_KEY = 'repose.docs.nav.scroll';

	/** Whether the current page's link shows whole below the sticky search. */
	function currentShows(): boolean {
		const link = nav?.querySelector<HTMLElement>('[aria-current="page"]');
		if (!nav || !link) return true;
		const box = nav.getBoundingClientRect();
		const at = link.getBoundingClientRect();
		const top = searchBox ? searchBox.getBoundingClientRect().bottom : box.top;
		return at.height > 0 && at.top >= top && at.bottom <= box.bottom;
	}

	/** Scrolls the sidebar so the current page's link shows, if it doesn't. */
	function revealCurrent() {
		const link = nav?.querySelector<HTMLElement>('[aria-current="page"]');
		if (!nav || !link) return;
		// A link that shows whole moves nothing, so the list sits at the
		// same y from page to page. At 1440x900 the list, with Tutorials
		// folded, fits whole (I-660); scrolling for a 16px margin moved it
		// 15px on /docs/troubleshooting and left it there for the next page.
		if (currentShows()) return;
		const box = nav.getBoundingClientRect();
		const at = link.getBoundingClientRect();
		const top = searchBox ? searchBox.getBoundingClientRect().bottom : box.top;
		// Otherwise the link's group label goes 12px under the search, so
		// no label is left half under it, unless the group is taller than
		// the list's window: then the least scroll that shows the link, 16px
		// clear of the foot. Going down, the next link shows whole too, or
		// the end of the list for the last one, so a phone's drawer does
		// not open on a row cut at its bottom edge (I-400).
		const label = link.closest('[data-group]')?.querySelector<HTMLElement>('[data-group-label]');
		const pad = 12;
		if (label) {
			const shift = label.getBoundingClientRect().top - (top + pad);
			if (at.bottom - shift <= box.bottom - 16) {
				nav.scrollTop += shift;
				return;
			}
		}
		if (at.top < top) {
			nav.scrollTop -= top + pad - at.top;
			return;
		}
		const links = [...nav.querySelectorAll<HTMLElement>('nav[aria-label="Docs"] a')];
		const next = links[links.indexOf(link) + 1];
		if (next) {
			nav.scrollTop += next.getBoundingClientRect().bottom - (box.bottom - 16);
		} else {
			nav.scrollTop = nav.scrollHeight;
		}
	}

	onMount(() => {
		// The saved scroll comes back only when the current link would not
		// show without it: a list that shows the link at the top stays at
		// the top, so the sidebar does not open scrolled for no reason.
		try {
			const saved = Number(sessionStorage.getItem(SCROLL_KEY));
			if (nav && saved > 0 && !currentShows()) nav.scrollTop = saved;
		} catch {
			// Storage blocked: the sidebar starts at the top.
		}
		revealCurrent();
		measure();
		const ro = new ResizeObserver(measure);
		if (nav) {
			ro.observe(nav);
			if (nav.firstElementChild) ro.observe(nav.lastElementChild ?? nav.firstElementChild);
		}
		return () => ro.disconnect();
	});

	let saving = false;
	function onNavScroll() {
		measure();
		if (saving) return;
		saving = true;
		requestAnimationFrame(() => {
			saving = false;
			try {
				if (nav) sessionStorage.setItem(SCROLL_KEY, String(Math.round(nav.scrollTop)));
			} catch {
				// Storage blocked: nothing to keep.
			}
		});
	}

	// A navigation, a search hit's #heading on this page included, closes the
	// drawer, and the sidebar follows to the page it lands on.
	$effect(() => {
		void page.url.href;
		menuOpen = false;
		void tick().then(() => {
			revealCurrent();
			measure();
		});
	});
	// Opening or closing the fold, or a search, changes the list's height.
	$effect(() => {
		void foldOpen;
		void query;
		void tick().then(measure);
	});

	async function openMenu() {
		menuOpen = true;
		await tick();
		revealCurrent();
		nav?.focus({ preventScroll: true });
	}

	function closeMenu(returnFocus = true) {
		if (!menuOpen) return;
		menuOpen = false;
		if (returnFocus) menuButton?.focus();
	}

	// The page behind an open drawer doesn't scroll.
	$effect(() => {
		if (!menuOpen) return;
		const root = document.documentElement;
		const before = root.style.overflow;
		root.style.overflow = 'hidden';
		return () => {
			root.style.overflow = before;
		};
	});
</script>

<svelte:window
	onkeydown={(e) => {
		if (e.key === 'Escape' && menuOpen) closeMenu();
	}}
/>

<!-- eslint-disable svelte/no-navigation-without-resolve -- every internal href here comes from href(), which builds it with resolve() -->

<!-- Below lg the docs' own bar sits under the site header and sticks with
     it: the menu button and the open page's title. From lg up the sidebar
     is always there and the bar goes (I-655). -->
<div data-docs-subbar class="sticky top-[57px] z-20 border-b border-rule bg-page lg:hidden">
	<div class="frame flex h-11 items-center gap-3">
		<button
			bind:this={menuButton}
			type="button"
			class="-ml-2 flex h-9 shrink-0 cursor-pointer items-center gap-2 rounded-sm px-2 text-sm text-ink hover:bg-sunken"
			aria-label={menuOpen ? 'Close the docs menu' : 'Open the docs menu'}
			aria-expanded={menuOpen}
			aria-controls="docs-nav"
			onclick={() => (menuOpen ? closeMenu() : openMenu())}
		>
			<svg viewBox="0 0 20 20" class="h-5 w-5 text-ink-muted" aria-hidden="true" fill="none">
				{#if menuOpen}
					<path d="M5 5l10 10M15 5L5 15" stroke="currentColor" stroke-width="1.5" />
				{:else}
					<path d="M3 5.5h14M3 10h14M3 14.5h14" stroke="currentColor" stroke-width="1.5" />
				{/if}
			</svg>
			Menu
		</button>
		{#if currentDoc}
			<span
				class="min-w-0 truncate border-l border-rule pl-3 text-sm text-ink-muted"
				aria-hidden="true">{currentDoc.title}</span
			>
		{/if}
	</div>
</div>

{#if menuOpen}
	<!-- The backdrop is a pointer target only; Escape and the menu button close the drawer from the keyboard. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div
		class="fixed inset-x-0 top-[101px] bottom-0 z-10 bg-zinc-950/25 lg:hidden dark:bg-black/50"
		onclick={() => closeMenu()}
	></div>
{/if}

<!-- The shared frame (I-656), so the sidebar starts under the logo: the
     220px sidebar, a 48px gap, and DocPage's cell, which holds the text
     and, from xl, the 180px "On this page" rail at the frame's right edge.
     At 1440 that is x=160, 428 and 1100 (I-660). -->
<div class="frame lg:grid lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-x-12">
	<!-- The sidebar sticks at 57px, the header's 56px row plus its hairline,
	     so it rests where it starts and does not move 1px on the first
	     scroll. From lg up it draws no scrollbar: the overlay one flashed
	     over the list on the first visit while revealCurrent and the saved
	     scroll settled, and vanished at the first move of the page (owner's
	     ask, I-660). It still scrolls by wheel, trackpad and keyboard, and
	     the fade at its foot says there is more. The drawer below lg keeps
	     its scrollbar. -->
	<aside
		id="docs-nav"
		bind:this={nav}
		onscroll={onNavScroll}
		tabindex="-1"
		aria-label="Docs menu"
		data-fade={fade ? '' : undefined}
		class="fixed top-[101px] bottom-0 left-0 z-20 w-[min(20rem,85vw)] overflow-y-auto overscroll-contain border-r border-rule bg-page px-5 pb-10 duration-200 ease-out outline-none motion-reduce:transition-none {menuOpen
			? 'visible translate-x-0 transition-[translate]'
			: 'invisible -translate-x-full transition-[translate,visibility]'} lg:visible lg:sticky lg:top-[57px] lg:bottom-auto lg:z-auto lg:h-[calc(100dvh-57px)] lg:w-[220px] lg:translate-x-0 lg:border-r-0 lg:bg-transparent lg:px-0 lg:pb-6 lg:transition-none lg:[scrollbar-width:none] lg:[&::-webkit-scrollbar]:hidden"
	>
		<!-- The search sticks to the top of the sidebar, so a list taller
		     than a phone's drawer scrolls under it and the search stays in
		     reach however far the current page's link sits down the list. A
		     rule under it shows once the list has gone under it. -->
		<div
			bind:this={searchBox}
			class="sticky top-0 z-10 -mx-5 border-b bg-page px-5 pt-5 pb-3 lg:mx-0 lg:px-0 {scrolled
				? 'border-rule'
				: 'border-transparent'}"
		>
			<label for="docs-search" class="sr-only">Search the docs</label>
			<input
				id="docs-search"
				type="search"
				class="field w-full"
				placeholder="Search the docs"
				autocomplete="off"
				bind:value={query}
			/>
		</div>

		{#if query.trim()}
			<ul class="mt-2 space-y-1" aria-label="Search results">
				{#each hits as hit (hit.doc.slug)}
					<li>
						<a
							href={href(hit.doc.slug) + (hit.heading ? `#${hit.heading.id}` : '')}
							class="block rounded-sm px-2 py-1.5 hover:bg-sunken"
						>
							<span class="block text-sm font-medium">
								{hit.doc.title}{#if hit.heading}<span class="text-ink-muted">
										› {hit.heading.text}</span
									>{/if}
							</span>
							<span class="mt-0.5 block text-xs text-ink-muted">{hit.snippet}</span>
						</a>
					</li>
				{:else}
					<li class="px-2 py-1.5 text-sm text-ink-muted">Nothing matches.</li>
				{/each}
			</ul>
		{:else}
			<nav class="mt-1" aria-label="Docs">
				{#snippet list(docs: (typeof groups)[number]['docs'])}
					<ul>
						{#each docs as doc (doc.slug)}
							<li>
								<a
									href={href(doc.slug)}
									aria-current={current === doc.slug ? 'page' : undefined}
									class="block rounded-sm px-2 py-[3px] text-sm {current === doc.slug
										? 'bg-sunken font-medium text-ink'
										: 'text-ink-muted hover:text-ink'}">{doc.title}</a
								>
							</li>
						{/each}
					</ul>
				{/snippet}
				{#each groups as group, i (group.section)}
					{#if group.section === FOLDED}
						<details data-group class="group/fold {i > 0 ? 'mt-4' : ''}" bind:open={foldOpen}>
							<summary
								data-group-label
								class="mb-1 flex cursor-pointer list-none items-center justify-between rounded-sm px-2 text-sm font-medium text-ink [&::-webkit-details-marker]:hidden"
							>
								{group.section}
								<svg
									viewBox="0 0 16 16"
									class="h-4 w-4 text-ink-muted transition-transform duration-150 group-open/fold:rotate-180 motion-reduce:transition-none"
									fill="none"
									stroke="currentColor"
									stroke-width="1.5"
									stroke-linecap="square"
									aria-hidden="true"><path d="M4 6l4 4 4-4" /></svg
								>
							</summary>
							{@render list(group.docs)}
						</details>
					{:else}
						<div data-group class={i > 0 ? 'mt-4' : ''}>
							<p data-group-label class="mb-1 px-2 text-sm font-medium text-ink">
								{group.section}
							</p>
							{@render list(group.docs)}
						</div>
					{/if}
				{/each}
			</nav>
		{/if}
	</aside>

	<div class="min-w-0">
		{@render children()}
	</div>
</div>
