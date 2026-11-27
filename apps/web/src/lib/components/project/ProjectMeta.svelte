<script lang="ts">
	// The project's head under its title, the same on Overview, Config and
	// Secrets (review r3 sys3-1): the remote in mono, then the state and the
	// size. One head on every tab keeps the tab strip at one y, and Config
	// keeps the state that says whether Apply reaches a running machine.
	import type { Project } from '$lib/api/types';
	import StateDot from '$lib/components/StateDot.svelte';
	import { frozenSince } from '$lib/frozen';
	import { notAnsweringSince, parkedText, restingWaitText } from '$lib/answering';
	import { onMount } from 'svelte';
	import { clock } from '$lib/clock.svelte';
	import {
		classMemoryGB,
		classVCPUs,
		normalizeRemoteDisplay,
		sinceLabel,
		sizeText,
		tempLeft,
		unsized,
		upLabel
	} from '$lib/format';

	let { project, host }: { project: Project; host?: string } = $props();

	// The size's cores (the api calls the field vcpus); a stopped machine is
	// not sampled, so the figure comes from the class (I-492, I-784).
	let vcpus = $derived(classVCPUs(project.class) || undefined);
	// A `tiny` sells half a core (DECISIONS I-1168). A half is written as a
	// word, as the CLI's own size summary writes it: "0.5 cores" reads like
	// a measurement rather than a size.
	let coresLabel = $derived(
		vcpus === 0.5 ? 'half a core' : `${vcpus} ${vcpus === 1 ? 'core' : 'cores'}`
	);
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
			><StateDot
				state={project.state}
				frozen={!!frozen}
				silent={!!notAnsweringSince(project)}
			/></span
		>
		<!-- Frozen reads as the list does, "frozen for 1h35m", with the size
	     facts every other state shows (review r1 dash-r1-4). Nothing says
	     what wakes it: I-826 cut the mock's sentence about connecting as
	     narrating what the screen implies, and a title attribute is still
	     a line the user reads. /docs/lifecycle answers it (I-484,
	     I-485). -->
		{#if frozen}
			<span class="status-frozen" data-testid="frozen">{sinceLabel(frozen, 'for', clock.now)}</span>
		{/if}
		{#if restingWaitText(project)}
			<!-- Resting as a save a locked Mac cannot read (I-1682). -->
			<span data-testid="resting-waits">{restingWaitText(project)}</span>
		{/if}
		{#if parkedText(project)}
			<!-- Parked on a Mac, waiting for its user to log in (I-1672). -->
			<span data-testid="parked">{parkedText(project)}</span>
		{/if}
		<span class="status-facts">
			{#if unsized(project)}
				<!-- No size on a host of the account's own (I-1632): what it
				     uses while it runs, and nothing when it does not. -->
				{#if sizeText(project)}
					<span aria-hidden="true">·</span>
					<span data-testid="project-used">{sizeText(project)}</span>
				{/if}
			{:else}
				<span aria-hidden="true">·</span>
				<span class="hk-mono" data-testid="project-class">{project.class}</span>
			{/if}
			{#if vcpus && !unsized(project)}
				<span aria-hidden="true">·</span>
				<span>{coresLabel}</span>
				<span aria-hidden="true">·</span>
				<span>{classMemoryGB(project.class)} GB</span>
			{/if}
			{#if host}
				<!-- Where this machine runs, shown when the account has a machine on
				     an owned host (DECISIONS I-1392): the host name, `repose`, or `-`. -->
				<span aria-hidden="true">·</span>
				<span data-testid="project-host"
					>{#if host === '-'}host -{:else}on <span class="hk-mono">{host}</span>{/if}</span
				>
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
