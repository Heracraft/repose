<script lang="ts">
	// Settings → API tokens (DECISIONS I-1314, I-1322): the tokens an agent
	// or a script calls the api with.
	import ApiTokens from '$lib/components/ApiTokens.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('API tokens · Settings')}</title></svelte:head>

<ApiTokens />
