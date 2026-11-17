<script lang="ts">
	// A project's Triggers tab (DECISIONS I-1258, amending I-1216's six):
	// what starts an agent here without you, what each run did, and the
	// form that makes one (I-1266). The frame above holds the project and
	// its poll; this tab fetches its own triggers and their runs.
	import ProjectTriggers from '$lib/components/project/ProjectTriggers.svelte';
	import { useProject } from '$lib/components/project/context.svelte';

	const ctx = useProject();
</script>

{#if ctx.project}
	<ProjectTriggers project={ctx.project} />
{/if}
