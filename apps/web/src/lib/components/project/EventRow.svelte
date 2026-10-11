<!--
  One event on the project page (I-662): its time, its kind as a small
  square and a word, the agent and its window, and the summary held to two
  lines, which a click opens in full. The kind's colour is a cue; the word
  carries it. guest_state_changed is the machine's state, drawn as the
  state list draws it. From sm up the four sit in columns, so a page of
  rows reads down each one; on a phone the time, kind and agent share the
  first line and the summary takes the second.
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
	<time
		class="font-mono text-compact text-ink-faint tabular-nums"
		datetime={event.ts}
		title={dateTime(event.ts)}>{clockTime(event.ts)}</time
	>
	<span class="kind tone-{info.tone} flex min-w-0 items-center gap-1.5 text-xs">
		<span class="sq" aria-hidden="true"></span>
		<span class="truncate">{info.label}</span>
	</span>
	<span class="flex min-w-0 items-baseline gap-1.5 text-xs">
		{#if event.agent}
			<span class="badge">{event.agent}</span>
			{#if event.window && event.window !== event.agent}
				<span class="truncate font-mono text-ink-muted">{event.window}</span>
			{/if}
		{/if}
	</span>
	<span class="summary min-w-0 text-sm">
		{#if machineState}
			<StateDot state={machineState} />
		{:else if clamped || open}
			<button
				type="button"
				class="w-full cursor-pointer text-left break-words hover:text-ink"
				class:line-clamp-2={!open}
				aria-expanded={open}
				onclick={() => (open = !open)}
				bind:this={text}>{event.summary}</button
			>
		{:else}
			<span class="line-clamp-2 break-words" bind:this={text}>{event.summary}</span>
		{/if}
	</span>
</li>

<style>
	/* A row is about 36px: 8px above and below a 20px line. */
	.event-row {
		display: grid;
		grid-template-columns: auto auto minmax(0, 1fr);
		grid-template-areas:
			'time kind agent'
			'summary summary summary';
		column-gap: 0.75rem;
		row-gap: 0.25rem;
		align-items: baseline;
		padding-block: 0.5rem;
		border-top: 1px solid var(--rule);
	}
	.event-row > :nth-child(1) {
		grid-area: time;
	}
	.event-row > :nth-child(2) {
		grid-area: kind;
	}
	.event-row > :nth-child(3) {
		grid-area: agent;
	}
	.event-row > :nth-child(4) {
		grid-area: summary;
	}
	@media (width >= 40rem) {
		.event-row {
			grid-template-columns: 3.5rem 6.5rem 6rem minmax(0, 1fr);
			grid-template-areas: 'time kind agent summary';
			row-gap: 0;
		}
	}
	.sq {
		display: inline-block;
		width: 8px;
		height: 8px;
		flex-shrink: 0;
		border-radius: 1px;
		background-color: var(--ink-faint);
		align-self: center;
	}
	.kind {
		color: var(--ink-muted);
	}
	.tone-error {
		color: var(--color-red-700);
	}
	.tone-error .sq {
		background-color: var(--color-red-600);
	}
	.tone-attention {
		color: var(--color-amber-800);
	}
	.tone-attention .sq {
		background-color: var(--color-amber-500);
	}
	.tone-ok .sq {
		background-color: var(--color-emerald-500);
	}
	@media (prefers-color-scheme: dark) {
		.tone-error {
			color: var(--color-red-300);
		}
		.tone-error .sq {
			background-color: var(--color-red-500);
		}
		.tone-attention {
			color: var(--color-amber-300);
		}
		.tone-attention .sq {
			background-color: var(--color-amber-400);
		}
		.tone-ok .sq {
			background-color: var(--color-emerald-300);
		}
	}
	/* Forced colours erase the squares' fills; the word still says it. */
	@media (forced-colors: active) {
		.sq {
			forced-color-adjust: none;
			background-color: CanvasText;
		}
	}
</style>
