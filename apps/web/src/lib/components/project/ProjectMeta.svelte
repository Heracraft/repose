<script lang="ts">
	// The project's head under its title, the same on Overview, Config and
	// Secrets (review r3 sys3-1): the remote in mono, then the state and the
	// size. One head on every tab keeps the tab strip at one y, and Config
	// keeps the state that says whether Apply reaches a running machine.
	import type { Project } from '$lib/api/types';
	import StateDot from '$lib/components/StateDot.svelte';
	import { frozenSince } from '$lib/frozen';
	import { onMount } from 'svelte';
	import { clock } from '$lib/clock.svelte';
	import {
		classMemoryGB,
		classVCPUs,
		normalizeRemoteDisplay,
		sinceLabel,
		tempLeft,
		upLabel
	} from '$lib/format';

	let { project }: { project: Project } = $props();

	// The size's cores (the api calls the field vcpus); a stopped machine is
	// not sampled, so the figure comes from the class (I-492, I-784).
	let vcpus = $derived(classVCPUs(project.class) || undefined);
	/** When the machine was frozen while idle, behind the flag (I-683). */
	let frozen = $derived(frozenSince(project));
	// "up 3m" moves with the 30-second clock, not the poll.
	onMount(() => clock.use());
</script>

<div class="project-meta">
	{#if project.remote_url}
		<p class="remote">
			{#each normalizeRemoteDisplay(project.remote_url).split(/(?<=\/)/) as part, i (i)}{part}<wbr
				/>{/each}
		</p>
	{/if}
	<p class="status">
		<span class="status-state" data-testid="project-state"
			><StateDot state={project.state} frozen={!!frozen} /></span
		>
		<!-- Frozen reads as the list does, "frozen for 1h35m", with the size
	     facts every other state shows (review r1 dash-r1-4). -->
		{#if frozen}
			<span class="status-frozen" data-testid="frozen" title="It wakes when you connect."
				>{sinceLabel(frozen, 'for', clock.now)}</span
			>
		{/if}
		<span class="status-facts">
			<span aria-hidden="true">·</span>
			<span class="hk-mono" data-testid="project-class">{project.class}</span>
			{#if vcpus}
				<span aria-hidden="true">·</span>
				<span>{vcpus} {vcpus === 1 ? 'core' : 'cores'}</span>
				<span aria-hidden="true">·</span>
				<span>{classMemoryGB(project.class)} GB</span>
			{/if}
			{#if project.multiplexer === 'herdr' && project.state === 'stopped'}
				<!-- The stored choice is the next start's; a running machine may
			     still run tmux until it stops (I-502). -->
				<span aria-hidden="true">·</span>
				<span class="hk-mono">herdr</span>
			{/if}
			{#if project.state === 'running' && !frozen}
				<span aria-hidden="true">·</span>
				<span>{upLabel(project.started_at, clock.now)}</span>
			{/if}
			{#if project.expires_at}
				<span aria-hidden="true">·</span>
				<span class="hk-badge hk-badge--outline" data-testid="project-temporary">temporary</span>
				<span class="whitespace-nowrap">{tempLeft(project.expires_at)}</span>
			{/if}
		</span>
	</p>
</div>

<style>
	.project-meta {
		/* Under the title as its lede: pull back the tabs row's 1.25rem. */
		margin: -0.875rem 0 1.25rem;
	}
	.remote {
		margin: 0;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		overflow-wrap: anywhere;
	}
	.status {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.5rem;
		margin: 0.5rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.status-state {
		font-size: 0.875rem;
		color: var(--hk-text);
	}
	.status-facts {
		display: contents;
	}
	/* "for 1h38m" is a duration like "up 14h8m": the facts' size and ink. */
	.status-frozen {
		margin-left: -0.25rem;
	}
</style>
