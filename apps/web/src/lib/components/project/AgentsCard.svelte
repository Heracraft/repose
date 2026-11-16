<!--
  The agents running on the machine, from the newest signals (I-157,
  I-837): one hairline row each, its name, its tmux window and its state
  as a dot and a word (working, needs input or idle from an agent's
  hooks, running for one without hooks, I-694, any other word as the api
  sends it). An agent that needs input and has a question waiting links
  to it on this page. Each row's Say opens a field under it for a message
  to that agent, as `repose say` sends one (I-1144). Under them, how many
  SSH sessions and terminal clients are attached, and when this was read.
  The page draws this only while the machine runs and reports an agent.
-->
<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { agentStateLabel, relativeTime } from '$lib/format';
	import { agentTone } from '$lib/components/projects/agentTone';
	import { sayToAgent } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { sayRefusal, sayState } from '$lib/say';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import type { Question, Signals } from '$lib/api/types';

	let {
		projectId,
		signals,
		questions = [],
		updated
	}: {
		projectId: string;
		signals?: Signals;
		questions?: Question[];
		updated?: Date;
	} = $props();

	let agents = $derived(signals?.agents ?? []);

	function asking(agent: string, window: string): boolean {
		return questions.some((q) => q.agent === agent || (!!q.window && q.window === window));
	}

	const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

	// A message for one agent (I-1144). The field opens under the row whose
	// Say asked for it and sends to that row's window, so the api's
	// "several agents are running" refusal is unreachable from here. One
	// field at a time: the card is 18rem in the two-column Overview.
	let openRow = $state<string | undefined>(undefined);
	let draft = $state('');
	let sending = $state(false);

	function open(key: string) {
		openRow = key;
		draft = '';
	}

	function close(key: string) {
		openRow = undefined;
		draft = '';
		void focusAfterRender(`say-open-${key}`);
	}

	async function send(key: string, target: string) {
		const text = draft.trim();
		if (!text || sending) return;
		sending = true;
		try {
			const answer = await sayToAgent(projectId, text, target);
			toast.success(sayState(answer));
			sending = false;
			close(key);
		} catch (err) {
			sending = false;
			// The api passes the machine's own refusal through where it names
			// the windows, and that sentence names a CLI flag: a reason this
			// feature knows is said in the dashboard's words (say.ts).
			const own = sayRefusal(err);
			if (own) toast.error(own);
			else toastApiError(err, 'Could not send the message.');
		}
	}

	function onkeydown(e: KeyboardEvent, key: string, target: string) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			void send(key, target);
		} else if (e.key === 'Escape') {
			close(key);
		}
	}

	/** One line, growing to four, as a question's answer field does. */
	function grow(e: Event) {
		const field = e.currentTarget as HTMLTextAreaElement;
		field.style.height = 'auto';
		const style = getComputedStyle(field);
		const line = parseFloat(style.lineHeight) || 20;
		const edge = field.offsetHeight - field.clientHeight;
		const pad = parseFloat(style.paddingTop) * 2;
		const max = line * 4 + pad + edge;
		field.style.height = `${Math.min(field.scrollHeight + edge, max)}px`;
		field.style.overflowY = field.scrollHeight + edge > max ? 'auto' : 'hidden';
	}
</script>

<section class="hk-section" aria-labelledby="agents-title" data-testid="agents-card">
	<div class="hk-section-head">
		<h2 id="agents-title" class="hk-h2-app">Agents</h2>
	</div>
	<ul class="agents">
		{#each agents as a, i (`${a.agent}-${a.window}-${i}`)}
			{@const label = agentStateLabel(a.state)}
			{@const key = `${a.agent}-${a.window}-${i}`}
			{@const target = a.window || a.agent}
			<li class="agent">
				<div class="line">
					<span class="who">
						<span class="name">{a.agent}</span>
						{#if a.window && a.window !== a.agent}
							<span class="window">{a.window}</span>
						{/if}
					</span>
					{#if label}
						<svelte:element
							this={a.state === 'needs_input' && asking(a.agent, a.window) ? 'a' : 'span'}
							href={a.state === 'needs_input' && asking(a.agent, a.window)
								? '#questions'
								: undefined}
							class="state"
							data-agent-state={a.state}
							><span class="hk-dot hk-dot--{agentTone(a.state)}" aria-hidden="true"
							></span>{label}</svelte:element
						>
					{/if}
				</div>
				{#if openRow === key}
					<div class="say" data-testid="say-field" data-say-window={target}>
						<label class="hk-sr" for={`say-${key}`}>Message for {target}</label>
						<textarea
							id={`say-${key}`}
							class="hk-textarea field"
							rows="1"
							placeholder="Message"
							bind:value={draft}
							oninput={grow}
							onkeydown={(e) => onkeydown(e, key, target)}
							use:focusOnMount
						></textarea>
						<div class="acts">
							<!-- aria-disabled, not disabled: the button keeps its focus
							     and its name while the send runs (I-391, I-766). -->
							<button
								type="button"
								class="hk-btn-secondary hk-btn-sm"
								aria-disabled={sending || !draft.trim() ? 'true' : undefined}
								onclick={() => send(key, target)}>{sending ? 'Sending…' : 'Send'}</button
							>
							<button
								type="button"
								class="hk-btn-ghost hk-btn-sm"
								aria-disabled={sending ? 'true' : undefined}
								onclick={() => close(key)}>Cancel</button
							>
						</div>
					</div>
				{:else}
					<button
						type="button"
						id={`say-open-${key}`}
						class="hk-btn-ghost hk-btn-sm say-open"
						aria-label={`Say something to ${target}`}
						onclick={() => open(key)}>Say…</button
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
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.line {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
	}
	/* The opener sits under the row's name, its box pulled back to the
	   column's left edge. */
	.say-open {
		margin: 0.125rem 0 0 -0.625rem;
	}
	.say {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0.5rem;
		margin-top: 0.5rem;
	}
	.field {
		flex: 1 1 100%;
		min-width: 0;
		min-height: 0;
		height: 2.25rem;
		padding-block: 0.4375rem;
		line-height: 1.25rem;
		resize: none;
		overflow-y: hidden;
	}
	.acts {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
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
