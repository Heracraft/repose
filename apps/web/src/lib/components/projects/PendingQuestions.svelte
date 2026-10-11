<!--
  The questions agents are waiting on (repose-ask, I-245), at the top of
  /projects: one bordered callout with the accent's rule down its left
  edge, a row per question. A row names the project and the agent, says
  the question and when it expires, and links to the project page, which
  is where it is answered. Nothing is drawn while none is waiting.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { dateTime, timeUntil } from '$lib/format';
	import type { Question } from '$lib/api/types';

	let { questions }: { questions: Question[] } = $props();

	function questionsHref(q: Question): string {
		return `${resolve('/projects/[id]', { id: q.project_id })}#questions`;
	}
</script>

{#if questions.length > 0}
	<section class="pq" aria-label="Questions waiting" data-testid="pending-questions">
		<!-- eslint-disable svelte/no-navigation-without-resolve -- questionsHref builds the links with resolve() -->
		<ul>
			{#each questions as q (q.id)}
				{@const left = timeUntil(q.expires_at)}
				<li class="pq-row">
					<div class="min-w-0 flex-1">
						<p class="text-sm text-ink-muted">
							<a href={questionsHref(q)} class="pq-project">{q.project}</a>
							<span aria-hidden="true">·</span>
							<span class="font-mono text-compact text-ink">{q.agent}</span> asks
						</p>
						<p class="mt-1 text-base text-ink wrap-break-word">{q.text}</p>
					</div>
					<div class="pq-side">
						{#if left}
							<span class="text-xs text-ink-muted tabular-nums" title={dateTime(q.expires_at)}
								>expires in {left}</span
							>
						{/if}
						<a
							href={questionsHref(q)}
							class="btn-quiet btn--sm"
							aria-label="Answer {q.project}'s question">Answer</a
						>
					</div>
				</li>
			{/each}
		</ul>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	</section>
{/if}

<style>
	.pq {
		border: 1px solid var(--rule);
		border-left: 3px solid var(--accent);
		border-radius: var(--radius-sm);
		background-color: var(--surface);
	}
	.pq-row {
		display: flex;
		align-items: center;
		gap: 0.75rem 1.5rem;
		padding: 0.875rem 1.25rem;
	}
	.pq-row + .pq-row {
		border-top: 1px solid var(--rule);
	}
	.pq-project {
		font-weight: 500;
		color: var(--ink);
		text-decoration: underline;
		text-decoration-color: var(--rule-strong);
		text-decoration-thickness: 1px;
		text-underline-offset: 4px;
	}
	.pq-project:hover {
		text-decoration-color: currentColor;
	}
	.pq-side {
		display: flex;
		flex-shrink: 0;
		align-items: center;
		gap: 1rem;
	}
	/* On a phone the expiry and the button go under the question. */
	@media (width < 40rem) {
		.pq-row {
			flex-direction: column;
			align-items: stretch;
			padding: 0.875rem 1rem;
		}
		.pq-side {
			justify-content: space-between;
		}
	}
	@media (forced-colors: active) {
		.pq {
			border-left-color: Highlight;
		}
	}
</style>
