<!--
  The conversation with the machine's agents (DECISIONS I-1604, amending
  I-1144, I-1220): what the owner sent (`GET /projects/:id/says`) and what
  the agents said back (their turn-ended, notify, question, permission
  and error events), oldest first, with the field to write the next
  message under it. A message goes into the agent's own terminal as
  `repose say` sends it, so the session the owner attaches to has it;
  the line under it says what became of it. A reply is the agent's last
  message as its own hook reported it, which is the first 200 characters
  of it; the terminal has the rest.

  The field goes to one agent: the only one, else the one the owner
  picked, else the one that spoke last. The phone gets the same page,
  and a notification's link lands here.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { listEventsPage, listSays, sayToAgent } from '$lib/api/client';
	import { PollFailure, type PollGroup } from '$lib/api/toast';
	import { errorText } from '$lib/api/errors';
	import { pollWhileVisible } from '$lib/poll';
	import { agentStateLabel, clockTime, dateTime } from '$lib/format';
	import { agentTone } from '$lib/components/projects/agentTone';
	import { sayRefusal, TEXT_CAP } from '$lib/say';
	import {
		AGENT_KINDS,
		THREAD_READ,
		THREAD_SHOWN,
		agentVerb,
		mergeThread,
		pickWindow,
		sayLine,
		type ThreadItem
	} from '$lib/thread';
	import { noTerminal, type AgentSignal, type Project, type ProjectEvent, type Say } from '$lib/api/types';

	let {
		project,
		initial,
		pollFailures
	}: {
		project: Project;
		/** The Overview's first load reads both halves, so the section paints with the rest. */
		initial: { says: Say[]; events: ProjectEvent[] };
		pollFailures?: PollGroup;
	} = $props();

	// svelte-ignore state_referenced_locally
	const failure = new PollFailure('Could not load messages.', pollFailures);

	// svelte-ignore state_referenced_locally
	let says = $state<Say[]>(initial.says);
	// svelte-ignore state_referenced_locally
	let events = $state<ProjectEvent[]>(initial.events);
	/** A message whose POST is still out, drawn as `sending`. */
	let pending = $state<{ id: string; text: string; window: string; at: string } | undefined>();
	let showAll = $state(false);

	let thread = $derived.by((): ThreadItem[] => {
		const items = mergeThread(says, events);
		if (pending) {
			items.push({
				from: 'you',
				id: pending.id,
				at: pending.at,
				text: pending.text,
				window: pending.window,
				state: 'sending'
			});
		}
		return items;
	});
	let drawn = $derived(showAll ? thread : thread.slice(-THREAD_SHOWN));

	let running = $derived(project.state === 'running');
	let agents = $derived<AgentSignal[]>(running ? (project.signals?.agents ?? []) : []);
	// A message is typed into a terminal; an agent outside one (Claude
	// Desktop's, I-1676) is listed above and cannot take it.
	let typable = $derived(agents.filter((a) => !noTerminal(a)));
	let chosen = $state<string | undefined>(undefined);
	let target = $derived(pickWindow(typable, thread, chosen));
	let targetAgent = $derived(typable.find((a) => (a.window || a.agent) === target));

	let draft = $state('');
	let sending = $state(false);
	let error = $state<string | undefined>(undefined);
	let field = $state<HTMLTextAreaElement | undefined>();

	async function read() {
		try {
			const [s, e] = await Promise.all([
				listSays(project.id, THREAD_READ),
				listEventsPage(project.id, { limit: THREAD_READ, kinds: [...AGENT_KINDS] })
			]);
			says = s;
			events = e.events;
			failure.ok();
		} catch (err) {
			failure.fail(err);
		}
	}

	// After a send the reply is what the owner waits for, so the thread
	// is read every 3 seconds for a minute rather than on the 10-second
	// poll.
	let quick: ReturnType<typeof setInterval> | undefined;
	function readQuickly() {
		clearInterval(quick);
		const until = Date.now() + 60_000;
		quick = setInterval(() => {
			if (Date.now() > until) clearInterval(quick);
			else void read();
		}, 3_000);
	}

	onMount(() => {
		// The first load already has them; the poll's first tick waits its turn.
		let first = true;
		const stop = pollWhileVisible(() => {
			if (first) {
				first = false;
				return;
			}
			return read();
		});
		return () => {
			stop();
			clearInterval(quick);
		};
	});

	async function send() {
		const text = draft.trim();
		if (!text || sending || !target) return;
		sending = true;
		error = undefined;
		pending = { id: `pending-${Date.now()}`, text, window: target, at: new Date().toISOString() };
		draft = '';
		void grow();
		try {
			const answer = await sayToAgent(project.id, text, target);
			says = [answer, ...says.filter((s) => s.id !== answer.id)];
			if (answer.replaced) {
				says = says.map((s) =>
					s.id !== answer.id && s.window === answer.window && s.state === 'waiting'
						? { ...s, state: 'replaced' }
						: s
				);
			}
			readQuickly();
		} catch (err) {
			// The text comes back to the field, so nothing typed is lost.
			draft = text;
			void grow();
			error = sayRefusal(err) ?? errorText(err, 'Could not send the message.');
		} finally {
			pending = undefined;
			sending = false;
		}
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			void send();
		}
	}

	/** One line, growing to eight. */
	async function grow() {
		await tick();
		if (!field) return;
		field.style.height = 'auto';
		const style = getComputedStyle(field);
		const line = parseFloat(style.lineHeight) || 20;
		const pad = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
		const max = line * 8 + pad;
		field.style.height = `${Math.min(field.scrollHeight, max)}px`;
		field.style.overflowY = field.scrollHeight > max ? 'auto' : 'hidden';
	}

	/** Why the field is off, or undefined when it takes a message. */
	let off = $derived(
		!running
			? 'The machine is not running.'
			: agents.length === 0
				? 'No agent is running on this machine.'
				: typable.length === 0
					? `${[...new Set(agents.map((a) => a.agent))].join(' and ')} runs outside a terminal repose can type into.`
					: undefined
	);

	/** The agent named on an item, with its window when that differs. */
	function whoLabel(it: Extract<ThreadItem, { from: 'agent' }>): string {
		return it.window && it.window !== it.who ? `${it.who} · ${it.window}` : it.who;
	}
