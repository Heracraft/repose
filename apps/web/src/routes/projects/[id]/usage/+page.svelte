<script lang="ts">
	// SHELL stub (I-841): PAGE B moves the Usage section here and draws it
	// inside projects/[id]/+layout.svelte (ia-final 2.2). The route exists
	// so the old #usage links and the tab land somewhere.
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import PageShell from '$lib/components/PageShell.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('Usage')}</title></svelte:head>

<PageShell title="Usage" crumbs={[{ label: 'Projects', href: resolve('/projects') }]}>
	<p class="hk-sr">{page.params.id}</p>
</PageShell>
