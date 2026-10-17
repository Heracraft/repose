<!--
  The newest events across the running projects, under the projects table.
  There is no cross-project events route: this asks GET
  /projects/:id/events?limit=10 of each running project (the ten started
  most recently at most), merges the answers by time and keeps the newest
  ten. If that fan-out ever costs too much, a cross-project GET /events
  would replace it. It refreshes every 60s while the tab is visible, and
  is not drawn while there is nothing to show.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { listEvents } from '$lib/api/client';
	import { eventKind } from '$lib/events';
	import { clockTime, dateTime, dayLabel, relativeTime } from '$lib/format';
	import type { Project, ProjectEvent } from '$lib/api/types';

	let { projects }: { projects: Project[] } = $props();

	const SHOWN = 10;
	const FAN_OUT = 10;

	interface Row extends ProjectEvent {
		project: Pick<Project, 'id' | 'name'>;
	}

	let rows = $state<Row[]>([]);

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
	async function load() {
		const mine = ++seq;
		const targets = asked.map((p) => ({ id: p.id, name: p.name }));
		const pages = await Promise.allSettled(targets.map((p) => listEvents(p.id, { limit: SHOWN })));
		if (mine !== seq) return;
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
		rows = merged.slice(0, SHOWN);
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
	<section class="mt-14" aria-labelledby="recent-activity" data-testid="recent-activity">
		<h2 id="recent-activity" class="text-xl font-semibold">Recent activity</h2>
		<!-- There is no cross-project events route, so this asks the ten
		     machines started most recently. Said only when it bites: with
		     more running than that, a heading reading "Recent activity"
		     over a sample of ten is a false claim about what happened. -->
		{#if running > FAN_OUT}
			<p class="mt-1 text-sm text-ink-muted">
				Across the {FAN_OUT} machines started most recently, of {running} running.
			</p>
		{/if}
		<ol class="ra mt-4">
			{#each rows as e (`${e.project.id}-${e.id}`)}
				{@const k = eventKind(e.kind, e.agent)}
				<li class="ra-row">
					<time
						class="ra-time font-mono text-compact text-ink-muted tabular-nums"
						datetime={e.ts}
						title={dateTime(e.ts)}>{when(e.ts)}</time
					>
					<span class="ra-kind ra-kind--{k.tone} text-xs">{k.label.replace(/[._]/g, ' ')}</span>
					<a
						href={resolve('/projects/[id]', { id: e.project.id })}
						class="ra-project font-mono text-compact"
						><span class="sr-only">Project </span>{e.project.name}</a
					>
					<span class="ra-summary text-sm" title={e.summary}
						>{#if e.agent}<span class="mr-1.5 font-mono text-compact text-ink-muted">{e.agent}</span
							>
						{/if}{e.summary}</span
					>
				</li>
			{/each}
		</ol>
	</section>
{/if}

<style>
	.ra {
		border-top: 1px solid var(--rule-strong);
	}
	.ra-row {
		display: grid;
		grid-template-columns: 4.5rem 6rem 8rem minmax(0, 1fr);
		align-items: baseline;
		column-gap: 1rem;
		padding: 0.625rem 1rem;
		border-bottom: 1px solid var(--rule);
	}
	.ra-kind {
		white-space: nowrap;
		color: var(--ink-muted);
	}
	.ra-kind--error {
		color: var(--color-red-700);
	}
	.ra-kind--attention {
		color: var(--color-amber-800);
	}
	.ra-kind--ok {
		color: var(--color-emerald-700);
	}
	@media (prefers-color-scheme: dark) {
		.ra-kind--error {
			color: var(--color-red-400);
		}
		.ra-kind--attention {
			color: var(--color-amber-300);
		}
		.ra-kind--ok {
			color: var(--color-emerald-300);
		}
	}
	.ra-project {
		justify-self: start;
		max-width: 100%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--ink);
		text-decoration: underline;
		text-decoration-color: var(--rule-strong);
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
		color: var(--ink);
	}
	/* On a phone: the time, the kind and the project on one line, the
	   summary under them. */
	@media (width < 40rem) {
		.ra-row {
			grid-template-columns: auto auto minmax(0, 1fr);
			row-gap: 0.25rem;
		}
		.ra-summary {
			grid-column: 1 / -1;
		}
	}
</style>
