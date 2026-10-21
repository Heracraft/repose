<!--
  The newest events across the running projects, under the projects table.
  There is no cross-project events route: this asks GET
  /projects/:id/events?limit=10 of each running project (the ten started
  most recently at most), merges the answers by time, folds a run of one
  kind on one project into its newest row (activity.ts) and keeps five
  rows (I-837). It refreshes every 60s while the tab is visible, and is
  not drawn while there is nothing to show.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { listEvents } from '$lib/api/client';
	import { eventKind, toneDot } from '$lib/events';
	import { clockTime, dateTime, dayLabel, relativeTime } from '$lib/format';
	import type { Project, ProjectEvent } from '$lib/api/types';
	import { foldActivity, type Folded } from './activity';

	let {
		projects,
		onsettled
	}: {
		projects: Project[];
		/** Called once, when the first load has answered or failed: the
		 *  page holds what sits under this section until then (I-664). */
		onsettled?: () => void;
	} = $props();

	const SHOWN = 5;
	/** Events asked of each machine: enough that a folded run still leaves five rows. */
	const PER_PROJECT = 10;
	const FAN_OUT = 10;

	interface Row extends ProjectEvent {
		project: Pick<Project, 'id' | 'name'>;
	}

	let rows = $state<Folded<Row>[]>([]);

	// The running projects asked, newest start first. A key of their ids,
	// so a poll of the list that changes nothing does not refetch.
	let asked = $derived(
		projects
			.filter((p) => p.state === 'running')
			.sort((a, b) => (b.started_at ?? '').localeCompare(a.started_at ?? ''))
			.slice(0, FAN_OUT)
	);
	let key = $derived(asked.map((p) => p.id).join(','));
	let running = $derived(projects.filter((p) => p.state === 'running').length);

	let seq = 0;
	let settled = false;
	function settle() {
		if (settled) return;
		settled = true;
		onsettled?.();
	}
	async function load() {
		const mine = ++seq;
		const targets = asked.map((p) => ({ id: p.id, name: p.name }));
		const pages = await Promise.allSettled(
			targets.map((p) => listEvents(p.id, { limit: PER_PROJECT }))
		);
		if (mine !== seq) return;
		// The rows land in the next render; the section under this one is
		// let in with them, in the same pass.
		settle();
		const merged: Row[] = [];
		pages.forEach((r, i) => {
			if (r.status === 'fulfilled') {
				for (const e of r.value) merged.push({ ...e, project: targets[i] });
			}
		});
		// A project whose call failed keeps nothing this round; with every
		// call failed the rows on screen stay rather than blink out.
		if (merged.length === 0 && pages.some((r) => r.status === 'rejected')) return;
		merged.sort((a, b) => (a.ts < b.ts ? 1 : a.ts > b.ts ? -1 : 0));
		rows = foldActivity(merged).slice(0, SHOWN);
	}

	// Runs again only when the set of running projects changes; load()
	// itself reads `asked`, which is a new array on every poll of the list.
	$effect(() => {
		void key;
		untrack(() => void load());
	});

	onMount(() => {
		if (!browser) return;
		const timer = setInterval(() => {
			if (document.visibilityState === 'visible') void load();
		}, 60_000);
		return () => clearInterval(timer);
	});

	function when(iso: string): string {
		return dayLabel(iso) === 'Today' ? clockTime(iso) : relativeTime(iso);
	}
</script>

{#if rows.length > 0}
	<section class="hk-section" aria-labelledby="recent-activity" data-testid="recent-activity">
		<div class="hk-section-head">
			<h2 id="recent-activity" class="hk-h2-app">Recent activity</h2>
			<!-- No cross-project route, so this asks the ten machines started
			     most recently; said only when it bites. -->
			{#if running > FAN_OUT}
				<span>Across the {FAN_OUT} machines started most recently, of {running} running.</span>
			{/if}
		</div>
		<ol class="ra">
			{#each rows as e (`${e.project.id}-${e.id}`)}
				{@const k = eventKind(e.kind, e.agent)}
				<li class="ra-row">
					<time class="ra-time" datetime={e.ts} title={dateTime(e.ts)}>{when(e.ts)}</time>
					<span class="ra-kind"
						><span class="hk-dot {toneDot(k.tone)} kdot" aria-hidden="true"></span>{k.label}</span
					>
					<a href={resolve('/projects/[id]', { id: e.project.id })} class="ra-project"
						><span class="hk-sr">Project </span>{e.project.name}</a
					>
					<span class="ra-summary" title={e.summary}
						>{#if e.agent}<span class="ra-agent">{e.agent}</span
							>{/if}{e.summary}{#if e.more > 0}<span class="ra-more">and {e.more} more</span
							>{/if}</span
					>
				</li>
			{/each}
		</ol>
	</section>
{/if}

<style>
	.ra {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.ra-row {
		display: grid;
		grid-template-columns: 5.5rem 7rem 9rem minmax(0, 1fr);
		align-items: baseline;
		column-gap: 1rem;
		padding: 0.625rem 0;
		border-top: 1px solid var(--hk-hairline);
	}
	.ra-time {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text-3);
	}
	.ra-kind {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		white-space: nowrap;
	}
	/* The kind's 6px cue, as on a project's Events list (EventRow). */
	.kdot {
		width: 6px;
		height: 6px;
		margin-right: 0.375rem;
		vertical-align: 0.0625rem;
	}
	.ra-project {
		justify-self: start;
		max-width: 100%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.8125rem;
		color: var(--hk-text);
		text-decoration: underline;
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
		text-underline-offset: 3px;
	}
	.ra-project:hover {
		text-decoration-color: currentColor;
	}
	.ra-summary {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.875rem;
		color: var(--hk-text);
	}
	.ra-agent {
		margin-right: 0.5rem;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.ra-more {
		margin-left: 0.5rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	/* A phone: the time, the kind and the project on one line, the summary
	   under them, wrapping. */
	@media (max-width: 639.98px) {
		.ra-row {
			grid-template-columns: auto auto minmax(0, 1fr);
			row-gap: 0.25rem;
		}
		.ra-summary {
			grid-column: 1 / -1;
			white-space: normal;
		}
	}
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.ra-project {
			padding-block: 0.8125rem;
			margin-block: -0.8125rem;
		}
	}
</style>
