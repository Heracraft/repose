<!--
  "Waiting on you" (I-693): the questions agents wait on, across the frame
  on /projects (every project, grouped by project when more than one
  waits, 3 rows before "Show all N") and on a project page (that project's,
  polled here). Each is a QuestionRow, answered in place. Older questions
  from one agent window fold under its newest. A question that closes,
  here or anywhere else (the terminal, the CLI, ntfy, email, Google Chat,
  the agent taking it back), stays 8 seconds as one line saying how, then
  leaves. Nothing is drawn while none waits.
-->
<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { resolve } from '$app/paths';
	import {
		answerQuestion,
		cancelQuestion,
		listProjectQuestions,
		listQuestions
	} from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { pendingQuestions } from '$lib/questions.svelte';
	import { clock } from '$lib/clock.svelte';
	import { CLOSED_LINE_MS, closedLine, layoutQuestions } from '$lib/question-view';
	import QuestionRow from './QuestionRow.svelte';
	import type { Question } from '$lib/api/types';

	let {
		questions = $bindable([]),
		projectId,
		pollFailures,
		cap = 0,
		testid = 'questions'
	}: {
		/** The open questions; the page's first load fills it, so the card is in the first paint. */
		questions?: Question[];
		/** One project's card, which polls its own questions; without it, every project's, which the page polls. */
		projectId?: string;
		pollFailures?: PollGroup;
		/** Rows shown before "Show all N"; 0 shows every row. */
		cap?: number;
		testid?: string;
	} = $props();

	/** Questions that closed while on screen, kept as their one line for 8 seconds. */
	const closed = new SvelteMap<string, Question>();
	/**
	 * Questions that left the open list without this card closing them,
	 * kept as they were while the card asks the api how they closed, so the
	 * row holds its place (and its focus) until its closing line replaces it.
	 */
	const leaving = new SvelteMap<string, Question>();
	/** Folded windows the reader opened, by their newest question's id. */
	const unfolded = new SvelteSet<string>();
	let showAll = $state(false);
	let busy = $state<string | undefined>(undefined);
	let notes = $state<Record<string, string>>({});
	let said = $state('');
	let section = $state<HTMLElement | undefined>(undefined);
	let heading = $state<HTMLElement | undefined>(undefined);

	let waiting = $derived(questions.filter((q) => !closed.has(q.id)));
	let open = $derived([...waiting, ...[...leaving.values()].filter((q) => !closed.has(q.id))]);
	let groups = $derived(layoutQuestions([...open, ...closed.values()]));
	let grouped = $derived(!projectId && groups.length > 1);
	let total = $derived(groups.reduce((n, g) => n + g.rows.length, 0));
	let capped = $derived(cap > 0 && !showAll && total > cap);

	const href = (q: { project_id: string }) => resolve('/projects/[id]', { id: q.project_id });

	// The page's group is fixed for its life, so reading it once is right.
	// svelte-ignore state_referenced_locally
	const pollFailure = new PollFailure('Could not load questions.', pollFailures);

	async function refresh() {
		try {
			// The page's own read (the list, the project) says whether the
			// api is up; the card's poll must not clear its outage bar.
			questions = projectId
				? await listProjectQuestions(projectId, true, { passive: true })
				: await listQuestions('pending', { passive: true });
			pollFailure.ok();
		} catch (err) {
			pollFailure.fail(err);
		}
	}

	onMount(() => {
		const stopClock = clock.use();
		if (!projectId) return stopClock;
		// The first load already has them; the poll's first tick waits its turn.
		let first = true;
		const stopPoll = pollWhileVisible(() => {
			if (first) {
				first = false;
				return;
			}
			return refresh();
		});
		return () => {
			stopClock();
			stopPoll();
		};
	});

	/** Shows q's closing line for 8 seconds in its row and says it to a screen reader. */
	function close(q: Question) {
		if (q.state === 'pending') return;
		closed.set(q.id, q);
		said = closedLine(q).text;
		setTimeout(() => closed.delete(q.id), CLOSED_LINE_MS);
	}

	// A question that leaves the open list without this card closing it was
	// closed somewhere else: hold its row, ask how once, and show that line.
	// A pre effect runs before the DOM would drop the row, so it never does.
	let known = new Map<string, Question>();
	$effect.pre(() => {
		const now = new Map(questions.map((q) => [q.id, q]));
		untrack(() => {
			const gone = [...known.values()].filter((q) => !now.has(q.id) && !closed.has(q.id));
			for (const id of now.keys()) leaving.delete(id);
			known = now;
			if (!gone.length) return;
			for (const q of gone) leaving.set(q.id, q);
			void lookUp(gone.map((q) => q.id));
		});
	});

	const holdsFocus = (id: string) =>
		!!section?.querySelector(`[data-qid="${id}"]`)?.contains(document.activeElement);

	async function lookUp(ids: string[]) {
		let all: Question[] = [];
		try {
			all = projectId
				? await listProjectQuestions(projectId, false, { passive: true })
				: await listQuestions('all', { passive: true });
		} catch {
			// The line is a courtesy; without it the row just goes.
		}
		for (const id of ids) {
			if (!leaving.has(id)) continue;
			// The row is still drawn, so whether it holds focus is current.
			void passFocus(id, holdsFocus(id));
			const q = all.find((c) => c.id === id);
			if (q) close(q);
			leaving.delete(id);
		}
	}

	/** After a row closes, the keyboard goes on to the next question (or the one above), else the card's title. */
	async function passFocus(id: string, hadFocus: boolean) {
		if (!section || !hadFocus) return;
		const row = section.querySelector(`[data-qid="${id}"]`);
		if (!row) return;
		const firsts = [...section.querySelectorAll<HTMLElement>('[data-qid]')];
		const at = firsts.indexOf(row as HTMLElement);
		// The next row's first control, else the nearest one above.
		const order = [...firsts.slice(at + 1), ...firsts.slice(0, at).reverse()];
		await tick();
		const next = order
			.map((r) => r.querySelector<HTMLElement>('[data-qrow-first]'))
			.find((el) => el?.isConnected);
		(next ?? heading)?.focus();
	}

	async function act(q: Question, answer?: string) {
		// Read before the row's controls are disabled, which drops focus.
		const hadFocus = holdsFocus(q.id);
		busy = q.id;
		notes = { ...notes, [q.id]: '' };
		try {
			const res =
				answer === undefined
					? await cancelQuestion(q.project_id, q.id)
					: await answerQuestion(q.project_id, q.id, answer);
			void passFocus(q.id, hadFocus);
			close(res);
			await refresh();
			// The dashboard nav's count drops with it (lib/questions.svelte.ts).
			void pendingQuestions.refresh();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'conflict') {
				// Closed elsewhere first: the row says how, from the api's copy.
				const theirs = err.detail?.question as Question | undefined;
				void passFocus(q.id, hadFocus);
				if (theirs) close(theirs);
				await refresh();
				void pendingQuestions.refresh();
			} else if (err instanceof ApiError && err.code === 'invalid') {
				const opts = (err.detail?.options as string[] | undefined) ?? q.options;
				notes = {
					...notes,
					[q.id]: opts.length ? `The answer is one of: ${opts.join(', ')}.` : err.message
				};
			} else {
				toastApiError(err, 'Could not send the answer.');
			}
		} finally {
			busy = undefined;
		}
	}

	/** The rows to draw, in order, after the cap. */
	let shown = $derived.by(() => {
		let left = capped ? cap : Infinity;
		return groups
			.map((g) => {
				const rows = g.rows.slice(0, Math.max(0, left));
				left -= rows.length;
				return { ...g, rows };
			})
			.filter((g) => g.rows.length > 0);
	});
	const plural = (n: number) => (n === 1 ? 'question' : 'questions');
