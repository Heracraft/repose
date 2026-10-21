<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { closestDocs } from '$lib/docs';

	let missing = $derived(page.status === 404);
	let closest = $derived(closestDocs(page.params.slug ?? ''));

	function href(slug: string): string {
		return slug === 'index' ? resolve('/docs') : resolve('/docs/[slug]', { slug });
	}
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -- href() builds every link with resolve() -->

<svelte:head
	><title>{missing ? 'Not found' : 'Something went wrong'} · repose docs</title></svelte:head
>

<!-- Inside the docs frame, sidebar and search kept, in DocPage's grid with
     the rail's 180px track left empty, so the title sits where every docs
     page puts its own. -->
<div class="xl:grid xl:grid-cols-[minmax(0,1fr)_180px] xl:gap-x-12">
	<main id="main" tabindex="-1" class="max-w-[42rem] min-w-0 pt-9 pb-24 outline-none xl:max-w-none">
		<p class="hk-eyebrow font-mono">{page.status}</p>
		<h1 class="hk-h2 mt-2">{missing ? 'Not found' : 'Something went wrong'}</h1>
		{#if missing}
			<h2 class="closest-label">Closest pages</h2>
			<ul class="closest">
				{#each closest as doc (doc.slug)}
					<li>
						<a href={href(doc.slug)}>{doc.title}</a>
						<span>{doc.section}</span>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="mt-6"><a class="hk-more mt-0" href={resolve('/docs')}>Quickstart</a></p>
		{/if}
	</main>
	<div class="hidden xl:block" aria-hidden="true"></div>
</div>

<style>
	.closest-label {
		margin: 2.5rem 0 0;
		padding-bottom: 0.625rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		font-weight: 500;
		letter-spacing: 0;
		color: var(--hk-text-3);
	}
	.closest {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.closest li {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		min-height: 2.75rem;
		padding: 0.6875rem 0;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.closest a {
		font-size: 0.875rem;
		line-height: 1.25rem;
		color: var(--hk-accent);
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	.closest a:hover {
		text-decoration-color: currentColor;
	}
	.closest span {
		flex: none;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
	}
</style>
