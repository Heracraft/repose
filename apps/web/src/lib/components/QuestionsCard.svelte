<script lang="ts">
	// Questions an agent asked with repose-ask and is waiting on (I-245):
	// answered here with a button per option or a text box, or cancelled.
	import { onMount } from 'svelte';
	import { listProjectQuestions, answerQuestion, cancelQuestion } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, PollGroup, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { pendingQuestions } from '$lib/questions.svelte';
	import { relativeTime } from '$lib/format';
	import type { Question } from '$lib/api/types';

	// pollFailures is the page's group, so this poll and the page's share
	// one toast (I-395). questions comes from the page's first load, so the
	// card is in the first paint and nothing lands above painted content
	// later; the page reads it back to point an agent's row here.
	let {
		projectId,
		pollFailures,
		questions = $bindable([])
	}: { projectId: string; pollFailures?: PollGroup; questions?: Question[] } = $props();

	let drafts = $state<Record<string, string>>({});
	let notes = $state<Record<string, string>>({});
	let busy = $state<string | undefined>(undefined);
	let answered = $state<string | undefined>(undefined);
	// An outage raises one toast rather than one every poll (I-393).
	// The page's group is fixed for its life, so reading it once is right.
	// svelte-ignore state_referenced_locally
	const pollFailure = new PollFailure('Could not load questions.', pollFailures);

	async function refresh() {
		try {
			questions = await listProjectQuestions(projectId);
			pollFailure.ok();
		} catch (err) {
			pollFailure.fail(err);
		}
	}

	// The first load already has them; the poll's first tick waits its turn.
	onMount(() => {
		let first = true;
		return pollWhileVisible(() => {
			if (first) {
				first = false;
				return;
			}
			return refresh();
		});
	});

	function expiresIn(iso: string): string {
		const minutes = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 60_000));
		if (minutes < 60) return `${minutes}m`;
		const h = Math.floor(minutes / 60);
		const m = minutes % 60;
		return m ? `${h}h ${m}m` : `${h}h`;
	}

	async function act(q: Question, answer?: string) {
		busy = q.id;
		notes = { ...notes, [q.id]: '' };
		try {
			if (answer === undefined) {
				await cancelQuestion(projectId, q.id);
				answered = `Cancelled: ${q.text.split('\n')[0]}`;
			} else {
				const res = await answerQuestion(projectId, q.id, answer);
				answered = `Answered: ${res.answer ?? answer}`;
			}
			setTimeout(() => (answered = undefined), 8_000);
			await refresh();
			// The dashboard nav's count drops with it (lib/questions.svelte.ts).
			void pendingQuestions.refresh();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'conflict') {
				// Answered elsewhere first (ntfy, email, the CLI): the list
				// drops it, so the reason goes in the status line.
				answered = `Not sent: ${err.message}.`;
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
</script>

{#if questions.length > 0 || answered}
	<section class="card" id="questions" data-testid="questions" aria-labelledby="questions-title">
		<h2 id="questions-title" class="text-xl font-semibold">Questions</h2>
		<!-- The live region is always in the page, so the line that appears
		     inside it is announced; a region inserted with its text already
		     in it is often read by no screen reader. -->
		<p class="text-sm text-ink-muted" class:mt-2={answered} role="status">{answered ?? ''}</p>
		<ul class="mt-4 space-y-4">
			{#each questions as q (q.id)}
				<!-- A callout: the accent's rule says "this is yours to answer";
				     the question is the largest text on the card. -->
				<li class="question py-1 pl-4" data-testid="question">
					<p class="font-mono text-compact text-ink-muted">
						<span class="text-ink">{q.agent} asks</span>{#if q.window && q.window !== q.agent}<span
								>&nbsp;in {q.window}</span
							>{/if}<span title={`asked ${relativeTime(q.created_at)}`}
							>&nbsp;· expires in {expiresIn(q.expires_at)}</span
						>
					</p>
					<p class="mt-1.5 text-lg whitespace-pre-wrap text-ink">{q.text}</p>
					<div class="mt-3 flex flex-wrap items-center gap-3">
						{#if q.options.length > 0}
							<!-- The options are equals, so none is the primary: a row
							     of solid buttons broke "one .btn per view" and gave
							     every answer the weight of the page's main action. -->
							{#each q.options as o (o)}
								<button
									type="button"
									class="btn-quiet h-10 min-w-16"
									disabled={busy === q.id}
									onclick={() => act(q, o)}>{o}</button
								>
							{/each}
						{:else}
							<label class="sr-only" for={`answer-${q.id}`}>Your answer</label>
							<input
								id={`answer-${q.id}`}
								class="field h-10 w-full sm:w-80"
								placeholder="Your answer"
								bind:value={drafts[q.id]}
								aria-invalid={notes[q.id] ? 'true' : undefined}
								aria-describedby={notes[q.id] ? `answer-note-${q.id}` : undefined}
								onkeydown={(e) => {
									if (e.key === 'Enter' && drafts[q.id]?.trim()) void act(q, drafts[q.id]);
								}}
							/>
							<button
								type="button"
								class="btn-quiet h-10 min-w-16"
								disabled={busy === q.id || !drafts[q.id]?.trim()}
								onclick={() => act(q, drafts[q.id])}>Answer</button
							>
						{/if}
						<button
							type="button"
							class="btn-ghost-danger"
							disabled={busy === q.id}
							title="The agent stops waiting"
							aria-label={`Cancel question from ${q.agent}`}
							onclick={() => act(q)}>Cancel question</button
						>
					</div>
					{#if notes[q.id]}
						<p id={`answer-note-${q.id}`} class="field-error">{notes[q.id]}</p>
					{/if}
				</li>
			{/each}
		</ul>
	</section>
{/if}

<style>
	.question {
		border-left: 3px solid var(--accent);
	}
	@media (forced-colors: active) {
		.question {
			border-left-color: Highlight;
		}
	}
</style>
