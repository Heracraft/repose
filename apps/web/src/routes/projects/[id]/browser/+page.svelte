<script lang="ts">
	// A project's Browser tab (DECISIONS I-1216, amending I-841's five):
	// the machine's browser as the agents see it, drawn here. The frame
	// above holds the project and its poll; this tab opens one view and
	// nothing else, so a machine that is not running is the api's own
	// refusal rather than a second rule here.
	import BrowserView from '$lib/components/project/BrowserView.svelte';
	import { useProject } from '$lib/components/project/context.svelte';

	const ctx = useProject();
</script>

{#if ctx.project}
	<BrowserView project={ctx.project} />
{/if}
