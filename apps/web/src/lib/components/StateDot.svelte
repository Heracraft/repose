<!--
  A guest's state as a 7px dot and its word (Guide B 5.3, I-837). The word
  carries the state; the dot is a glance cue, hidden from assistive tech.
  Colour only where the user is needed: amber for "needs you", red for
  "went wrong". Running is a filled ink dot, a step under way pulses with a
  halo (still under reduced motion), stopped is a hollow 3:1 ring. A frozen
  machine (I-683) shows the snowflake and "frozen" in the ice ink in
  place of the dot and "running". data-state and data-tone let
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
	<!-- The owner's look (I-683): the snowflake and "frozen" in place of
	     the dot and the state. The state itself is still `running` and
	     every page says so (I-826), so it is the title and the accessible
	     name here rather than a second word in a narrow cell -- without
	     it "running" appears nowhere on a frozen machine's row and the
	     states table has no entry to look "frozen" up in. -->
	<span
		class="state frozen"
		data-state={state}
		data-frozen=""
		title={`${state}, frozen`}
		aria-label={`${state}, frozen`}
	>
		<Snowflake size={13} />
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
		color: var(--hk-ice-ink);
	}
</style>
