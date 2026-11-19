<script lang="ts">
	// Settings → Hosts (DECISIONS I-1366): the machines of your own that
	// run your projects, with the same add, list and remove `repose host`
	// has.
	import OwnHosts from '$lib/components/OwnHosts.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('Hosts · Settings')}</title></svelte:head>

<OwnHosts />
