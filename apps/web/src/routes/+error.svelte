<script lang="ts">
	// The root error page: a path nothing serves, or a load that threw.
	// ErrorView is the whole page; the root layout keeps the header and the
	// footer around it.
	import { page } from '$app/state';
	import ErrorView from '$lib/components/ErrorView.svelte';

	let missing = $derived(page.status === 404);
	let title = $derived(missing ? 'Not found' : 'Something went wrong');
</script>

<svelte:head><title>{title} · repose</title></svelte:head>

<ErrorView status={page.status} {title} />
