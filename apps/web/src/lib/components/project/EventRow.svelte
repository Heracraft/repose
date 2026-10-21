<!--
  One event on the project page (I-662, I-837), on Guide B's log grid:
  its time, its kind as a word with a 6px dot (amber for a question or
  input asked, red for an error, grey otherwise), the agent, and the
  summary held to two lines, which a click opens in full. The word
  carries the kind; the dot is a cue. guest_state_changed is the
  machine's state, drawn as the state list draws it. On a phone the time,
  kind and agent share the first line and the summary takes the second.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import StateDot from '$lib/components/StateDot.svelte';
	import { eventKind } from '$lib/events';
	import { clockTime, dateTime } from '$lib/format';
	import type { GuestState, ProjectEvent } from '$lib/api/types';

	let { event }: { event: ProjectEvent } = $props();

	const STATES: GuestState[] = [
		'running',
		'stopped',
		'creating',
		'building',
		'starting',
		'stopping',
		'restoring',
		'destroying',
		'destroyed',
		'error'
	];

	let info = $derived(eventKind(event.kind, event.agent));
	let machineState = $derived(
		event.kind === 'guest_state_changed' && (STATES as string[]).includes(event.summary)
			? (event.summary as GuestState)
			: undefined
	);
	let toneDot = $derived(
		info.tone === 'error' ? 'hk-dot--err' : info.tone === 'attention' ? 'hk-dot--warn' : ''
	);

	// The summary turns into a button only when two lines cut it short; a
	// button on every row would be a tab stop that does nothing.
	let open = $state(false);
	let clamped = $state(false);
	let text = $state<HTMLElement | undefined>(undefined);

	function measure() {
		if (text && !open) clamped = text.scrollHeight > text.clientHeight + 1;
	}

	onMount(() => {
		measure();
		const ro = new ResizeObserver(measure);
		if (text) ro.observe(text);
		return () => ro.disconnect();
	});
</script>

<li class="event-row" data-testid="event-row" data-kind={event.kind}>
	<time class="time" datetime={event.ts} title={dateTime(event.ts)}>{clockTime(event.ts)}</time>
	<span class="kind"><span class="hk-dot {toneDot} kdot" aria-hidden="true"></span>{info.label}</span>
	<span
		class="agent"
		title={event.window && event.window !== event.agent ? `window ${event.window}` : undefined}
		>{event.agent ?? ''}</span
	>
	<span class="summary">
		{#if machineState}
			<StateDot state={machineState} />
		{:else if clamped || open}
			<button
				type="button"
				class="more"
				class:line-clamp-2={!open}
				aria-expanded={open}
				onclick={() => (open = !open)}
				bind:this={text}>{event.summary}</button
			>
		{:else}
			<span class="line-clamp-2" bind:this={text}>{event.summary}</span>
		{/if}
	</span>
</li>

<style>
	/* Guide B 12.17: time, kind, agent, then the summary taking the rest. */
	.event-row {
		display: grid;
		grid-template-columns: auto auto minmax(0, 1fr);
		grid-template-areas:
			'time kind agent'
			'summary summary summary';
		column-gap: 0.75rem;
		row-gap: 0.25rem;
		align-items: baseline;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.time {
		grid-area: time;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text-3);
	}
	.kind {
		grid-area: kind;
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		min-width: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		white-space: nowrap;
	}
	.kdot {
		width: 6px;
		height: 6px;
	}
	.agent {
		grid-area: agent;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.summary {
		grid-area: summary;
		min-width: 0;
		font-size: 0.875rem;
		color: var(--hk-text);
		overflow-wrap: anywhere;
	}
	.more {
		width: 100%;
		padding: 0;
		border: 0;
		background: none;
		font: inherit;
		color: inherit;
		text-align: left;
		cursor: pointer;
	}
	@media (min-width: 640px) {
		.event-row {
			grid-template-columns: 3.5rem 6.5rem 5.5rem minmax(0, 1fr);
			grid-template-areas: 'time kind agent summary';
			row-gap: 0;
		}
	}
	@media (forced-colors: active) {
		.kdot {
			forced-color-adjust: none;
			background: CanvasText;
		}
	}
</style>
