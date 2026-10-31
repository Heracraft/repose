<!--
  A project's agents on one line of the list (signals.agents, I-617,
  I-837): each name in mono with a 6px dot for its state (agentTone), two
  at most, then "+N" for the rest. The state is also in each name's
  accessible text, so the dot is never the only sign of it. An agent
  waiting on you keeps a neutral dot when its row already carries the
  questions badge: one amber mark per fact. Nothing at all with no agent.
-->
<script lang="ts">
	import { agentStateLabel } from '$lib/format';
	import type { AgentSignal } from '$lib/api/types';
	import { agentTone } from './agentTone';

	let {
		agents = [],
		asked = false,
		shown = 2
	}: {
		agents?: AgentSignal[];
		/** The row already shows its questions badge. */
		asked?: boolean;
		/** Names drawn before "+N". */
		shown?: number;
	} = $props();

	let first = $derived(agents.slice(0, shown));
	let rest = $derived(agents.length - first.length);

	function tone(a: AgentSignal): string {
		const t = agentTone(a.state);
		return t === 'warn' && asked ? 'idle' : t;
	}
</script>

{#if agents.length > 0}
	<span class="chips">
		{#each first as a, i (`${a.agent}-${a.window}-${i}`)}
			{@const word = agentStateLabel(a.state)}
			<span
				class="chip"
				data-agent-state={a.state}
				title={[word, a.window && a.window !== a.agent ? `window ${a.window}` : '']
					.filter(Boolean)
					.join(', ') || undefined}
			>
				<span class="hk-dot hk-dot--{tone(a)} chip-dot" aria-hidden="true"
				></span>{a.agent}{#if word}<span class="hk-sr">, {word}</span>{/if}
			</span>
		{/each}
		{#if rest > 0}
			<span
				class="more"
				title={agents
					.slice(shown)
					.map((a) => a.agent)
					.join(', ')}>+{rest}<span class="hk-sr"> more</span></span
			>
		{/if}
	</span>
{/if}

<style>
	.chips {
		display: inline-flex;
		align-items: center;
		/* Was 0.75rem, which with two mono names, their dots and a "+N"
		   ran past 19% of a 600px column and spilled over Disk (I-896). */
		gap: 0.5rem;
		max-width: 100%;
		min-width: 0;
		white-space: nowrap;
		overflow: hidden;
	}
	.chip {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--hk-text);
	}
	.chip-dot {
		width: 6px;
		height: 6px;
	}
	.more {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
</style>