</script>

<!-- Nothing to read and nobody to write to: a machine that never ran an
     agent has no conversation to draw. -->
{#if thread.length > 0 || !off}
	<section
		class="hk-section convo"
		aria-labelledby="convo-title"
		id="messages"
		data-testid="conversation"
	>
		<div class="hk-section-head">
			<h2 id="convo-title" class="hk-h2-app">Messages</h2>
		</div>

		{#if thread.length > 0}
			{#if thread.length > drawn.length}
				<button
					type="button"
					class="hk-btn-ghost hk-btn-sm earlier"
					onclick={() => (showAll = true)}>Earlier messages</button
				>
			{/if}
			<ol class="thread" aria-label="Messages">
				{#each drawn as it (it.id)}
					<li class="item item--{it.from}" data-from={it.from}>
						<div class="meta">
							<span class="who">{it.from === 'you' ? 'You' : whoLabel(it)}</span>
							{#if it.from === 'agent' && agentVerb(it.kind)}
								<span class="verb" data-kind={it.kind}>{agentVerb(it.kind)}</span>
							{/if}
							<time datetime={it.at} title={dateTime(it.at)}>{clockTime(it.at)}</time>
						</div>
						<p class="text">{it.text}</p>
						{#if it.from === 'you'}
							<p class="state" data-state={it.state}>{sayLine(it)}</p>
						{:else if it.kind === 'agent_question'}
							<a class="state hk-link" href="#questions">Answer above</a>
						{/if}
					</li>
				{/each}
			</ol>
		{/if}

		{#if targetAgent && targetAgent.state === 'working' && thread.at(-1)?.from === 'you'}
			<p class="working" aria-live="polite">
				<span class="hk-dot hk-dot--{agentTone('working')}" aria-hidden="true"></span>{target} is working
			</p>
		{/if}

		<form
			class="composer"
			class:composer--off={!!off}
			onsubmit={(e) => {
				e.preventDefault();
				void send();
			}}
		>
			<label class="hk-sr" for="convo-field">Message{target ? ` for ${target}` : ''}</label>
			<textarea
				id="convo-field"
				class="field"
				rows="1"
				maxlength={TEXT_CAP}
				placeholder={off ?? `Message ${target}`}
				disabled={!!off}
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? 'convo-error' : undefined}
				bind:this={field}
				bind:value={draft}
				oninput={() => void grow()}
				{onkeydown}
			></textarea>
			<div class="bar">
				<div class="to">
					{#if typable.length > 1}
						<label for="convo-to" class="hk-sr">To</label>
						<select id="convo-to" class="to-select" bind:value={() => target, (v) => (chosen = v)}>
							{#each typable as a (a.window || a.agent)}
								<option value={a.window || a.agent}
									>{a.window || a.agent}{agentStateLabel(a.state)
										? ` · ${agentStateLabel(a.state)}`
										: ''}</option
								>
							{/each}
						</select>
					{:else if targetAgent}
						<span class="to-one">
							<span class="hk-dot hk-dot--{agentTone(targetAgent.state)}" aria-hidden="true"
							></span>{target}{agentStateLabel(targetAgent.state)
								? ` · ${agentStateLabel(targetAgent.state)}`
								: ''}
						</span>
					{/if}
				</div>
				<!-- aria-disabled, not disabled: the button keeps its focus and
			     its name while the send runs (I-391, I-766). -->
				<button
					type="submit"
					class="hk-btn-secondary hk-btn-sm"
					disabled={!!off}
					aria-disabled={!off && (sending || !draft.trim()) ? 'true' : undefined}>Send</button
				>
			</div>
		</form>
		{#if error}
			<p id="convo-error" class="hk-field-error" role="alert" data-testid="convo-error">{error}</p>
		{/if}
	</section>
{/if}

<style>
	.thread {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.earlier {
		margin: 0 0 0.5rem -0.625rem;
	}
	.item {
		padding: 0.75rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.item:first-child {
		border-top: 0;
		padding-top: 0;
	}
	.meta {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		font-size: 0.8125rem;
	}
	.who {
		font-weight: 500;
		color: var(--hk-text);
	}
	.item--agent .who {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	.verb {
		color: var(--hk-text-3);
	}
	.verb[data-kind='needs_input'],
	.verb[data-kind='agent_question'] {
		color: var(--hk-busy);
	}
	.verb[data-kind='error'] {
		color: var(--hk-err);
	}
	time {
		margin-left: auto;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.text {
		margin: 0.25rem 0 0;
		max-width: 44rem;
		font-size: 0.9375rem;
		line-height: 1.5;
		color: var(--hk-text);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	/* The owner's own words sit on the surface the field has, so the two
	   voices read apart without bubbles. */
	.item--you .text {
		display: inline-block;
		padding: 0.5rem 0.75rem;
		border-radius: var(--hk-radius-sm);
		background: var(--hk-surface-2);
	}
	.state {
		display: block;
		margin: 0.25rem 0 0;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.state[data-state='failed'] {
		color: var(--hk-err);
	}
	.working {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0.25rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}

	/* ---- The field ----
	   One box, the text above and what it goes to and Send under it. */
	.composer {
		margin-top: 0.75rem;
		border: 1px solid var(--hk-control-edge);
		border-radius: var(--hk-radius);
		background: var(--hk-surface);
	}
	.composer:focus-within {
		border-color: var(--hk-accent);
		box-shadow: 0 0 0 1px var(--hk-accent);
	}
	.composer--off {
		border-color: var(--hk-border);
		background: var(--hk-disabled-bg);
	}
	.field {
		display: block;
		width: 100%;
		min-height: 2.75rem;
		padding: 0.75rem 0.75rem 0.25rem;
		border: 0;
		background: transparent;
		color: var(--hk-text);
		font: inherit;
		font-size: 0.9375rem;
		line-height: 1.375rem;
		resize: none;
		overflow-y: hidden;
	}
	.field:focus {
		outline: none;
		box-shadow: none;
	}
	.field::placeholder {
		color: var(--hk-text-3);
	}
	.bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.25rem 0.5rem 0.5rem 0.75rem;
	}
	.to {
		min-width: 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.to-one {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-family: var(--hk-font-mono);
	}
	.to-select {
		max-width: 100%;
		padding: 0.25rem 0.5rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius-sm);
		background: var(--hk-surface);
		color: var(--hk-text);
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.field {
			font-size: 1rem;
		}
		.to-select {
			min-height: 2.75rem;
		}
	}
</style>
