<script lang="ts">
	import { Marked } from 'marked';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { codespan } from '$lib/codespan';

	// Inline code renders as the docs render it, so `repose-notify` stays
	// on one line here too; marked's default let it break at its hyphen.
	const marked = new Marked({ renderer: { codespan } });

	let { raw }: { raw: string } = $props();

	// content/legal/*.md files carry a small YAML-ish frontmatter block
	// (title, effective, status) that 14-security's docs use to mark drafts;
	// this is the only place that reads it, so a tiny hand-rolled parser
	// beats pulling in a YAML dependency for three known keys.
	function parseFrontmatter(text: string): { meta: Record<string, string>; body: string } {
		const match = /^---\n([\s\S]*?)\n---\n([\s\S]*)$/.exec(text);
		if (!match) return { meta: {}, body: text };
		const meta: Record<string, string> = {};
		for (const line of match[1].split('\n')) {
			const i = line.indexOf(':');
			if (i === -1) continue;
			meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
		}
		return { meta, body: match[2] };
	}

	const MONTHS = [
		'January',
		'February',
		'March',
		'April',
		'May',
		'June',
		'July',
		'August',
		'September',
		'October',
		'November',
		'December'
	];

	// "2026-09-27" as "27 September 2026", by hand rather than with
	// toLocaleDateString, so the prerendered text and the hydrated text are
	// the same string whatever the server's and the reader's locale.
	function longDate(iso: string): string {
		const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
		return m ? `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]} ${m[1]}` : iso;
	}

	// A heading's id, as the docs spell theirs ("Who can see your data" is
	// who-can-see-your-data). Written here rather than imported from
	// lib/docs, which would pull every docs page into the legal bundle.
	function slug(text: string): string {
		return text
			.toLowerCase()
			.replace(/<[^>]+>/g, '')
			.replace(/&[a-z]+;|&#\d+;/g, '')
			.replace(/[^a-z0-9]+/g, '-')
			.replace(/^-|-$/g, '');
	}

	let { meta, body } = $derived(parseFrontmatter(raw));
	// The policy's own h1 is drawn by this component, so the effective
	// date, the draft notice and (below lg) the policies' links sit under
	// it in that order; the markdown's h1 is taken out of the body. Each
	// h2 gets an id, so the rail beside the text can link to it.
	let rendered = $derived.by(() => {
		const sections: { id: string; text: string }[] = [];
		let title = meta.title ?? '';
		const html = (marked.parse(body) as string)
			.replace(/<h1>(.*?)<\/h1>\n?/, (_, inner: string) => {
				title = inner;
				return '';
			})
			.replace(/<h2>(.*?)<\/h2>/g, (_, inner: string) => {
				const id = slug(inner);
				sections.push({ id, text: inner.replace(/<[^>]+>/g, '').replace(/&amp;/g, '&') });
				return `<h2 id="${id}">${inner}</h2>`;
			});
		return { title, html, sections };
	});

	const links = [
		{ href: resolve('/terms'), label: 'Terms' },
		{ href: resolve('/privacy'), label: 'Privacy' },
		{ href: resolve('/refunds'), label: 'Refunds' }
	];

	// The section you are reading, marked in the rail as the docs mark
	// theirs (DocPage): the last h2 whose top has passed a line a quarter
	// of the way down the window, picked on scroll at most once a frame.
	// Above the first h2 you are reading the policy's opening, which is no
	// section, so nothing is marked.
	let article: HTMLElement | undefined = $state();
	let active = $state<string | undefined>();
	$effect(() => {
		void rendered;
		if (!article || rendered.sections.length < 2) return;
		const heads = [...article.querySelectorAll<HTMLElement>('h2[id]')];
		const pick = () => {
			const line = window.innerHeight / 4;
			const atEnd =
				window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2;
			let id: string | undefined;
			for (const el of heads) {
				const top = el.getBoundingClientRect().top;
				if (top <= line || (atEnd && top < window.innerHeight)) id = el.id;
			}
			active = id;
		};
		let frame = 0;
		const onScroll = () => {
			if (frame) return;
			frame = requestAnimationFrame(() => {
				frame = 0;
				pick();
			});
		};
		window.addEventListener('scroll', onScroll, { passive: true });
		window.addEventListener('resize', onScroll, { passive: true });
		pick();
		return () => {
			window.removeEventListener('scroll', onScroll);
			window.removeEventListener('resize', onScroll);
			cancelAnimationFrame(frame);
		};
	});
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -- the policy links are built with resolve() above; the rest are #fragments -->

<!-- The root layout draws the header and the footer. The policies sit in
     the docs' frame (I-660), so a reader going from /docs/cli to /terms
     sees the text in the same place: from 768px a 220px column under the
     logo with the three policies in the docs sidebar's look, a 48px gap,
     then the text; from 1280px the text, 48px and a 180px "On this page"
     rail at the frame's right edge. At 1440 that is 160, 428 to 1052 and
     1100 to 1280. Below 1280 the text holds to 42rem and the sections fold
     under the effective date; below 768px the policies are one row of
     links there. -->
<div class="frame md:grid md:grid-cols-[220px_minmax(0,1fr)] md:gap-x-12">
	<div class="hidden md:block">
		<nav aria-label="Policies" class="side sticky top-[57px] pt-8 pr-2 pb-10">
			<p class="side-title">Policies</p>
			<ul>
				{#each links as link (link.href)}
					<li>
						<a href={link.href} aria-current={page.url.pathname === link.href ? 'page' : undefined}
							>{link.label}</a
						>
					</li>
				{/each}
			</ul>
		</nav>
	</div>

	<div class="xl:grid xl:grid-cols-[minmax(0,1fr)_180px] xl:gap-x-12">
		<main
			id="main"
			tabindex="-1"
			class="max-w-[42rem] min-w-0 pt-9 pb-24 outline-none xl:max-w-none"
		>
			<article bind:this={article}>
				<p class="hk-eyebrow">Policies</p>
				<!-- eslint-disable-next-line svelte/no-at-html-tags -- the title comes from this repo's own markdown, as the body below does -->
				<h1 class="hk-h2 mt-2">{@html rendered.title}</h1>
				{#if meta.effective}
					<p class="mt-2 text-compact text-ink-3">Effective {longDate(meta.effective)}</p>
				{/if}
				{#if meta.status}
					<p class="hk-banner mt-5 mb-0">Draft: {meta.status}</p>
				{/if}
				<nav aria-label="Policies" class="phone-links mt-5 flex gap-5 md:hidden">
					{#each links as link (link.href)}
						<a href={link.href} aria-current={page.url.pathname === link.href ? 'page' : undefined}
							>{link.label}</a
						>
					{/each}
				</nav>
				<div class="mt-6 border-t border-hairline sm:mt-8">
					{#if rendered.sections.length > 1}
						<!-- Below 1280 there is no rail, so the sections fold in here, as on the docs. -->
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
								{#each rendered.sections as h (h.id)}
									<li><a href={`#${h.id}`}>{h.text}</a></li>
								{/each}
							</ul>
						</details>
					{/if}
				</div>
				<div class="doc mt-8">
					<!-- eslint-disable-next-line svelte/no-at-html-tags -- `raw` only ever comes from this repo's own src/content/legal/*.md via a ?raw import, never from a user or the api -->
					{@html rendered.html}
				</div>
			</article>
		</main>

		<!-- The docs rail's place and look: it sticks at 57px, draws no
		     scrollbar, and scrolls by wheel when the window is short. -->
		<div class="hidden xl:block">
			{#if rendered.sections.length > 1}
				<nav
					aria-label="On this page"
					class="doc-rail sticky top-[57px] max-h-[calc(100dvh-57px)] overflow-y-auto overscroll-contain pt-9 pb-10 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
				>
					<p class="doc-rail-title">On this page</p>
					<ul class="doc-rail-track">
						{#each rendered.sections as h (h.id)}
							<li>
								<a href={`#${h.id}`} aria-current={active === h.id ? 'true' : undefined}>{h.text}</a
								>
							</li>
						{/each}
					</ul>
				</nav>
			{/if}
		</div>
	</div>
</div>

<style>
	.side-title {
		margin: 0 0 0.25rem;
		padding: 0 0.625rem 0 calc(0.75rem + 2px);
		font-size: 0.75rem;
		line-height: 1.25rem;
		font-weight: 600;
		color: var(--hk-text);
	}
	.side ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.side a {
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
	.side a:hover {
		color: var(--hk-text);
		background: var(--hk-hover);
	}
	.side a[aria-current='page'] {
		color: var(--hk-accent);
		background: var(--hk-accent-wash);
		border-left-color: var(--hk-accent);
		border-radius: 0 var(--hk-radius-6) var(--hk-radius-6) 0;
	}

	/* Below 768px: one row of the three policies, the current one ink with
	   an underline, the others muted. 44px tall for a thumb. */
	.phone-links a {
		display: inline-flex;
		align-items: center;
		min-height: 2.75rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-decoration: none;
	}
	.phone-links a[aria-current='page'] {
		color: var(--hk-text);
		text-decoration: underline;
		text-decoration-thickness: 1px;
		text-underline-offset: 6px;
	}

	@media (prefers-reduced-motion: reduce) {
		.side a {
			transition: none;
		}
	}
	@media (forced-colors: active) {
		.side a {
			border-left-color: Canvas;
		}
		.side a[aria-current='page'] {
			border-left-color: Highlight;
			text-decoration: underline;
			text-decoration-thickness: 2px;
			text-underline-offset: 0.35em;
		}
	}
</style>
