<!--
  The agents running on the machine, from the newest signals (I-157): one
  row each, its tmux window and its state. An agent that needs input and
  has a question waiting links to it on this page. Under them, how many
  SSH sessions and terminal clients are attached, and when this was read.
-->
<script lang="ts">
	import { agentStateLabel, relativeTime } from '$lib/format';
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

<section class="card" aria-labelledby="agents-title" data-testid="agents-card">
	<h2 id="agents-title" class="text-xl font-semibold">Agents</h2>
	{#if agents.length === 0}
		<p class="mt-3 text-sm text-ink-muted">No agents running</p>
	{:else}
		<ul class="mt-2">
			{#each agents as a (a.window)}
				{@const label = agentStateLabel(a.state)}
				<li class="row flex items-center justify-between gap-3 py-2.5 text-sm">
					<span class="flex min-w-0 items-baseline gap-2">
						<span class="font-medium">{a.agent}</span>
						{#if a.window && a.window !== a.agent}
							<span class="truncate font-mono text-compact text-ink-muted">{a.window}</span>
						{/if}
					</span>
					{#if a.state === 'needs_input' && asking(a.agent, a.window)}
						<a href="#questions" class="badge badge--warn hover:underline">{label}</a>
					{:else if a.state === 'needs_input'}
						<span class="badge badge--warn">{label}</span>
					{:else if label}
						<span class="badge" class:badge--new={a.state === 'working'}>{label}</span>
					{:else}
						<span class="text-xs text-ink-faint">no state</span>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
	{#if signals}
		<p class="mt-3 border-t pt-3 text-xs text-ink-muted tabular-nums border-rule">
			{plural(signals.ssh_sessions, 'SSH session', 'SSH sessions')}, {plural(
				signals.tmux_clients,
				'tmux client',
				'tmux clients'
			)}
		</p>
	{/if}
	{#if updated}
		<p class="mt-1 text-xs text-ink-faint">Updated {relativeTime(updated.toISOString())}</p>
	{/if}
</section>
