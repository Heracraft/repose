<!--
  One closed question in /questions' Answered section: when, project and
  agent in the meta slot (wrapping cleanly so project names do not clip),
  then the question text wrapped to 3 lines (4 on a phone) with "Show all"
  whenever clamped so the full text is readable without hover tooltips,
  an optional agent link, and the outcome.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { resolve } from '$app/paths';
	import { agentLink } from '$lib/agent-link';
	import { dateTime } from '$lib/format';
	import type { AnsweredRow } from '$lib/question-view';

	let {
		row,
		live = true
	}: {
		row: AnsweredRow;
		/** Whether the project still exists (a destroyed project stays unlinked). */
		live?: boolean;
	} = $props();

	let q = $derived(row.question);
	let link = $derived(agentLink(q.link));

	let open = $state(false);
	let clamped = $state(false);
	let textEl = $state<HTMLElement | undefined>(undefined);

	function measure() {
		if (textEl && !open) {
			clamped = textEl.scrollHeight > textEl.clientHeight + 1;
		}
	}

	onMount(() => {
		measure();
		const ro = new ResizeObserver(() => measure());
		if (textEl) ro.observe(textEl);
		void document.fonts?.ready.then(measure);
		return () => ro.disconnect();
	});

	async function toggle() {
		open = !open;
		await tick();
		if (!open) measure();
	}

	let textId = $derived(`aq-text-${q.id}`);
</script>

<li class="arow" data-testid="answered-row" data-qid={q.id}>
	<p class="arow-meta">
		<span class="arow-when-project"
			><time datetime={q.answered_at ?? q.created_at} title={dateTime(q.answered_at ?? q.created_at)}
				>{row.when}</time
			><span aria-hidden="true">&nbsp;·&nbsp;</span>{#if live}<a
					href={resolve('/projects/[id]', { id: q.project_id })}
					class="arow-project hk-tap">{q.project}</a
				>{:else}<span class="arow-project">{q.project}</span>{/if}</span
		><span class="arow-by"
			><span class="arow-sep" aria-hidden="true">&nbsp;·&nbsp;</span><span class="hk-sr"
				>asked by
			</span><span class="arow-agent">{q.agent}</span></span
		>
	</p>
	<div class="arow-body">
		<p
			id={textId}
			class="arow-text"
			class:arow-text--clamped={!open}
			data-testid="answered-text"
			bind:this={textEl}
		>
			{q.text}
		</p>
		{#if clamped || open}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm arow-more"
				aria-expanded={open}
				aria-controls={textId}
				onclick={toggle}>{open ? 'Show less' : 'Show all'}</button
			>
		{/if}
		{#if link}
			<!-- eslint-disable svelte/no-navigation-without-resolve -- the agent's own address, checked by agentLink -->
			<a class="hk-link arow-link" href={link.href} target="_blank" rel="noopener noreferrer"
				>{link.text}</a
			>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/if}
		<p class="arow-outcome">
			<span data-testid="answered-outcome"
				>{#if row.answer !== undefined}<span class="arow-answer">{row.answer}</span
					>{#if row.rest}<span aria-hidden="true">&nbsp;·&nbsp;</span>{row.rest}{/if}{:else}{row.rest}{/if}</span
			>
		</p>
	</div>
</li>

<style>
	/* One hairline row a question: when, where and which agent asked,
	   then what was asked (wrapped, expandable when long) and how it ended.
	   From a 640px column the meta takes its own left column. */
	.arow {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.25rem;
		padding: 0.75rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.arow p {
		margin: 0;
		min-width: 0;
	}
	.arow-meta {
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
		overflow-wrap: break-word;
	}
	.arow-when-project {
		overflow-wrap: break-word;
	}
	.arow-project {
		color: var(--hk-text-2);
	}
	a.arow-project {
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	a.arow-project:hover {
		color: var(--hk-text);
		text-decoration-color: currentColor;
	}
	.arow-body {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.125rem;
		min-width: 0;
	}
	.arow-text {
		max-width: 48rem;
		font-size: 0.875rem;
		line-height: 1.375rem;
		color: var(--hk-text);
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.arow-text--clamped {
		display: -webkit-box;
		-webkit-box-orient: vertical;
		-webkit-line-clamp: 3;
		line-clamp: 3;
		overflow: hidden;
	}
	.arow-more {
		margin: 0.125rem 0 0 -0.625rem;
	}
	.arow-link {
		display: inline-block;
		margin-top: 0.125rem;
		font-size: 0.8125rem;
		overflow-wrap: anywhere;
	}
	.arow-outcome {
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		overflow-wrap: anywhere;
	}
	.arow-answer {
		font-weight: 500;
		color: var(--hk-text);
	}
	.arow-agent {
		font-family: var(--hk-font-mono);
	}
	@container dash (min-width: 640px) {
		.arow {
			grid-template-columns: 12.5rem minmax(0, 1fr);
			column-gap: 1.5rem;
			align-items: start;
		}
		/* The agent takes its own line under the time and project name. */
		.arow-by {
			display: block;
			margin-top: 0.125rem;
			overflow-wrap: anywhere;
		}
		.arow-sep {
			display: none;
		}
	}
	@media (max-width: 639.98px) {
		.arow-text--clamped {
			-webkit-line-clamp: 4;
			line-clamp: 4;
		}
	}
	@media (forced-colors: active) {
		.arow-more {
			margin-inline: 0;
		}
	}
</style>
