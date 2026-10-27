<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { docBySlug, movedDoc } from '$lib/docs';
	import DocPage from '$lib/components/DocPage.svelte';

	let doc = $derived(docBySlug(page.params.slug ?? ''));
	let moved = $derived(movedDoc(page.params.slug ?? ''));

	// The overview's slug is 'index', but its page is /docs, not /docs/index.
	function href(slug: string): string {
		return slug === 'index' ? resolve('/docs') : resolve('/docs/[slug]', { slug });
	}

	$effect(() => {
		if (moved)
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- href() resolve()s it, plus the old link's #section
			void goto(href(moved) + page.url.hash, { replaceState: true });
	});
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -- href() builds the link with resolve() -->

{#if doc}
	{#key doc.slug}
		<DocPage {doc} />
	{/key}
{:else if moved}
	<!-- An old slug's page holds the target's link, not nothing: the
	     prerendered HTML is what a reader with scripts off gets, and the
	     layout's skip link (#main) needs a target on every page. -->
	<main id="main" tabindex="-1" class="max-w-[42rem] pt-9 pb-24 outline-none">
		<p class="text-ink-2">
			This page moved to <a href={href(moved)} class="hk-link">{docBySlug(moved)?.title ?? moved}</a
			>.
		</p>
	</main>
{/if}
