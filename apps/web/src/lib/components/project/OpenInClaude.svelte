<script lang="ts">
	// Open in Claude (DECISIONS I-1606): Claude Desktop's own link to a new
	// session on the machine over the user's ssh. A running machine, frozen
	// or not, opens at once (ssh wakes a frozen one); a stopped one starts
	// first, as the header's Start does, and the link opens once it runs.
	// The link works only where `<slug>.repose` is in the user's own ssh
	// config, which repose's CLI writes at `repose login`; the page cannot
	// see whether this computer did, so it says so once, beside the button
	// (ENG-224). Windows and phones get no button: Desktop cannot reach a
	// machine there (ENG-225).
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { claudeLink, claudePlatform, type ClaudePlatform } from '$lib/claudeLink';
	import type { Project } from '$lib/api/types';
	import { useProject } from './context.svelte';

	let { project }: { project: Project } = $props();
	const ctx = useProject();

	let href = $derived(claudeLink(project.slug, project.checkout));
	/** Unknown until the page runs in the browser. */
	let platform = $state<ClaudePlatform | undefined>(undefined);
	onMount(() => {
		const nav = navigator as Navigator & { userAgentData?: { mobile?: boolean } };
		platform = claudePlatform(nav.userAgent, nav.maxTouchPoints, nav.userAgentData?.mobile);
	});
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

{#if platform === 'desktop'}
	<div class="oic">
		{#if project.state === 'running'}
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- a claude:// link to the user's own app, not a page of this site -->
			<a class="hk-btn-secondary" {href} data-testid="open-in-claude">Open in Claude</a>
		{:else if project.state === 'stopped'}
			<button
				type="button"
				class="hk-btn-secondary"
				data-testid="open-in-claude"
				disabled={!!ctx.opBusy}
				onclick={() => void startThenOpen()}>{opening ? 'Starting…' : 'Open in Claude'}</button
			>
		{:else}
			<button type="button" class="hk-btn-secondary" data-testid="open-in-claude" disabled
				>{opening ? 'Starting…' : 'Open in Claude'}</button
			>
		{/if}
		<p class="oic-need">
			This computer needs the repose CLI and <code>repose login</code>, once:
			<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'install' })}>Install</a>.
		</p>
	</div>
{:else if platform === 'windows'}
	<!-- eslint-disable svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
	<p class="oic-need oic">
		Claude Desktop on Windows can't reach a machine yet:
		<a class="hk-link" href={resolve('/docs/[slug]', { slug: 'ssh-and-editors' }) + '#windows'}
			>Windows</a
		>.
	</p>
{/if}

<style>
	.oic {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem 1rem;
		margin-bottom: 1rem;
	}
	.oic-need code {
		white-space: nowrap;
		margin-inline: 0;
	}
	.oic-need {
		margin: 0;
		color: var(--hk-text-2, inherit);
		font-size: 0.875rem;
	}
</style>
