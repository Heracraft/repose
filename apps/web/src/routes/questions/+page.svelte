<script lang="ts">
	// /questions (DECISIONS I-841): every question an agent waits on, under
	// its project's name, count and state, answered in place; then the last
	// 7 days' closed ones, each with its outcome and where it came from.
	// The pending list is the rail's own (lib/questions.svelte.ts): this
	// page's 10s poll hands it each answer, so the rail reads nothing more.
	import { onMount } from 'svelte';
	import { listQuestions } from '$lib/api/client';
	import { PollFailure } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { clock } from '$lib/clock.svelte';
	import { title } from '$lib/title';
	import { pendingQuestions } from '$lib/questions.svelte';
	import { projectsStore } from '$lib/projects.svelte';
	import { ANSWERED_DAYS, answeredRows } from '$lib/question-view';
	import PageShell from '$lib/components/PageShell.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import AnsweredRow from '$lib/components/questions/AnsweredRow.svelte';
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
						<AnsweredRow row={r} live={live(r.question.project_id)} />
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
