<!--
  One question an agent waits on (repose-ask, I-245), as "Waiting on you"
  draws it on /projects and on a project page (I-693, I-837): a hairline
  row of two lines. The question first, held to 3 lines (4 on a phone)
  with "Show all" only when that cuts it; when it fits one line at the
  row's width, the option buttons sit at its right end. Then a 13px meta
  line (project, agent, window, age, the expiry only in its last 10
  minutes) with the answer on the same line from 768px: the options, or a
  field that grows to 4 lines (Enter sends, Shift+Enter breaks a line)
  and Send, then Dismiss flush right. A closed question is one line saying
  what closed it, for as long as the card keeps it.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { dateTime } from '$lib/format';
	import { askedLine, closedLine, expiryLine } from '$lib/question-view';
	import type { Question } from '$lib/api/types';

	let {
		question: q,
		now,
		projectHref,
		busy = false,
		note = '',
		onanswer,
		ondismiss
	}: {
		question: Question;
		/** The page's 30-second clock, in ms. */
		now: number;
		/** When set, the project's name leads the meta line as a link to it. */
		projectHref?: string;
		/** An answer or a dismissal of this question is on its way. */
		busy?: boolean;
		/** Why the last answer was refused, under the controls. */
		note?: string;
		onanswer: (answer: string) => void;
		ondismiss: () => void;
	} = $props();

	let pending = $derived(q.state === 'pending');
	let closed = $derived(pending ? undefined : closedLine(q));
	let asked = $derived(askedLine(q, now));
	let expiry = $derived(expiryLine(q, now));
	let windowName = $derived(q.window && q.window !== q.agent ? q.window : '');

	// The question turns "Show all" on only when the clamp cuts it.
	let open = $state(false);
	let clamped = $state(false);
	let textEl = $state<HTMLElement | undefined>(undefined);
	let rowEl = $state<HTMLElement | undefined>(undefined);
	let lineEl = $state<HTMLElement | undefined>(undefined);
	let optsEl = $state<HTMLElement | undefined>(undefined);
	/** The options sit at the end of the question's line: it fits one line beside them. */
	let inline = $state(false);

	let canvas: HTMLCanvasElement | undefined;
	/** The question's width on one line, from its font: the same wherever the options sit. */
	function textWidth(): number {
		if (!textEl) return Infinity;
		canvas ??= document.createElement('canvas');
		const ctx = canvas.getContext('2d');
		if (!ctx) return Infinity;
		const s = getComputedStyle(textEl);
		ctx.font = `${s.fontWeight} ${s.fontSize} ${s.fontFamily}`;
		return ctx.measureText(q.text.trim()).width;
	}

	/** The options' width, kept: it is the same on either line, and the group is redrawn on a move. */
	let optsWidth = 0;
	function measure() {
		if (textEl && !open) clamped = textEl.scrollHeight > textEl.clientHeight + 1;
		if (optsEl?.isConnected && optsEl.offsetWidth > 0) optsWidth = optsEl.offsetWidth;
		const wide = window.matchMedia('(min-width: 768px)').matches;
		inline =
			wide &&
			q.options.length > 0 &&
			!q.text.trim().includes('\n') &&
			!!lineEl &&
			optsWidth > 0 &&
			textWidth() + 24 + optsWidth <= lineEl.clientWidth;
	}
	onMount(() => {
		measure();
		const ro = new ResizeObserver(() => measure());
		if (rowEl) ro.observe(rowEl);
		return () => ro.disconnect();
	});

	// The free answer: one line, growing to four, then it scrolls.
	let draft = $state('');
	let field = $state<HTMLTextAreaElement | undefined>(undefined);
	function grow() {
		if (!field) return;
		field.style.height = 'auto';
		const line = parseFloat(getComputedStyle(field).lineHeight) || 20;
		const edge = field.offsetHeight - field.clientHeight;
		const pad = parseFloat(getComputedStyle(field).paddingTop) * 2;
		const max = line * 4 + pad + edge;
		field.style.height = `${Math.min(field.scrollHeight + edge, max)}px`;
		field.style.overflowY = field.scrollHeight + edge > max ? 'auto' : 'hidden';
	}
	function send() {
		const v = draft.trim();
		if (!v || busy) return;
		onanswer(v);
	}
	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			send();
		} else if (e.key === 'Escape') {
			field?.blur();
		}
	}
	async function toggle() {
		open = !open;
		await tick();
		if (!open) measure();
	}
	const idFor = (what: string) => `q-${what}-${q.id}`;
