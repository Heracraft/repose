<!--
  A guest's state as a small square and its word. The word carries the
  state; the square is a glance cue, so it is hidden from assistive tech
  and nothing depends on its colour. data-state and data-tone let the
  stylesheet draw the square for forced colours and reduced motion without
  this component knowing how.
-->
<script lang="ts">
	import type { GuestState } from '$lib/api/types';
	import Snowflake from './Snowflake.svelte';

	let {
		state,
		frozen = false
	}: {
		state: GuestState;
		/** A running machine frozen while idle (I-683): the snowflake and
		 * "frozen" in the frost colour, in place of the dot and "running". */
		frozen?: boolean;
	} = $props();

	type Tone = 'idle' | 'good' | 'busy' | 'error';

	const tone: Record<GuestState, Tone> = {
		running: 'good',
		stopped: 'idle',
		creating: 'busy',
		building: 'busy',
		starting: 'busy',
		stopping: 'busy',
		restoring: 'busy',
		destroying: 'busy',
		destroyed: 'idle',
		error: 'error'
	};

	const dotClass: Record<Tone, string> = {
		idle: 'dot',
		good: 'dot dot--good',
		busy: 'dot dot--busy',
		error: 'dot dot--error'
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
		class="frozen inline-flex items-center gap-1.5"
		data-state={state}
		data-frozen=""
		title={`${state}, frozen`}
		aria-label={`${state}, frozen`}
	>
		<Snowflake size={13} />
		<span>frozen</span>
	</span>
{:else}
	<span class="inline-flex items-center gap-1.5" data-state={state}>
		<span class={dotClass[tone[state]]} data-tone={tone[state]} aria-hidden="true"></span>
		<span>{state}</span>
	</span>
{/if}

<style>
	.frozen {
		color: var(--frost);
	}
</style>
