<script lang="ts">
	import { pushState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { DOCS, type Doc, type DocHeading } from '$lib/docs';
	import { FEEDBACK_URL } from '$lib/links';
	import { PLANS } from '$lib/plans';
	import PlanCells from './PlanCells.svelte';

	let { doc }: { doc: Doc } = $props();

	let index = $derived(DOCS.findIndex((d) => d.slug === doc.slug));
	let prev = $derived(index > 0 ? DOCS[index - 1] : undefined);
	let next = $derived(index < DOCS.length - 1 ? DOCS[index + 1] : undefined);
	// The page's sections are its h2s; the rail lists each with its h3s
	// under it, one step in. The h3s show under the section being read,
	// or all of them on the CLI reference, whose 30 command h3s are what
	// a reader looks for there.
	let toc = $derived(doc.headings.filter((h) => h.depth === 2));
	let outline = $derived.by(() => {
		const out: { h: DocHeading; subs: DocHeading[] }[] = [];
		for (const h of doc.headings) {
			if (h.depth === 2) out.push({ h, subs: [] });
			else if (h.depth === 3 && out.length) out[out.length - 1].subs.push(h);
		}
		return out;
	});
	let allSubs = $derived(doc.slug === 'cli');

	// A page's "plans" comment block (the Pricing page's table) is drawn as
	// the plan cards, from lib/plans.ts as on the landing and /billing, so
	// the three cannot disagree (I-663). The table stays in the markdown for
	// /docs/<slug>.md, where an agent reads it. Built from strings: an HTML
	// comment opener in the script is a parse error in the dev server's
	// compiler.
	const PLANS_BLOCK = new RegExp(
		'<' + String.raw`!-- plans\b[\s\S]*?-->[\s\S]*?<` + String.raw`!-- /plans -->`
	);
	let parts = $derived.by(() => {
		const m = PLANS_BLOCK.exec(doc.html);
		return m
			? { before: doc.html.slice(0, m.index), after: doc.html.slice(m.index + m[0].length) }
			: undefined;
	});

	// The copy buttons come in doc.html, so one listener on the article
	// handles them all. The button's own word changes for the eye; the
	// live region below says the same to a screen reader, which does not
	// announce a change to the text of the button it is on.
	let article: HTMLElement | undefined = $state();
	let copyStatus = $state('');
	$effect(() => {
		if (!article) return;
		const el = article;
		const timers = new WeakMap<HTMLButtonElement, ReturnType<typeof setTimeout>>();
		const onClick = async (e: MouseEvent) => {
			const button = (e.target as Element).closest<HTMLButtonElement>('button.copy');
			if (!button) return;
			try {
				await navigator.clipboard.writeText(button.dataset.copy ?? '');
				button.textContent = 'Copied';
				button.classList.add('is-copied');
				copyStatus = 'Copied to the clipboard.';
			} catch {
				button.textContent = 'Copy failed';
				copyStatus = 'Could not copy to the clipboard.';
			}
			clearTimeout(timers.get(button));
			timers.set(
				button,
				setTimeout(() => {
					button.textContent = 'Copy';
					button.classList.remove('is-copied');
					copyStatus = '';
				}, 1200)
			);
		};
		el.addEventListener('click', onClick);
		return () => el.removeEventListener('click', onClick);
	});

	// A code pane or table that scrolls is one a keyboard user cannot read
	// to its end unless it takes focus (axe scrollable-region-focusable).
	// A pane scrolls sideways past its width and down past 30rem, so either
	// axis counts. Only a box that overflows gets the tab stop, named after
	// the section it sits in, checked again when the column changes width,
	// so where the docs fit Tab skips them. Two under one heading are told
	// apart by their place: "Code: Start an agent (2)".
	function sectionOf(box: HTMLElement): string {
		for (let el: Element | null = box; el && el !== article; el = el.parentElement) {
			for (let sib = el.previousElementSibling; sib; sib = sib.previousElementSibling) {
				if (/^H[23]$/.test(sib.tagName)) return (sib.textContent ?? '').replace(/^#/, '').trim();
			}
		}
		return doc.title;
	}
	$effect(() => {
		if (!article) return;
		const blocks = [...article.querySelectorAll<HTMLElement>('pre, .table-wrap')];
		const mark = () => {
			const seen: Record<string, number> = {};
			for (const box of blocks) {
				const scrolls = box.scrollWidth > box.clientWidth || box.scrollHeight > box.clientHeight;
				if (scrolls) {
					box.tabIndex = 0;
					box.setAttribute('role', 'region');
					const label = `${box.tagName === 'PRE' ? 'Code' : 'Table'}: ${sectionOf(box)}`;
					const n = (seen[label] ?? 0) + 1;
					seen[label] = n;
					box.setAttribute('aria-label', n > 1 ? `${label} (${n})` : label);
				} else {
					box.removeAttribute('tabindex');
					box.removeAttribute('role');
					box.removeAttribute('aria-label');
				}
			}
		};
		mark();
		const ro = new ResizeObserver(mark);
		for (const box of blocks) ro.observe(box);
		return () => ro.disconnect();
	});

	// The rail marks the section you are reading: the last h2 whose top has
	// passed the reading line, a quarter of the way down the window, or the
	// first one while you read the page's intro.
	// The pass over the h2s picks the current one. It runs on scroll, at
	// most once a frame: an observer alone stays silent when one jump (a
	// wheel fling, a scrollbar drag, scrollTo) carries a heading from below
	// the reading band to above it without landing inside, and the rail
	// went stale or empty. The observers still call it for what a scroll
	// does not report: the pager arriving on a resize, a heading moving
	// when a code block above it reflows. At the page's end the last
	// section that shows is current, since a short one never reaches the line.
	let active = $state<string | undefined>();
	let rail: HTMLElement | undefined = $state();
	let pager: HTMLElement | undefined = $state();
	$effect(() => {
		if (!article || !pager || toc.length < 2) return;
		const heads = toc
			.map((h) => article!.querySelector<HTMLElement>(`h2[id="${CSS.escape(h.id)}"]`))
			.filter((el): el is HTMLElement => el !== null);
		const pick = () => {
			const line = window.innerHeight / 4;
			const atEnd =
				window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2;
			let id: string | undefined;
			for (const el of heads) {
				const top = el.getBoundingClientRect().top;
				if (top <= line || (atEnd && top < window.innerHeight)) id = el.id;
			}
			// Above the first h2 the first entry is marked, so the rail
			// always says where you are.
			active = id ?? heads[0]?.id;
		};
		const io = new IntersectionObserver(pick, {
			rootMargin: '-57px 0px -75% 0px'
		});
		for (const el of heads) io.observe(el);
		const bottom = new IntersectionObserver(pick);
		if (pager) bottom.observe(pager);
		let frame = 0;
		const onScroll = () => {
			if (frame) return;
			frame = requestAnimationFrame(() => {
				frame = 0;
				pick();
			});
		};
		window.addEventListener('scroll', onScroll, { passive: true });
		pick();
		return () => {
			io.disconnect();
			bottom.disconnect();
			window.removeEventListener('scroll', onScroll);
			cancelAnimationFrame(frame);
		};
	});

	// A long rail scrolls on its own; it follows the current link.
	$effect(() => {
		if (!rail || !active) return;
		const link = rail.querySelector<HTMLElement>('[aria-current="true"]');
		if (!link) return;
		const box = rail.getBoundingClientRect();
		const at = link.getBoundingClientRect();
		if (at.top < box.top || at.bottom > box.bottom) {
			rail.scrollTop += at.top - box.top - rail.clientHeight / 3;
		}
	});

	// A section link scrolls smoothly unless the visitor asked for less
	// motion, then jumps; either way the URL takes the #section and focus
	// moves to the heading, as following the plain link would.
	function toSection(e: MouseEvent, id: string) {
		if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
		const el = document.getElementById(id);
		if (!el) return;
		e.preventDefault();
		const still = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		el.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'start' });
		el.tabIndex = -1;
		el.focus({ preventScroll: true });
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- a #section on the page already open
		pushState(`#${id}`, page.state);
	}

	function href(slug: string): string {
		return slug === 'index' ? resolve('/docs') : resolve('/docs/[slug]', { slug });
	}
	let markdown = $derived(resolve('/docs/[slug].md', { slug: doc.slug }));
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -- every internal href here comes from href() or resolve() -->

