<script lang="ts">
	// /questions (DECISIONS I-841): every question an agent waits on, under
	// its project's name, count and state, answered in place; then the last
	// 7 days' closed ones, each with its outcome and where it came from.
	// The pending list is the rail's own (lib/questions.svelte.ts): this
	// page's 10s poll hands it each answer, so the rail reads nothing more.
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { listQuestions } from '$lib/api/client';
	import { PollFailure } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { clock } from '$lib/clock.svelte';
	import { dateTime } from '$lib/format';
	import { title } from '$lib/title';
	import { pendingQuestions } from '$lib/questions.svelte';
	import { projectsStore } from '$lib/projects.svelte';
	import { ANSWERED_DAYS, answeredRows } from '$lib/question-view';
	import PageShell from '$lib/components/PageShell.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import WaitingCard from '$lib/components/questions/WaitingCard.svelte';
	import type { Question } from '$lib/api/types';

	/** Rows of Answered drawn before "Show all N". */
	const ANSWERED_SHOWN = 10;

	let questions = $state<Question[] | undefined>(undefined);
	let all = $state<Question[]>([]);
	let loadFailed = $state(false);
	let loadError = $state<string | undefined>(undefined);
	let showAll = $state(false);
	/** When the closed list was last read; it moves only when a question closes. */
	let lastAll = 0;

	const failure = new PollFailure('Could not load questions.');

	async function readAll() {
		try {
			// A side read: passive, so its answer never clears an outage the
			// pending list reported (I-393).
			all = await listQuestions('all', { passive: true });
			lastAll = Date.now();
		} catch {
			// Answered keeps what it showed.
		}
	}

	async function refresh() {
		const first = questions === undefined;
		const before = new Set((questions ?? []).map((q) => q.id));
		try {
			const [list] = await Promise.all([
				listQuestions('pending'),
				first ? readAll() : undefined,
				first ? projectsStore.refresh(30_000) : undefined
			]);
			questions = list;
			pendingQuestions.set(list);
			loadFailed = false;
			failure.ok();
			// A question that left the pending list closed somewhere: Answered
			// gains it now, not on the minute.
			const left = [...before].some((id) => !list.some((q) => q.id === id));
			if (!first && (left || Date.now() - lastAll >= 60_000)) await readAll();
		} catch (err) {
			if (questions === undefined) {
				loadFailed = true;
				loadError = loadErrorText(err, 'Could not load questions.');
			}
			failure.fail(err, questions === undefined);
		}
	}

	onMount(() => {
		const stopClock = clock.use();
		const stopPoll = pollWhileVisible(refresh);
		return () => {
			stopClock();
			stopPoll();
		};
	});

	const projectOf = (id: string) => projectsStore.list?.find((p) => p.id === id);
	/** A project that is gone keeps its name, unlinked: its page would be a 404. */
	const live = (id: string) => !projectsStore.list || !!projectOf(id);

	let rows = $derived(answeredRows(all, clock.now));
	let shown = $derived(showAll ? rows : rows.slice(0, ANSWERED_SHOWN));
</script>

<svelte:head><title>{title('Questions')}</title></svelte:head>

<PageShell title="Questions">
	<LoadState
		status={questions !== undefined ? 'ready' : loadFailed ? 'failed' : 'loading'}
		onretry={refresh}
		error={loadError}
	>
		<WaitingCard
			bind:questions={() => questions ?? [], (v) => (questions = v)}
			group
			{projectOf}
			empty="No questions waiting."
			onclosed={() => void readAll()}
		/>

		{#if rows.length > 0}
			<section class="hk-section answered" aria-labelledby="answered-title">
				<div class="hk-section-head">
					<h2 id="answered-title" class="hk-h2-app">Answered</h2>
					<span>Last {ANSWERED_DAYS} days</span>
				</div>
				<ol class="answered-list" data-testid="answered">
					{#each shown as r (r.question.id)}
						{@const q = r.question}
						<li class="arow" data-testid="answered-row" data-qid={q.id}>
							<p class="arow-meta">
								<time
									datetime={q.answered_at ?? q.created_at}
									title={dateTime(q.answered_at ?? q.created_at)}>{r.when}</time
								><span aria-hidden="true">&nbsp;·&nbsp;</span>{#if live(q.project_id)}<a
										href={resolve('/projects/[id]', { id: q.project_id })}
										class="arow-project hk-tap">{q.project}</a
									>{:else}<span class="arow-project">{q.project}</span>{/if}<span class="arow-by"
									><span class="arow-sep" aria-hidden="true">&nbsp;·&nbsp;</span><span class="hk-sr"
										>asked by
									</span><span class="arow-agent">{q.agent}</span></span
								>
							</p>
							<p class="arow-text" title={q.text}>{q.text}</p>
							<p class="arow-outcome">
								<span data-testid="answered-outcome"
									>{#if r.answer !== undefined}<span class="arow-answer">{r.answer}</span
										>{#if r.rest}<span aria-hidden="true">&nbsp;·&nbsp;</span
											>{r.rest}{/if}{:else}{r.rest}{/if}</span
								>
							</p>
						</li>
					{/each}
				</ol>
				{#if rows.length > ANSWERED_SHOWN}
					<div class="amore">
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm"
							onclick={() => (showAll = !showAll)}
							>{showAll ? 'Show fewer' : `Show all ${rows.length}`}</button
						>
					</div>
				{/if}
			</section>
		{/if}
	</LoadState>
</PageShell>

<style>
	.answered-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	/* One hairline row a question: when, where and which agent asked,
	   then what was asked on one line, then how it ended. From a 640px
	   column the when, where and who take their own column. */
	.arow {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.125rem;
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
		white-space: nowrap;
		/* Clipped sideways only: the project link's 44px touch box
		   reaches over and under the line. */
		overflow: clip visible;
		text-overflow: ellipsis;
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
	.arow-text {
		font-size: 0.875rem;
		line-height: 1.375rem;
		color: var(--hk-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
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
			grid-template-columns: 11rem minmax(0, 1fr);
			column-gap: 1.5rem;
		}
		.arow-meta {
			grid-row: span 2;
		}
		/* The agent takes the meta column's second line. */
		.arow-by {
			display: block;
			overflow: hidden;
			text-overflow: ellipsis;
		}
		.arow-sep {
			display: none;
		}
	}
	.amore {
		margin-top: 0.5rem;
	}
	.amore .hk-btn-ghost {
		margin-left: -0.625rem;
	}
	@media (forced-colors: active) {
		.amore .hk-btn-ghost {
			margin-inline: 0;
		}
	}
</style>
