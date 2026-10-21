<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { docsBySection, search } from '$lib/docs';
	import { pricingHref } from '$lib/links';

	let { children } = $props();

	const groups = docsBySection();
	let query = $state('');
	let hits = $derived(search(query));
	let menuOpen = $state(false);
	let nav: HTMLElement | undefined = $state();
	let searchBox: HTMLElement | undefined = $state();
	let menuButton: HTMLButtonElement | undefined = $state();
	let content: HTMLElement | undefined = $state();

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

	// The sidebar stays mounted at every width (DECISIONS I-344): below md it
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

	// From md the sidebar sticks under the header. Its height follows the
	// end of the docs grid, so near the footer it gets shorter, and its top,
	// with the search, stays at 57px; a fixed 100dvh height made sticky push
	// it up under the header by the footer's height.
	const HEADER = 57;
	let navHeight = $state<number | undefined>();
	let sizing = false;
	function sizeNav() {
		if (sizing) return;
		sizing = true;
		requestAnimationFrame(() => {
			sizing = false;
			const grid = nav?.parentElement;
			if (!grid) return;
			const end =
				grid.getBoundingClientRect().bottom - parseFloat(getComputedStyle(grid).paddingBottom);
			navHeight = Math.max(0, Math.min(window.innerHeight, end) - HEADER);
		});
	}

	onMount(() => {
		sizeNav();
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
			sizeNav();
			revealCurrent();
			measure();
		});
	});
	// Opening or closing the fold, or a search, changes the list's height.
	// The hits start at the top of the list, best first, whatever the page
	// list was scrolled to; clearing the search goes back to the open page.
	let searching = $derived(query.trim() !== '');
	$effect(() => {
		void foldOpen;
		const q = query.trim();
		void tick().then(() => {
			if (q && nav) nav.scrollTop = 0;
			measure();
		});
	});
	$effect(() => {
		if (searching) return;
		void tick().then(() => {
			revealCurrent();
			measure();
		});
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
		// The button sticks under the header; a plain focus() scrolls the
		// page to bring it in, and the reader loses their place.
		if (returnFocus) menuButton?.focus({ preventScroll: true });
	}

	// The page behind an open drawer doesn't scroll.
	$effect(() => {
		if (!menuOpen) return;
		const root = document.documentElement;
		const before = root.style.overflow;
		root.style.overflow = 'hidden';
		// Tab stays in the drawer: the article column and the footer behind
		// the scrim go inert. The header and the sub-bar stay live, so the
		// menu button still closes it (kit 1.8).
		const behind = [content, ...document.querySelectorAll<HTMLElement>('.site-footer')].filter(
			(el): el is HTMLElement => !!el
		);
		for (const el of behind) el.inert = true;
		// From 768px the sidebar is the menu; a window widened with the
		// drawer open closes it, so nothing stays inert.
		const wide = window.matchMedia('(min-width: 768px)');
		const onWide = () => {
			if (wide.matches) closeMenu(false);
		};
		wide.addEventListener('change', onWide);
		return () => {
			wide.removeEventListener('change', onWide);
			root.style.overflow = before;
			for (const el of behind) el.inert = false;
		};
	});
</script>

<svelte:window
	onscroll={sizeNav}
	onresize={sizeNav}
	onkeydown={(e) => {
		if (e.key === 'Escape' && menuOpen) closeMenu();
	}}
/>

<!-- eslint-disable svelte/no-navigation-without-resolve -- every internal href here comes from href() or pricingHref(), which build it with resolve() -->

<!-- Below 768px the docs' own bar sits under the site header and sticks
     with it, ending at 101px: the menu button, which shows the open page's
     title, and Pricing, which the header drops on a phone. From 768px the
     sidebar is always there and the bar goes (I-655). -->
<div data-docs-subbar class="subbar sticky top-[57px] z-20 md:hidden">
	<nav aria-label="Docs bar" class="frame flex h-11 items-center justify-between gap-3">
		<button
			bind:this={menuButton}
			type="button"
			class="subbar-menu"
			aria-expanded={menuOpen}
			aria-controls="docs-nav"
			onclick={() => (menuOpen ? closeMenu() : openMenu())}
		>
			<span class="sr-only">{menuOpen ? 'Close the docs menu' : 'Open the docs menu'},</span>
			<span class="min-w-0 truncate">{currentDoc?.title ?? 'Docs'}</span>
			<svg
				viewBox="0 0 16 16"
				class="subbar-chevron h-4 w-4 shrink-0"
				class:open={menuOpen}
				aria-hidden="true"
				fill="none"
				stroke="currentColor"
				stroke-width="1.5"
				stroke-linecap="round"
				stroke-linejoin="round"><path d="M4 6l4 4 4-4" /></svg
			>
		</button>
		{#if current !== 'billing'}
			<!-- The docs' own Pricing page already says it in the bar's title. -->
			<a href={pricingHref()} class="subbar-link">Pricing</a>
		{/if}
	</nav>
</div>

{#if menuOpen}
	<!-- The backdrop is a pointer target only; Escape and the menu button close the drawer from the keyboard. -->
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div
		class="scrim fixed inset-x-0 top-[101px] bottom-0 z-10 md:hidden"
		onclick={() => closeMenu()}
	></div>
{/if}

<!-- The shared frame (I-656), so the sidebar starts under the logo: the
     220px sidebar, a 48px gap, and DocPage's cell, which holds the text
     and, from 1280px, the 180px "On this page" rail at the frame's right
     edge. At 1440 that is x=160, 428 and 1100 (I-660). -->
<div class="frame md:grid md:grid-cols-[220px_minmax(0,1fr)] md:gap-x-12">
	<!-- The sidebar sticks at 57px, the header's 56px row plus its hairline,
	     so it rests where it starts and does not move 1px on the first
	     scroll. From 768px it draws no scrollbar: the overlay one flashed
	     over the list on the first visit while revealCurrent and the saved
	     scroll settled (owner's ask, I-660). It still scrolls by wheel,
	     trackpad and keyboard, and the fade at its foot says there is more.
	     The phone's drawer keeps its scrollbar and is opaque. -->
	<aside
		id="docs-nav"
		bind:this={nav}
		onscroll={onNavScroll}
		tabindex="-1"
		aria-label="Docs menu"
		data-fade={fade ? '' : undefined}
		style:--docs-nav-h={navHeight === undefined ? undefined : `${navHeight}px`}
		class="sidebar fixed top-[101px] bottom-0 left-0 z-20 w-[min(20rem,85vw)] overflow-y-auto overscroll-contain px-5 pb-10 outline-none {menuOpen
			? 'is-open visible translate-x-0'
			: 'invisible -translate-x-full'} md:visible md:sticky md:top-[57px] md:bottom-auto md:z-auto md:h-[var(--docs-nav-h,calc(100dvh-57px))] md:w-[220px] md:translate-x-0 md:pt-8 md:pr-2 md:pb-8 md:pl-0 md:[scrollbar-width:none] md:[&::-webkit-scrollbar]:hidden"
	>
		<!-- The search sticks to the top of the sidebar, so a list taller
		     than a phone's drawer scrolls under it and the search stays in
		     reach. A rule under it shows once the list has gone under it. -->
		<div
			bind:this={searchBox}
			class="search-box sticky top-0 z-10 -mx-5 px-5 pt-5 pb-3 md:mx-0 md:px-0 md:pt-0"
			class:scrolled
		>
			<label for="docs-search" class="sr-only">Search the docs</label>
			<input
				id="docs-search"
				type="search"
				class="hk-input hk-input--search docs-search"
				placeholder="Search the docs"
				autocomplete="off"
				bind:value={query}
			/>
		</div>

		{#if searching}
			<ul class="hits mt-2" aria-label="Search results">
				{#each hits as hit (hit.doc.slug)}
					<li>
						<a href={href(hit.doc.slug) + (hit.heading ? `#${hit.heading.id}` : '')}>
							<span class="hit-title">
								{hit.doc.title}{#if hit.heading}<span class="hit-heading"
										>{` › ${hit.heading.text}`}</span
									>{/if}
							</span>
							<span class="hit-snippet"
								>{#each hit.parts as part, i (i)}{#if part.hit}<mark class="hit-mark"
											>{part.text}</mark
										>{:else}{part.text}{/if}{/each}</span
							>
						</a>
					</li>
				{:else}
					<li class="hit-none">Nothing matches.</li>
				{/each}
			</ul>
		{:else}
			<nav aria-label="Docs">
				{#snippet list(docs: (typeof groups)[number]['docs'])}
					<ul class="items">
						{#each docs as doc (doc.slug)}
							<li>
								<a href={href(doc.slug)} aria-current={current === doc.slug ? 'page' : undefined}
									>{doc.title}</a
								>
							</li>
						{/each}
					</ul>
				{/snippet}
				{#each groups as group (group.section)}
					{#if group.section === FOLDED}
						<details data-group class="group/fold group-block" bind:open={foldOpen}>
							<summary data-group-label class="group-title fold-title">
								{group.section}
								<svg
									viewBox="0 0 16 16"
									class="fold-chevron h-4 w-4 text-ink-3 group-open/fold:rotate-180"
									fill="none"
									stroke="currentColor"
									stroke-width="1.5"
									stroke-linecap="round"
									stroke-linejoin="round"
									aria-hidden="true"><path d="M4 6l4 4 4-4" /></svg
								>
							</summary>
							{@render list(group.docs)}
						</details>
					{:else}
						<div data-group class="group-block">
							<p data-group-label class="group-title">{group.section}</p>
							{@render list(group.docs)}
						</div>
					{/if}
				{/each}
			</nav>
		{/if}
	</aside>

	<div class="min-w-0" bind:this={content}>
		{@render children()}
	</div>
</div>

<style>
	.subbar {
		border-bottom: 1px solid var(--hk-hairline);
		background: color-mix(in srgb, var(--hk-bg) 92%, transparent);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
	}
	.subbar-menu {
		display: flex;
		align-items: center;
		gap: 0.375rem;
		min-width: 0;
		height: 2.75rem;
		margin-left: -0.5rem;
		padding: 0 0.5rem;
		border-radius: var(--hk-radius-sm);
		font-size: 0.875rem;
		font-weight: 500;
		color: var(--hk-text);
		cursor: pointer;
		transition: background-color var(--hk-dur-fast) var(--hk-ease);
	}
	.subbar-menu:hover {
		background: var(--hk-hover);
	}
	.subbar-chevron {
		color: var(--hk-text-3);
		transition: transform var(--hk-dur-fast) var(--hk-ease);
	}
	.subbar-chevron.open {
		transform: rotate(180deg);
	}
	.subbar-link {
		display: flex;
		flex: none;
		align-items: center;
		height: 2.75rem;
		margin-right: -0.5rem;
		padding: 0 0.5rem;
		font-size: 0.875rem;
		color: var(--hk-text-2);
		text-decoration: none;
	}
	.subbar-link:hover {
		color: var(--hk-text);
	}
	.scrim {
		background: var(--hk-scrim);
	}

	/* The phone's drawer: an opaque page-colour panel, its edge a hairline. */
	.sidebar {
		background: var(--hk-bg);
		border-right: 1px solid var(--hk-border);
		transition:
			translate var(--hk-dur-screen) var(--hk-ease),
			visibility 0s linear var(--hk-dur-screen);
	}
	.sidebar.is-open {
		transition: translate var(--hk-dur-screen) var(--hk-ease);
	}
	@media (min-width: 768px) {
		.sidebar {
			background: transparent;
			border-right: 0;
			transition: none;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.sidebar,
		.sidebar.is-open,
		.subbar-menu,
		.subbar-chevron {
			transition: none;
		}
	}

	.search-box {
		background: var(--hk-bg);
		border-bottom: 1px solid transparent;
	}
	.search-box.scrolled {
		border-bottom-color: var(--hk-hairline);
	}
	@media (pointer: fine) and (min-width: 640px) {
		.docs-search {
			height: 2rem;
			min-height: 2rem;
			font-size: 0.8125rem;
		}
	}
	/* Chromium's own clear button is not the kit's; Escape clears the field. */
	.docs-search::-webkit-search-cancel-button {
		appearance: none;
	}

	.group-block {
		margin-top: 1.25rem;
	}
	/* The items carry a 2px left rule for the current page; the titles
	   take the same 2px, so both texts start at one x. */
	.group-title {
		margin: 0 0 0.25rem;
		padding: 0 0.625rem 0 calc(0.75rem + 2px);
		font-size: 0.75rem;
		line-height: 1.25rem;
		font-weight: 600;
		color: var(--hk-text);
	}
	.fold-title {
		display: flex;
		align-items: center;
		justify-content: space-between;
		border-radius: var(--hk-radius-6);
		cursor: pointer;
		list-style: none;
	}
	.fold-title::-webkit-details-marker {
		display: none;
	}
	.fold-title:hover {
		background: var(--hk-hover);
	}

	.items {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.items a {
		display: block;
		padding: 0.3125rem 0.625rem 0.3125rem 0.75rem;
		border-left: 2px solid transparent;
		border-radius: var(--hk-radius-6);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-2);
		text-decoration: none;
		transition:
			color var(--hk-dur-fast) var(--hk-ease),
			background-color var(--hk-dur-fast) var(--hk-ease);
	}
	.items a:hover {
		color: var(--hk-text);
		background: var(--hk-hover);
	}
	.items a[aria-current='page'] {
		color: var(--hk-accent);
		background: var(--hk-accent-wash);
		border-left-color: var(--hk-accent);
		border-radius: 0 var(--hk-radius-6) var(--hk-radius-6) 0;
	}
	/* Thumb-sized rows in the phone's drawer. */
	@media (pointer: coarse), (max-width: 767.98px) {
		.items a {
			padding-block: 0.75rem;
			font-size: 0.9375rem;
		}
		.group-title {
			font-size: 0.8125rem;
		}
		.fold-title {
			min-height: 2.75rem;
		}
	}

	.hits {
		margin: 0.5rem 0 0;
		padding: 0;
		list-style: none;
	}
	.hits a {
		display: block;
		padding: 0.5rem 0.625rem;
		border-radius: var(--hk-radius-6);
		text-decoration: none;
	}
	.hits a:hover {
		background: var(--hk-hover);
	}
	.hit-title {
		display: block;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.hit-heading {
		font-weight: 400;
		color: var(--hk-text-3);
	}
	.hit-snippet {
		display: block;
		margin-top: 0.125rem;
		font-size: 0.75rem;
		line-height: 1.125rem;
		color: var(--hk-text-3);
	}
	.hit-mark {
		padding: 0;
		border-radius: var(--hk-radius-xs);
		background: var(--hk-accent-wash);
		color: var(--hk-text);
	}
	.hit-none {
		padding: 0.5rem 0.625rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}

	/* Forced colours paint a transparent edge in the text colour, so the
	   reserved edges take the canvas and only the current page keeps one,
	   with an underline as well. */
	@media (forced-colors: active) {
		.items a,
		.search-box {
			border-color: Canvas;
		}
		.search-box.scrolled {
			border-bottom-color: CanvasText;
		}
		.items a[aria-current='page'] {
			border-left-color: Highlight;
			text-decoration: underline;
		}
	}
</style>
