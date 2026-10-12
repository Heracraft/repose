<!--
  A project's agents as small chips (signals.agents, I-617): the agent's
  name, with an emerald dot while it works, amber "needs input" when it
  waits on you, muted when idle. SSH sessions follow as a count. With
  neither, "none" in faint text. The state is also in each chip's name for
  a screen reader, so the dot is never the only sign of it.
-->
<script lang="ts">
	import { agentStateLabel } from '$lib/format';
	import type { AgentSignal } from '$lib/api/types';

	let { agents = [], ssh = 0 }: { agents?: AgentSignal[]; ssh?: number } = $props();

	function waiting(a: AgentSignal): boolean {
		return a.state === 'needs_input' || a.state === 'question';
	}
</script>

{#if agents.length === 0 && ssh === 0}
	<span class="text-ink-faint">none</span>
{:else}
	<span class="chips">
		{#each agents as a, i (`${a.agent}-${a.window}-${i}`)}
			{@const word = agentStateLabel(a.state)}
			{#if waiting(a)}
				<span class="badge badge--warn chip" title={a.window ? `window ${a.window}` : undefined}
					>{a.agent} · needs input</span
				>
			{:else}
				<span
					class="badge chip"
					class:chip--working={a.state === 'working'}
					title={[word, a.window ? `window ${a.window}` : ''].filter(Boolean).join(', ') ||
						undefined}
				>
					{#if a.state === 'working'}<span class="chip-dot" aria-hidden="true"
						></span>{/if}{a.agent}{#if word}<span class="sr-only">, {word}</span>{/if}
				</span>
			{/if}
		{/each}
		{#if ssh > 0}
			<span class="text-xs text-ink-muted tabular-nums">{ssh} SSH</span>
		{/if}
	</span>
{/if}

<style>
	.chips {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.375rem;
	}
	.chip {
		gap: 0.3125rem;
		white-space: nowrap;
	}
	.chip--working {
		color: var(--ink);
	}
	.chip-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		border-radius: 1px;
		background-color: var(--color-emerald-500);
	}
	@media (prefers-color-scheme: dark) {
		.chip-dot {
			background-color: var(--color-emerald-300);
		}
	}
	@media (forced-colors: active) {
		.chip-dot {
			forced-color-adjust: none;
			background-color: CanvasText;
		}
	}
</style>
