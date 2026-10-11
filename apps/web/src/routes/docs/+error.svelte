<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
</script>

<svelte:head><title>Not found · repose docs</title></svelte:head>

<!-- DocPage's grid, with the rail's 180px track left empty, so the title
     sits where every docs page puts its own. -->
<div class="xl:grid xl:grid-cols-[minmax(0,1fr)_180px] xl:gap-x-12">
	<main id="main" class="max-w-[42rem] min-w-0 pt-10 pb-24 xl:max-w-none">
		<h1 class="text-4xl font-semibold tracking-[-0.01em]">No such page</h1>
		<p class="mt-3 text-lg text-ink-muted">
			{#if page.status === 404 && page.params.slug}
				There's no docs page called “{page.params.slug}”.
			{:else}
				This page could not be shown.
			{/if}
			<a href={resolve('/docs')} class="link">The overview</a> lists everything.
		</p>
	</main>
	<div class="hidden xl:block" aria-hidden="true"></div>
</div>
