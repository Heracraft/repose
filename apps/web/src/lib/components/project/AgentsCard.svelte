<!--
  The agents running on the machine, from the newest signals (I-157,
  I-837): one hairline row each, its name, its tmux window and its state
  as a dot and a word (working, needs input or idle from an agent's
  hooks, running for one without hooks, I-694, any other word as the api
  sends it). An agent that needs input and has a question waiting links
  to it on this page. Under them, how many SSH sessions and terminal
  clients are attached, and when this was read. The page draws this only
  while the machine runs and reports an agent.
-->
<script lang="ts">
	import { agentStateLabel, relativeTime } from '$lib/format';
	import { agentTone } from '$lib/components/projects/agentTone';
	import type { Question, Signals } from '$lib/api/types';

	let {
		signals,
		questions = [],
		updated
	}: {
		signals?: Signals;
		questions?: Question[];
		updated?: Date;
	} = $props();

	let agents = $derived(signals?.agents ?? []);

	function asking(agent: string, window: string): boolean {
		return questions.some((q) => q.agent === agent || (!!q.window && q.window === window));
	}

	const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
</script>

<section class="hk-section" aria-labelledby="agents-title" data-testid="agents-card">
	<div class="hk-section-head">
		<h2 id="agents-title" class="hk-h2-app">Agents</h2>
	</div>
	<ul class="agents">
		{#each agents as a, i (`${a.agent}-${a.window}-${i}`)}
			{@const label = agentStateLabel(a.state)}
			<li class="agent">
				<span class="who">
					<span class="name">{a.agent}</span>
					{#if a.window && a.window !== a.agent}
						<span class="window">{a.window}</span>
					{/if}
				</span>
				{#if label}
					<svelte:element
						this={a.state === 'needs_input' && asking(a.agent, a.window) ? 'a' : 'span'}
						href={a.state === 'needs_input' && asking(a.agent, a.window) ? '#questions' : undefined}
						class="state"
						data-agent-state={a.state}
						><span class="hk-dot hk-dot--{agentTone(a.state)}" aria-hidden="true"
						></span>{label}</svelte:element
					>
				{/if}
			</li>
		{/each}
	</ul>
	{#if signals}
		<p class="foot">
			{plural(signals.ssh_sessions, 'SSH session', 'SSH sessions')}, {plural(
				signals.tmux_clients,
				'tmux client',
				'tmux clients'
			)}{#if updated}<span aria-hidden="true">&nbsp;·&nbsp;</span>Updated {relativeTime(
					updated.toISOString()
				)}{/if}
		</p>
	{/if}
</section>

<style>
	.agents {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.agent {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.who {
		display: flex;
		min-width: 0;
		align-items: baseline;
		gap: 0.5rem;
	}
	.name {
		font-size: 0.875rem;
		font-weight: 500;
	}
	.window {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.state {
		display: inline-flex;
		flex: none;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	a.state {
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	a.state:hover {
		color: var(--hk-text);
	}
	.foot {
		margin: 0.625rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
</style>
