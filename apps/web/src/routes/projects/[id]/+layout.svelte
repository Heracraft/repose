<script lang="ts">
	// A project's five tabs share one frame (DECISIONS I-841): the header,
	// Start and Stop, the tabs and the project's poll stay mounted while
	// the tabs switch. Keyed on the id, so moving to another project from
	// the rail starts its own frame and polls instead of reusing this one's.
	import type { Snippet } from 'svelte';
	import { page } from '$app/state';
	import ProjectFrame from '$lib/components/project/ProjectFrame.svelte';

	let { children }: { children: Snippet } = $props();
	let id = $derived(page.params.id as string);
</script>

{#key id}
	<ProjectFrame {id}>{@render children()}</ProjectFrame>
{/key}
