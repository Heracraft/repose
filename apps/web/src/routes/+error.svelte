<script lang="ts">
	// The root error page: a path nothing serves, or a load that threw.
	// ErrorView is the whole page; the root layout keeps the header and the
	// footer around it. Signed in, the <title> is a dashboard page's, with
	// the "(N)" of waiting questions, as a project's 404 has it (I-841).
	import { page } from '$app/state';
	import { authState } from '$lib/auth.svelte';
	import { title as dashTitle } from '$lib/title';
	import ErrorView from '$lib/components/ErrorView.svelte';

	let missing = $derived(page.status === 404);
	let title = $derived(missing ? 'Not found' : 'Something went wrong');
	let tab = $derived(authState.authenticated === true ? dashTitle(title) : `${title} · repose`);
</script>

<svelte:head><title>{tab}</title></svelte:head>

<ErrorView status={page.status} {title} />
