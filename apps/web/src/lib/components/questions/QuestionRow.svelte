<!--
  One question an agent waits on (repose-ask, I-245), as the "Waiting on
  you" card draws it on /projects and on a project page (I-693). An amber
  square marks it; a 13px line names the agent, its window and the age,
  with the expiry in amber only in its last 10 minutes. The question is
  held to 3 lines (4 on a phone) with "Show all" only when that cuts it.
  Under it the options as quiet chips, or a field that grows to 4 lines
  (Enter sends, Shift+Enter breaks a line), then a muted Dismiss. A closed
  question is one line saying what closed it, for as long as the card
  keeps it.
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
	function measure() {
		if (textEl && !open) clamped = textEl.scrollHeight > textEl.clientHeight + 1;
	}
	onMount(() => {
		measure();
		const ro = new ResizeObserver(measure);
		if (textEl) ro.observe(textEl);
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

{#if closed}
	<li class="qrow qrow--closed" data-testid="question" data-qid={q.id} data-state={q.state}>
		<span class="dot qrow-mark" class:dot--good={closed.tone === 'answered'} aria-hidden="true"
		></span>
		<p class="qrow-line min-w-0 truncate text-sm text-ink-muted">
			{#if closed.answer !== undefined}
				Answered <span class="font-medium text-ink">{closed.answer}</span>{closed.text.slice(
					'Answered '.length + closed.answer.length
				)}
			{:else}
				{closed.text}
			{/if}
		</p>
	</li>
{:else}
	<li class="qrow" data-testid="question" data-qid={q.id} data-state="pending">
		<span class="dot dot--warn qrow-mark" aria-hidden="true"></span>
		<div class="qrow-body min-w-0">
			<p class="qrow-meta text-compact">
				<span class="qrow-who min-w-0 truncate">
					{#if projectHref}
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- the card builds it with resolve() -->
						<a href={projectHref} class="qrow-project">{q.project}</a><span
							class="text-ink-faint"
							aria-hidden="true">&nbsp;·&nbsp;</span
						>
					{/if}<span class="font-mono text-ink">{q.agent}</span><span class="sr-only"
						>&nbsp;asks</span
					>{#if windowName}<span class="qrow-window text-ink-muted"
							><span class="text-ink-faint" aria-hidden="true">&nbsp;·&nbsp;</span><span
								class="sr-only">in window&nbsp;</span
							>{windowName}</span
						>{/if}
				</span>
				<span class="qrow-time text-ink-muted tabular-nums">
					<time datetime={q.created_at} title={dateTime(q.created_at)}>{asked}</time
					>{#if expiry}<span class="qrow-expiry" title={dateTime(q.expires_at)}
							><span aria-hidden="true">&nbsp;·&nbsp;</span>{expiry}</span
						>{/if}
				</span>
			</p>
			<p
				id={idFor('text')}
				class="qrow-text text-ink"
				class:qrow-text--clamped={!open}
				bind:this={textEl}
			>
				{q.text}
			</p>
			{#if clamped || open}
				<button
					type="button"
					class="btn-ghost hit -mx-2 mt-0.5"
					aria-expanded={open}
					aria-controls={idFor('text')}
					onclick={toggle}>{open ? 'Show less' : 'Show all'}</button
				>
			{/if}
			<div class="qrow-actions">
				{#if q.options.length > 0}
					<!-- Answers of equal weight, so quiet chips, never a row of
					     primaries; the label is the agent's own word. -->
					<div class="qrow-options" role="group" aria-label="Answers">
						{#each q.options as o (o)}
							<button
								type="button"
								class="btn-quiet btn--sm"
								data-qrow-first
								disabled={busy}
								onclick={() => onanswer(o)}>{o}</button
							>
						{/each}
					</div>
					<button
						type="button"
						class="btn-ghost qrow-dismiss"
						disabled={busy}
						title="The agent stops waiting"
						aria-label={`Dismiss the question from ${q.agent}`}
						onclick={ondismiss}>Dismiss</button
					>
				{:else}
					<label class="sr-only" for={idFor('answer')}>Your answer</label>
					<textarea
						id={idFor('answer')}
						class="field qrow-field"
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
					<div class="qrow-buttons">
						<button
							type="button"
							class="btn-quiet btn--sm qrow-send"
							disabled={busy || !draft.trim()}
							onclick={send}>{busy ? 'Sending…' : 'Send'}</button
						>
						<button
							type="button"
							class="btn-ghost qrow-dismiss"
							disabled={busy}
							title="The agent stops waiting"
							aria-label={`Dismiss the question from ${q.agent}`}
							onclick={ondismiss}>Dismiss</button
						>
					</div>
				{/if}
			</div>
			{#if note}
				<p id={idFor('note')} class="field-error">{note}</p>
			{/if}
		</div>
	</li>
{/if}

<style>
	/* A marker column and the body. The marker sits on the meta line's
	   middle; the hairline between rows is the card's (.qrow + .qrow).
	   The body's parts are the row's grid items, so a phone can give them
	   the marker's column too. */
	.qrow {
		display: grid;
		grid-template-columns: 0.5rem minmax(0, 1fr);
		column-gap: 0.75rem;
		padding-block: 0.875rem;
	}
	.qrow-body {
		display: contents;
	}
	.qrow-body > :global(*) {
		grid-column: 2;
		justify-self: start;
	}
	.qrow-body > .qrow-meta,
	.qrow-body > .qrow-actions,
	.qrow-body > .qrow-text {
		justify-self: stretch;
	}
	.qrow-mark {
		margin-top: 0.375rem;
	}
	.qrow--closed {
		align-items: center;
		padding-block: 0.625rem;
	}
	.qrow--closed .qrow-mark {
		margin-top: 0;
	}
	.qrow-meta {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.75rem;
		line-height: 1.25rem;
	}
	.qrow-who {
		display: block;
	}
	.qrow-time {
		flex-shrink: 0;
		white-space: nowrap;
	}
	.qrow-expiry {
		color: var(--color-amber-800);
	}
	.qrow-project {
		font-weight: 500;
		color: var(--ink);
		text-decoration: underline;
		text-decoration-color: var(--rule-strong);
		text-decoration-thickness: 1px;
		text-underline-offset: 4px;
	}
	.qrow-project:hover {
		text-decoration-color: currentColor;
	}
	/* About 80 characters a line at 16px; the agent's own line breaks kept. */
	.qrow-text {
		margin-top: 0.25rem;
		max-width: 40rem;
		font-size: var(--text-base);
		line-height: 1.5rem;
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
	.qrow-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: 0.5rem 0.75rem;
		margin-top: 0.75rem;
	}
	.qrow-options {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.qrow-options :global(.btn-quiet) {
		min-width: 4rem;
	}
	/* The field takes the row up to about 32rem; Send and Dismiss wrap
	   under it whole (DESIGN-LANGUAGE "Card layout"). Its one line is a
	   compact button's height. */
	.qrow-field {
		flex: 1 1 16rem;
		min-width: 0;
		max-width: 32rem;
		min-height: 0;
		resize: none;
		overflow-y: hidden;
		padding-block: 0.375rem;
		line-height: 1.25rem;
	}
	.qrow-buttons {
		display: flex;
		flex-shrink: 0;
		align-items: center;
		gap: 0.5rem;
	}
	.qrow-dismiss {
		align-self: center;
	}
	@media (prefers-color-scheme: dark) {
		.qrow-expiry {
			color: var(--color-amber-300);
		}
	}
	/* A phone: four lines of the question; the window gives way first on
	   the meta line; each option a full-width line; the field full width
	   with Dismiss left and Send right under it. */
	@media (width < 40rem) {
		.qrow-body > :global(:not(.qrow-meta)) {
			grid-column: 1 / -1;
		}
		.qrow-text--clamped {
			-webkit-line-clamp: 4;
			line-clamp: 4;
		}
		.qrow-window {
			display: none;
		}
		.qrow-options {
			flex-direction: column;
			width: 100%;
		}
		.qrow-field {
			flex-basis: 100%;
			max-width: none;
		}
		.qrow-buttons {
			width: 100%;
			flex-direction: row-reverse;
			justify-content: space-between;
		}
		.qrow-dismiss {
			margin-inline-start: -0.5rem;
		}
	}
	@media (forced-colors: active) {
		.qrow-expiry {
			color: CanvasText;
			font-weight: 600;
		}
	}
</style>
