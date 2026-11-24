<script lang="ts">
	// Open in Claude (DECISIONS I-1606): Claude Desktop's own link to a new
	// session on the machine over the user's ssh. A running machine, frozen
	// or not, opens at once (ssh wakes a frozen one); a stopped one starts
	// first, as the header's Start does, and the link opens once it runs.
	import { claudeLink } from '$lib/claudeLink';
	import type { Project } from '$lib/api/types';
	import { useProject } from './context.svelte';

	let { project }: { project: Project } = $props();
	const ctx = useProject();

	let href = $derived(claudeLink(project.slug, project.checkout));
	/** Set by a click on a stopped machine: open once it runs. */
	let opening = $state(false);

	/** The link, as a click on it would open it. */
	function follow(link: string) {
		const a = document.createElement('a');
		a.href = link;
		a.hidden = true;
		document.body.appendChild(a);
		a.click();
		a.remove();
	}

	async function startThenOpen() {
		opening = true;
		const started = await ctx.start();
		opening = false;
		if (started && ctx.project?.state === 'running') follow(href);
	}
</script>

{#if project.state === 'running'}
	<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- a claude:// link to the user's own app, not a page of this site -->
	<a class="hk-btn-secondary oic" {href} data-testid="open-in-claude">Open in Claude</a>
{:else if project.state === 'stopped'}
	<button
		type="button"
		class="hk-btn-secondary oic"
		data-testid="open-in-claude"
		disabled={!!ctx.opBusy}
		onclick={() => void startThenOpen()}>{opening ? 'Starting…' : 'Open in Claude'}</button
	>
{:else}
	<button type="button" class="hk-btn-secondary oic" data-testid="open-in-claude" disabled
		>{opening ? 'Starting…' : 'Open in Claude'}</button
	>
{/if}

<style>
	.oic {
		margin-top: 0.75rem;
		align-self: flex-start;
	}
</style>
