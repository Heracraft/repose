<!--
  A guest's state as a 7px dot and its word (Guide B 5.3, I-837). The word
  carries the state; the dot is a glance cue, hidden from assistive tech.
  Colour only where the user is needed: amber for "needs you", red for
  "went wrong". Running is a filled ink dot, a step under way pulses with a
  halo (still under reduced motion), stopped is a hollow 3:1 ring. A frozen
  machine (I-683) shows the snowflake and "frozen" in the secondary text
  colour in place of the dot and "running". data-state and data-tone let
  a stylesheet or a test read the state without this component knowing.
-->
<script lang="ts">
	import type { GuestState } from '$lib/api/types';
	import Snowflake from './Snowflake.svelte';

	let {
		state,
		frozen = false
	}: {
		state: GuestState;
		/** A running machine frozen while idle (I-683). */
		frozen?: boolean;
	} = $props();

	type Tone = 'idle' | 'on' | 'busy' | 'err';

	const tone: Record<GuestState, Tone> = {
		running: 'on',
		stopped: 'idle',
		creating: 'busy',
		building: 'busy',
		starting: 'busy',
		stopping: 'busy',
		restoring: 'busy',
		destroying: 'busy',
		destroyed: 'idle',
		error: 'err'
	};
</script>

{#if frozen}
	<span class="state frozen" data-state={state} data-frozen="">
		<Snowflake size={12} />
		<span>frozen</span>
	</span>
{:else}
	<span class="state" data-state={state}>
		<span class="hk-dot hk-dot--{tone[state]}" data-tone={tone[state]} aria-hidden="true"></span>
		<span>{state}</span>
	</span>
{/if}

<style>
	.state {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}
	.frozen {
		gap: 0.375rem;
		color: var(--hk-text-2);
	}
</style>