<svelte:head>
	<title>{doc.slug === 'index' ? 'repose docs' : `${doc.title} · repose docs`}</title>
	<meta name="description" content={doc.description} />
	<link rel="alternate" type="text/markdown" href={markdown} />
</svelte:head>

<!-- One measure: prose, code panes, tables and the plan cards share the
     text column's right edge. The cell DocPage fills starts 48px right of
     the sidebar; from 1280px it is the text, 48px and a 180px "On this
     page" rail at the frame's right edge, so at 1440 the text is 428 to
     1052 (624px) and the rail 1100 to 1280 (I-660). The rail's column is
     drawn on every page, empty when a page has one section or none, so the
     text and the rail sit at the same x on every page (I-396). Below 1280
     the text holds to 42rem and the sections fold under the toolbar. -->
<div class="xl:grid xl:grid-cols-[minmax(0,1fr)_180px] xl:gap-x-12">
	<main id="main" tabindex="-1" class="max-w-[42rem] min-w-0 pt-9 pb-24 outline-none xl:max-w-none">
		<p class="hk-eyebrow">{doc.section}</p>
		<h1 class="hk-h2 mt-2">{doc.title}</h1>
		{#if doc.description}
			<p class="hk-lead mt-2">{doc.description}</p>
		{/if}
		<div class="mt-5 flex items-center">
			<a
				class="hk-btn-ghost hk-btn-sm doc-tool"
				href={markdown}
				type="text/markdown"
				data-sveltekit-reload>View as Markdown</a
			>
		</div>

		<div class="mt-6 border-t border-hairline sm:mt-8">
			{#if toc.length > 1}
				<!-- Below 1280 there is no rail, so the page's sections fold
				     in here, one row on the page's hairline. -->
				<details class="doc-fold group border-b border-hairline xl:hidden">
					<summary
						class="flex h-11 cursor-pointer list-none items-center justify-between text-sm font-medium text-ink-2 hover:text-ink [&::-webkit-details-marker]:hidden"
					>
						On this page
						<svg
							viewBox="0 0 16 16"
							class="fold-chevron h-4 w-4 text-ink-3 group-open:rotate-180"
							aria-hidden="true"
							fill="none"
							stroke="currentColor"
							stroke-width="1.5"
							stroke-linecap="round"
							stroke-linejoin="round"><path d="M4 6l4 4 4-4" /></svg
						>
					</summary>
					<ul class="doc-toc pb-4">
						{#each toc as h (h.id)}
							<li><a href={`#${h.id}`}>{h.text}</a></li>
						{/each}
					</ul>
				</details>
			{/if}
		</div>

		{#if doc.experimental}
			<p class="hk-banner mt-8 mb-0" data-testid="experimental">
				<span
					>Experimental. This setup is new and can change or stop working. Tell us what breaks on
					the <a href={FEEDBACK_URL}>feedback board</a>.</span
				>
			</p>
		{/if}

		<!-- eslint-disable svelte/no-at-html-tags -- doc.html is rendered from this repo's own src/content/docs/*.md at build time, never from a user or the api -->
		{#if parts}
			<article bind:this={article} class="mt-8">
				<div class="doc">{@html parts.before}</div>
				<div class="doc-plans">
					<PlanCells plans={PLANS} introEligible variant="public" level={2} />
				</div>
				<div class="doc">{@html parts.after}</div>
			</article>
		{:else}
			<article bind:this={article} class="doc mt-8">{@html doc.html}</article>
		{/if}
		<!-- eslint-enable svelte/no-at-html-tags -->

		<!-- Always in the page, so the words that appear in it are announced. -->
		<p class="sr-only" role="status">{copyStatus}</p>

		<nav
			bind:this={pager}
			class="pager mt-12 grid gap-x-8 sm:grid-cols-2"
			aria-label="Previous and next page"
		>
			{#if prev}
				<a href={href(prev.slug)}>
					<span class="pager-label">Previous</span>
					<span class="pager-title">{prev.title}</span>
				</a>
			{:else}
				<span class="max-sm:hidden"></span>
			{/if}
			{#if next}
				<a href={href(next.slug)} class="sm:text-right">
					<span class="pager-label">Next</span>
					<span class="pager-title">{next.title}</span>
				</a>
			{/if}
		</nav>
	</main>

	<!-- Sticks at 57px like the sidebar: under the header and its hairline.
	     It draws no scrollbar, so a short window with a long rail shows no
	     overlay bar flashing over it (I-660); it scrolls by wheel and
	     follows the section being read. -->
	<div class="hidden xl:block">
		{#if toc.length > 1}
			<nav
				bind:this={rail}
				class="doc-rail sticky top-[57px] max-h-[calc(100dvh-57px)] overflow-y-auto overscroll-contain pt-9 pb-10 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
				aria-label="On this page"
			>
				<p class="doc-rail-title">On this page</p>
				<ul class="doc-rail-track">
					{#each outline as { h, subs } (h.id)}
						<li>
							<a
								href={`#${h.id}`}
								aria-current={active === h.id ? 'true' : undefined}
								onclick={(e) => toSection(e, h.id)}>{h.text}</a
							>
							{#if subs.length && (allSubs || active === h.id)}
								<ul>
									{#each subs as s (s.id)}
										<li>
											<a class="doc-rail-sub" href={`#${s.id}`} onclick={(e) => toSection(e, s.id)}
												>{s.label}</a
											>
										</li>
									{/each}
								</ul>
							{/if}
						</li>
					{/each}
				</ul>
			</nav>
		{/if}
	</div>
</div>

<style>
	/* The toolbar's one link sits flush with the text's left edge: the
	   ghost button's padding is pulled back. */
	.doc-tool {
		margin-left: -0.625rem;
	}

	.doc-plans {
		margin: 2rem 0;
	}
	/* The cards share the 624px text column at 1440, where the landing and
	   /billing give them 1120: from 900px a tighter inset, a smaller price
	   and a 12px specs line keep "$49 $39 a month", "8 GB running at once"
	   and "100 GB disk · 250 GB egress" on one line in a 197px card. */
	@media (min-width: 900px) {
		.doc-plans :global(.plans .plan) {
			padding: 0.9375rem;
		}
		.doc-plans :global(.plans .p-price .n) {
			font-size: 1.75rem;
			line-height: 2.25rem;
		}
		.doc-plans :global(.plans .p-run) {
			font-size: 0.875rem;
		}
		.doc-plans :global(.plans .p-specs--line) {
			font-size: 0.75rem;
		}
	}

	.pager {
		row-gap: 0;
	}
	.pager a {
		display: block;
		padding: 1rem 0;
		border-top: 1px solid var(--hk-hairline);
		text-decoration: none;
	}
	.pager-label {
		display: block;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
	}
	.pager-title {
		display: block;
		margin-top: 0.125rem;
		font-size: 0.9375rem;
		line-height: 1.5rem;
		font-weight: 500;
		color: var(--hk-text);
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	.pager a:hover .pager-title {
		color: var(--hk-accent);
	}

	@media (prefers-reduced-motion: reduce) {
		.pager-title {
			transition: none;
		}
	}
</style>
