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

	// The rail's rows: the docs rail's type, 28px tall, on a hairline, the
	// current one ink with a 1px ink edge laid on that hairline.
	const ROW =
		'-ml-px block border-l py-1 pl-3 text-sm leading-5 transition-[color,border-color] duration-150';
	const ROW_IDLE = 'border-transparent text-ink-muted hover:text-ink';
	const ROW_CURRENT = 'border-ink text-ink';

	// The policies' list in the left column, in the docs sidebar's type: a
	// 26px row, the current one ink on --sunken.
	const SIDE = 'block rounded-sm px-2 py-[3px] text-sm';
	const SIDE_IDLE = 'text-ink-muted hover:text-ink';
	const SIDE_CURRENT = 'bg-sunken font-medium text-ink';
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -- the policy links are built with resolve() above; the rest are #fragments -->

<!-- The root layout draws the header and the footer. The policies sit in
     the docs' frame (I-660), so a reader going from /docs/cli to /terms
     sees the text in the same place: from lg a 220px column under the
     logo with the three policies in the docs sidebar's type, a 48px gap,
     then the text; from xl the text, 48px and a 180px "On this page"
     rail at the frame's right edge. At 1440 that is 160, 428 to 1052 and
     1100 to 1280. Below xl the text holds to 42rem and the sections fold
     under the effective date; below lg the policies are one line of
     links there. The title sits 40px under the header's rule. -->
<div class="frame lg:grid lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-x-12">
	<!-- Sticks at 57px, under the header and its hairline, its label level
	     with the rail's. -->
	<div class="hidden lg:block">
		<nav aria-label="Policies" class="sticky top-[57px] pt-11 pb-10">
			<p class="mb-1 px-2 text-sm font-medium text-ink">Policies</p>
			<ul>
				{#each links as link (link.href)}
					<li>
						<a
							href={link.href}
							aria-current={page.url.pathname === link.href ? 'page' : undefined}
							class="{SIDE} {page.url.pathname === link.href ? SIDE_CURRENT : SIDE_IDLE}"
							>{link.label}</a
						>
					</li>
				{/each}
			</ul>
		</nav>
	</div>

	<div class="xl:grid xl:grid-cols-[minmax(0,1fr)_180px] xl:gap-x-12">
		<main id="main" class="max-w-[42rem] min-w-0 pt-10 pb-24 xl:max-w-none">
			<article bind:this={article}>
				<div class="doc prose max-w-none">
					<!-- eslint-disable-next-line svelte/no-at-html-tags -- the title comes from this repo's own markdown, as the body below does -->
					<h1 class="mb-4">{@html rendered.title}</h1>
				</div>
				<div class="mb-10 flex flex-col gap-4 text-sm">
					{#if meta.effective}
						<p class="text-ink-muted">Effective {longDate(meta.effective)}</p>
					{/if}
					{#if meta.status}
						<p class="banner banner--warn mb-0 self-start">Draft: {meta.status}</p>
					{/if}
					<nav aria-label="Policies" class="flex gap-5 lg:hidden">
						{#each links as link (link.href)}
							<a
								href={link.href}
								aria-current={page.url.pathname === link.href ? 'page' : undefined}
								class={page.url.pathname === link.href
									? 'text-ink underline decoration-1 underline-offset-4'
									: 'text-ink-muted hover:text-ink'}>{link.label}</a
							>
						{/each}
					</nav>
					{#if rendered.sections.length > 1}
						<!-- Below xl there is no rail, so the sections fold in here, as on the docs. -->
						<details class="group rounded-sm border border-rule xl:hidden">
							<summary
								class="flex cursor-pointer list-none items-center justify-between px-4 py-2 font-medium [&::-webkit-details-marker]:hidden"
							>
								On this page
								<svg
									viewBox="0 0 20 20"
									class="h-4 w-4 text-ink-muted transition-transform group-open:rotate-180 motion-reduce:transition-none"
									aria-hidden="true"
									fill="none"
									><path d="M5 8l5 5 5-5" stroke="currentColor" stroke-width="1.5" /></svg
								>
							</summary>
							<ul class="space-y-1.5 border-t border-rule px-4 py-3">
								{#each rendered.sections as h (h.id)}
									<li>
										<a href={`#${h.id}`} class="text-ink-muted hover:text-ink">{h.text}</a>
									</li>
								{/each}
							</ul>
						</details>
					{/if}
				</div>
				<div class="doc prose max-w-none">
					<!-- eslint-disable-next-line svelte/no-at-html-tags -- `raw` only ever comes from this repo's own src/content/legal/*.md via a ?raw import, never from a user or the api -->
					{@html rendered.html}
				</div>
			</article>
		</main>

		<!-- The docs rail's place and type: it sticks at 57px, draws no
		     scrollbar, and scrolls by wheel when the window is short. -->
		<div class="hidden xl:block">
			{#if rendered.sections.length > 1}
				<nav
					aria-label="On this page"
					class="sticky top-[57px] max-h-[calc(100dvh-57px)] overflow-y-auto overscroll-contain pt-11 pb-10 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
				>
					<p class="text-sm font-medium text-ink">On this page</p>
					<ul class="mt-3 border-l border-rule">
						{#each rendered.sections as h (h.id)}
							<li>
								<a
									href={`#${h.id}`}
									aria-current={active === h.id ? 'true' : undefined}
									class="{ROW} {active === h.id ? ROW_CURRENT : ROW_IDLE}">{h.text}</a
								>
							</li>
						{/each}
					</ul>
				</nav>
			{/if}
		</div>
	</div>
</div>
