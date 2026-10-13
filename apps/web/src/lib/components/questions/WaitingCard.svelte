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
	/** Folded windows the reader opened, by their newest question's id. */
	const unfolded = new SvelteSet<string>();
	let showAll = $state(false);
	let busy = $state<string | undefined>(undefined);
	let notes = $state<Record<string, string>>({});
	let said = $state('');
	let section = $state<HTMLElement | undefined>(undefined);
	let heading = $state<HTMLElement | undefined>(undefined);

	let open = $derived(questions.filter((q) => !closed.has(q.id)));
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
			questions = projectId
				? await listProjectQuestions(projectId)
				: await listQuestions('pending');
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
	// closed somewhere else: ask how, once, and show that line.
	let known = new Set<string>();
	$effect(() => {
		const ids = questions.map((q) => q.id);
		untrack(() => {
			const now = new Set(ids);
			const gone = [...known].filter((id) => !now.has(id) && !closed.has(id));
			known = now;
			if (gone.length) void lookUp(gone);
		});
	});

	async function lookUp(ids: string[]) {
		try {
			const all = projectId
				? await listProjectQuestions(projectId, false, { passive: true })
				: await listQuestions('all', { passive: true });
			for (const q of all) if (ids.includes(q.id)) close(q);
		} catch {
			// The row has gone already; the line is a courtesy.
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
		const hadFocus = !!section
			?.querySelector(`[data-qid="${q.id}"]`)
			?.contains(document.activeElement);
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
	<section
		class="card waiting"
		id="questions"
		data-testid={testid}
		aria-labelledby="questions-title"
		bind:this={section}
	>
		<div class="flex items-center gap-2.5">
			<h2 id="questions-title" class="text-xl font-semibold" tabindex="-1" bind:this={heading}>
				Waiting on you
			</h2>
			{#if open.length > 0}
				<span
					class="badge badge--warn tabular-nums"
					aria-label="{open.length} {plural(open.length)}">{open.length}</span
				>
			{/if}
		</div>
		<!-- Always in the page, so the line put in it is announced. -->
		<p class="sr-only" role="status">{said}</p>
		<ul class="mt-2">
			{#each shown as g (g.project_id)}
				<li class="waiting-group" class:waiting-group--head={grouped}>
					{#if grouped}
						<p class="waiting-project text-sm">
							<a href={resolve('/projects/[id]', { id: g.project_id })} class="waiting-project-link"
								>{g.project}</a
							>
							{#if g.pending > 0}
								<span class="text-ink-muted tabular-nums"
									>{g.pending}<span class="sr-only"> {plural(g.pending)}</span></span
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
									<li class="waiting-fold text-sm text-ink-muted">
										<span
											>{r.folded.length} earlier {plural(r.folded.length)} from
											<span class="font-mono text-compact">{r.question.agent}</span
											>{#if r.question.window && r.question.window !== r.question.agent}&nbsp;·
												{r.question.window}{/if}</span
										>
										<button
											type="button"
											class="btn-ghost hit -mx-2"
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
			<button type="button" class="btn-ghost hit -mx-2 mt-1" onclick={() => (showAll = !showAll)}
				>{showAll ? 'Show fewer' : `Show all ${total}`}</button
			>
		{/if}
	</section>
{/if}

<style>
	/* Rows split by hairlines; no rule down the side (I-693). */
	.waiting :global(.qrow + .qrow),
	.waiting :global(.qrow + .waiting-fold),
	.waiting :global(.waiting-fold + .qrow) {
		border-top: 1px solid var(--rule);
	}
	.waiting-group + .waiting-group {
		border-top: 1px solid var(--rule);
	}
	.waiting-group--head {
		padding-top: 0.75rem;
	}
	.waiting-project {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
	}
	.waiting-project-link {
		font-weight: 500;
		color: var(--ink);
		text-decoration: underline;
		text-decoration-color: var(--rule-strong);
		text-decoration-thickness: 1px;
		text-underline-offset: 4px;
	}
	.waiting-project-link:hover {
		text-decoration-color: currentColor;
	}
	.waiting-fold {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
		padding: 0.5rem 0 0.75rem 1.25rem;
	}
	/* A phone's rows use the marker's column, so the fold does too. */
	@media (width < 40rem) {
		.waiting-fold {
			padding-left: 0;
		}
	}
</style>