</script>

{#snippet options()}
	<!-- Answers of equal weight, so secondary buttons, never a row of
	     primaries; the label is the agent's own word. -->
	<div class="qrow-options" role="group" aria-label="Answers" bind:this={optsEl}>
		{#each q.options as o, i (o)}
			<button
				type="button"
				class="hk-btn-secondary hk-btn-sm"
				data-qrow-first={i === 0 ? '' : undefined}
				disabled={busy}
				onclick={() => onanswer(o)}>{o}</button
			>
		{/each}
	</div>
{/snippet}

{#snippet dismiss()}
	<button
		type="button"
		class="hk-btn-ghost hk-btn-sm qrow-dismiss"
		disabled={busy}
		title="The agent stops waiting"
		aria-label={`Dismiss the question from ${q.agent}`}
		onclick={ondismiss}>Dismiss</button
	>
{/snippet}

{#if closed}
	<li class="qrow qrow--closed" data-testid="question" data-qid={q.id} data-state={q.state}>
		<!-- The question stays beside the outcome, so with two open the
		     reader can tell which one took the answer (review r1 dash-r1-8). -->
		<!-- eslint-disable svelte/no-navigation-without-resolve -- the card builds projectHref with resolve() -->
		<p class="qrow-closed-line">
			<span class="qrow-closed-q"
				>{#if projectHref}<a href={projectHref} class="qrow-project">{q.project}</a><span
						aria-hidden="true">&nbsp;·&nbsp;</span
					>{/if}<span class="qrow-closed-text">{q.text}</span></span
			>
			<span class="qrow-closed-outcome" data-testid="question-outcome">
				{#if closed.answer !== undefined}
					Answered <span class="qrow-answer-word">{closed.answer}</span>{closed.text.slice(
						'Answered '.length + closed.answer.length
					)}
				{:else}
					{closed.text}
				{/if}
			</span>
		</p>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	</li>
{:else}
	<li class="qrow" data-testid="question" data-qid={q.id} data-state="pending" bind:this={rowEl}>
		<div class="qrow-line" bind:this={lineEl}>
			<p id={idFor('text')} class="qrow-text" class:qrow-text--clamped={!open} bind:this={textEl}>
				{q.text}
			</p>
			{#if inline}{@render options()}{/if}
		</div>
		{#if clamped || open}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm qrow-more"
				aria-expanded={open}
				aria-controls={idFor('text')}
				onclick={toggle}>{open ? 'Show less' : 'Show all'}</button
			>
		{/if}
		<div class="qrow-foot">
			<p class="qrow-meta">
				{#if projectHref}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- the card builds it with resolve() -->
					<a href={projectHref} class="qrow-project">{q.project}</a><span aria-hidden="true"
						>&nbsp;·&nbsp;</span
					>
				{/if}<span class="qrow-agent">{q.agent}</span><span class="hk-sr">&nbsp;asks</span
				>{#if windowName}<span class="qrow-window"
						><span aria-hidden="true">&nbsp;·&nbsp;</span><span class="hk-sr">in window&nbsp;</span
						>{windowName}</span
					>{/if}<span aria-hidden="true">&nbsp;·&nbsp;</span><time
					datetime={q.created_at}
					title={dateTime(q.created_at)}>{asked}</time
				>{#if expiry}<span class="qrow-expiry" title={dateTime(q.expires_at)}
						><span aria-hidden="true">&nbsp;·&nbsp;</span>{expiry}</span
					>{/if}
			</p>
			<div class="qrow-answer">
				{#if q.options.length > 0}
					{#if !inline}{@render options()}{/if}
				{:else}
					<label class="hk-sr" for={idFor('answer')}>Your answer</label>
					<textarea
						id={idFor('answer')}
						class="hk-textarea qrow-field"
						rows="1"
						placeholder="Answer"
						data-qrow-first
						bind:this={field}
						bind:value={draft}
						oninput={grow}
						{onkeydown}
						disabled={busy}
						aria-invalid={note ? 'true' : undefined}
						aria-describedby={note ? idFor('note') : undefined}
					></textarea>
					<button
						type="button"
						class="hk-btn-secondary hk-btn-sm qrow-send"
						disabled={busy || !draft.trim()}
						onclick={send}>{busy ? 'Sending…' : 'Send'}</button
					>
				{/if}
				{@render dismiss()}
			</div>
		</div>
		{#if note}
			<p id={idFor('note')} class="hk-field-error">{note}</p>
		{/if}
	</li>
{/if}

<style>
	.qrow {
		padding: 0.875rem 0;
	}
	.qrow--closed {
		padding: 0.625rem 0;
	}
	.qrow-closed-line {
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
		margin: 0;
		font-size: 0.875rem;
		color: var(--hk-text-3);
	}
	.qrow-closed-q {
		flex: 0 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.qrow-closed-q .qrow-project {
		font-size: 0.8125rem;
	}
	.qrow-closed-outcome {
		flex: none;
		max-width: 60%;
		overflow-wrap: anywhere;
	}
	.qrow-answer-word {
		font-weight: 500;
		color: var(--hk-text);
	}
	.qrow-line {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1.5rem;
	}
	/* About 90 characters a line at 15px; the agent's own line breaks kept. */
	.qrow-text {
		margin: 0;
		min-width: 0;
		max-width: 48rem;
		font-size: 0.9375rem;
		line-height: 1.5rem;
		color: var(--hk-text);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.qrow-text--clamped {
		display: -webkit-box;
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 3;
		line-clamp: 3;
		overflow: hidden;
	}
	.qrow-more {
		margin: 0.125rem 0 0 -0.625rem;
	}
	.qrow-foot {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		margin-top: 0.375rem;
	}
	.qrow-meta {
		margin: 0;
		min-width: 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.qrow-project {
		font-weight: 500;
		color: var(--hk-text);
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	.qrow-project:hover {
		text-decoration-color: currentColor;
	}
	.qrow-agent {
		font-family: var(--hk-font-mono);
		color: var(--hk-text-2);
	}
	/* The last 10 minutes: ink and weight, since the colours belong to
	   badges, dots and banners. */
	.qrow-expiry {
		font-weight: 500;
		color: var(--hk-text);
	}
	.qrow-answer {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0.5rem;
	}
	.qrow-options {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	/* Beside the question the buttons overhang its 24px line by 3px each
	   way, so the row keeps the line's height. */
	.qrow-line .qrow-options {
		margin-block: -0.1875rem;
	}
	.qrow-options :global(.hk-btn-secondary) {
		min-width: 3.5rem;
	}
	/* One line at a small button's height, growing to four. */
	.qrow-field {
		flex: 1 1 14rem;
		min-width: 0;
		max-width: 28rem;
		min-height: 0;
		height: 1.875rem;
		padding-block: 0.25rem;
		line-height: 1.25rem;
		resize: none;
		overflow-y: hidden;
	}
	.qrow-dismiss {
		margin-left: auto;
		margin-right: -0.625rem;
	}
	/* From 768px the answer shares the meta line, Dismiss flush right. */
	@media (min-width: 768px) {
		.qrow-foot {
			flex-direction: row;
			align-items: center;
			gap: 1.5rem;
		}
		.qrow-meta {
			flex: 0 1 auto;
		}
		.qrow-answer {
			flex: 1 1 auto;
			align-items: center;
		}
	}
	/* A phone: four lines of the question; the window gives way first on
	   the meta line; the field takes the row, Send under it. */
	@media (max-width: 639.98px) {
		.qrow-text--clamped {
			-webkit-line-clamp: 4;
			line-clamp: 4;
		}
		.qrow-window {
			display: none;
		}
		.qrow-field {
			flex-basis: 100%;
			max-width: none;
			height: 2.75rem;
			padding-block: 0.625rem;
		}
	}
	@media (forced-colors: active) {
		.qrow-expiry {
			color: CanvasText;
		}
	}
	/* Forced colours draw a ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.qrow-dismiss {
			margin-inline: 0;
		}
	}
</style>
