<!--
  The project's newest 5 events on its Overview (DECISIONS I-841), with
  "All events" to the Events tab, where the pager and the filters are.
  One page of 5, read again on the project's poll.
-->
<script module lang="ts">
	/** Rows on the Overview. */
	export const LATEST_EVENTS = 5;
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { listEventsPage } from '$lib/api/client';
	import { PollFailure, type PollGroup } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import type { ProjectEvent } from '$lib/api/types';
	import EventRow from './EventRow.svelte';

	let {
		projectId,
		events = $bindable(),
		pollFailures
	}: {
		projectId: string;
		/** The Overview's first load fills it, so the section paints with the rest. */
		events?: ProjectEvent[];
		pollFailures?: PollGroup;
	} = $props();

	// svelte-ignore state_referenced_locally
	const failure = new PollFailure('Could not load events.', pollFailures);

	onMount(() => {
		// The first load already has them; the poll's first tick waits its turn.
		let first = true;
		return pollWhileVisible(async () => {
			if (first) {
				first = false;
				return;
			}
			try {
				events = (await listEventsPage(projectId, { limit: LATEST_EVENTS })).events;
				failure.ok();
			} catch (err) {
				failure.fail(err);
			}
		});
	});
</script>

<section class="hk-section" aria-labelledby="latest-title" data-testid="latest-events">
	<div class="hk-section-head">
		<h2 id="latest-title" class="hk-h2-app">Latest events</h2>
		<a class="hk-link all" href={resolve('/projects/[id]/events', { id: projectId })}>All events</a>
	</div>
	{#if !events || events.length === 0}
		<div class="hk-empty-row">
			<span>{events ? 'No events yet.' : 'Could not load events.'}</span>
		</div>
	{:else}
		<ul class="latest">
			{#each events.slice(0, LATEST_EVENTS) as e (e.id)}
				<EventRow event={e} />
			{/each}
		</ul>
	{/if}
</section>

<style>
	.latest {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.latest > :global(li:first-child) {
		border-top: 0;
		padding-top: 0.25rem;
	}
	.hk-section-head > .all {
		color: var(--hk-accent);
	}
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.hk-section-head > .all {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
</style>