</script>

{#snippet row(q: Question, single: boolean)}
	<QuestionRow
		question={q}
		now={clock.now}
		projectHref={single ? href(q) : undefined}
		busy={busy === q.id}
		note={notes[q.id]}
		onanswer={(a) => act(q, a)}
		ondismiss={() => act(q)}
	/>
{/snippet}

{#if open.length > 0 || closed.size > 0}
	<!-- A section on the page's paper, no frame (I-837): its count badge is
	     the one amber mark, the same count the header's Projects badge shows. -->
	<section
		class="hk-section waiting"
		id="questions"
		data-testid={testid}
		aria-labelledby="questions-title"
		bind:this={section}
	>
		<div class="hk-section-head waiting-head">
			<h2 id="questions-title" class="hk-h2-app" tabindex="-1" bind:this={heading}>
				Waiting on you
			</h2>
			{#if waiting.length > 0}
				<span
					class="hk-badge hk-badge--warn waiting-count"
					aria-label="{waiting.length} {plural(waiting.length)}">{waiting.length}</span
				>
			{/if}
		</div>
		<!-- Always in the page, so the line put in it is announced. -->
		<p class="hk-sr" role="status">{said}</p>
		<ul class="waiting-list">
			{#each shown as g (g.project_id)}
				<li class="waiting-group" class:waiting-group--head={grouped}>
					{#if grouped}
						<p class="waiting-project">
							<a href={resolve('/projects/[id]', { id: g.project_id })} class="waiting-project-link"
								>{g.project}</a
							>
							{#if g.pending > 0}
								<span class="waiting-project-n"
									>{g.pending}<span class="hk-sr"> {plural(g.pending)}</span></span
								>
							{/if}
						</p>
					{/if}
					<ul>
						{#each g.rows as r (r.question.id)}
							{@render row(r.question, !projectId && !grouped)}
							{#if r.folded.length > 0}
								{#if unfolded.has(r.question.id)}
									{#each r.folded as f (f.id)}
										{@render row(f, !projectId && !grouped)}
									{/each}
								{:else}
									<li class="waiting-fold">
										<span
											>{r.folded.length} earlier {plural(r.folded.length)} from
											<span class="waiting-fold-agent">{r.question.agent}</span
											>{#if r.question.window && r.question.window !== r.question.agent}<span
													class="waiting-fold-window">&nbsp;· {r.question.window}</span
												>{/if}</span
										>
										<button
											type="button"
											class="hk-btn-ghost hk-btn-sm waiting-fold-show"
											onclick={() => unfolded.add(r.question.id)}>Show</button
										>
									</li>
								{/if}
							{/if}
						{/each}
					</ul>
				</li>
			{/each}
		</ul>
		{#if cap > 0 && total > cap}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm waiting-all"
				onclick={() => (showAll = !showAll)}>{showAll ? 'Show fewer' : `Show all ${total}`}</button
			>
		{/if}
	</section>
{/if}

<style>
	.waiting-head {
		justify-content: flex-start;
		align-items: center;
		gap: 0.625rem;
	}
	.waiting-count {
		font-variant-numeric: tabular-nums;
	}
	.waiting-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.waiting-list ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	/* Each row hairline-topped, the first included: the head has no rule. */
	.waiting :global(.qrow),
	.waiting-fold {
		border-top: 1px solid var(--hk-hairline);
	}
	.waiting-group--head {
		border-top: 1px solid var(--hk-hairline);
	}
	.waiting-group--head :global(.qrow:first-child) {
		border-top: 0;
		padding-top: 0.25rem;
	}
	.waiting-project {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		margin: 0;
		padding-top: 0.875rem;
		font-size: 0.8125rem;
	}
	.waiting-project-link {
		font-weight: 500;
		color: var(--hk-text);
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	.waiting-project-link:hover {
		text-decoration-color: currentColor;
	}
	.waiting-project-n {
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.waiting-fold {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
		padding: 0.5rem 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.waiting-fold-agent {
		font-family: var(--hk-font-mono);
	}
	.waiting-fold-show {
		margin-block: -0.25rem;
	}
	.waiting-all {
		margin: 0.5rem 0 0 -0.625rem;
	}
	/* A phone drops the window from the row's meta line, so the fold does too. */
	@media (max-width: 639.98px) {
		.waiting-fold-window {
			display: none;
		}
	}
</style>
