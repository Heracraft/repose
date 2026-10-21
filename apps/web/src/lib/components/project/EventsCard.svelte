<!--
  A project's events in pages of 10, newest first (I-662). The page's
  place lives in the URL (eventsUrl.ts), so Back steps between pages and a
  reload keeps the page. Newest, Newer and Older move by the api's cursors;
  the position is "Page 3 of 8" from the api's count. With one page there
  is no pager (I-680). A filter keeps the
  agents', the errors' or the machine's events. Only the newest page polls
  for rows; a deeper one polls the count and offers the newer ones in one
  link, so the rows being read never move.
-->
<script module lang="ts">
	import { listEventsPage } from '$lib/api/client';
	import { EVENT_FILTERS } from '$lib/events';
	import type { ProjectEvent } from '$lib/api/types';
	import { EVENTS_PAGE_SIZE, type EventsState } from './eventsUrl';

	/** One loaded page, and the state it was loaded for. */
	export interface LoadedEvents {
		key: string;
		events: ProjectEvent[];
		hasOlder: boolean;
		/** From the api's Repose-Event-Count; undefined from an api older than I-662. */
		total?: number;
	}

	/** The key a state is loaded under: its cursor and filter. */
	export const eventsKey = (s: EventsState) =>
		JSON.stringify([s.before ?? '', s.after ?? '', s.kind ?? '']);

	/**
	 * One page for a state. Older than a cursor, or the newest, asks for one
	 * more than a page to know whether an older page exists; newer than a
	 * cursor always has one (the cursor's own event). 'newest' when a Newer
	 * page came back short: it is the top of the list, which is page 1.
	 */
	export async function fetchEventsPage(
		projectId: string,
		s: EventsState
	): Promise<LoadedEvents | 'newest'> {
		const kinds = s.kind ? [...EVENT_FILTERS[s.kind]] : undefined;
		const key = eventsKey(s);
		if (s.after) {
			const res = await listEventsPage(projectId, {
				after: s.after,
				limit: EVENTS_PAGE_SIZE,
				kinds
			});
			// An api older than I-662 ignores after and sends its newest.
			if (res.total === undefined || res.events.length < EVENTS_PAGE_SIZE) return 'newest';
			return { key, events: res.events, hasOlder: true, total: res.total };
		}
		const res = await listEventsPage(projectId, {
			before: s.before,
			limit: EVENTS_PAGE_SIZE + 1,
			kinds
		});
		return {
			key,
			events: res.events.slice(0, EVENTS_PAGE_SIZE),
			hasOlder: res.events.length > EVENTS_PAGE_SIZE,
			total: res.total
		};
	}
</script>

<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { PollFailure, type PollGroup } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import { dayLabel } from '$lib/format';
	import type { EventFilter } from '$lib/events';
	import EventRow from './EventRow.svelte';
	import {
		readEventsState,
		eventsSearch,
		isNewest,
		olderState,
		newerState,
		newestState,
		kindState,
		positionLabel
	} from './eventsUrl';

	let {
		projectId,
		initial,
		pollFailures
	}: {
		projectId: string;
		/** The page's first load fetched this page, so it paints with the rest. */
		initial?: LoadedEvents;
		pollFailures?: PollGroup;
	} = $props();

	// svelte-ignore state_referenced_locally
	const failure = new PollFailure('Could not load events.', pollFailures);

	const FILTERS: [EventFilter | undefined, string][] = [
		[undefined, 'All'],
		['agents', 'Agents'],
		['errors', 'Errors'],
		['machine', 'Machine']
	];

	let view = $derived(readEventsState(page.url.searchParams));
	let key = $derived(eventsKey(view));

	// svelte-ignore state_referenced_locally
	let loaded = $state<LoadedEvents | undefined>(initial);
	let loading = $state(false);
	/** The count when this page loaded; a deeper page offers what came after. */
	let baseTotal = $state<number | undefined>(undefined);
	let newerCount = $state(0);
	let announce = $state('');

	let card = $state<HTMLElement | undefined>(undefined);
	let list = $state<HTMLUListElement | undefined>(undefined);
	/** Set by a pager or filter click: the list takes focus once its page is in. */
	let focusOnLoad = false;

	let events = $derived(loaded?.events ?? []);
	let total = $derived(loaded?.total);
	let current = $derived(isNewest(view) ? 1 : view.page);
	let position = $derived(
		positionLabel({
			page: current,
			total,
			range:
				events.length > 0
					? { newest: events[0].ts, oldest: events[events.length - 1].ts }
					: undefined
		})
	);
	let onNewest = $derived(isNewest(view));
	let hasOlder = $derived(loaded?.hasOlder ?? false);
	// A short last page keeps the list's height, so the card under it
	// does not jump; a project with one page of events has no such floor.
	let holdHeight = $derived(!onNewest || hasOlder);

	/** This page's URL with the pager at s, the rest of the query kept. */
	function href(s: typeof view): string {
		return `${resolve('/projects/[id]', { id: projectId })}${eventsSearch(page.url.searchParams, s)}`;
	}

	function to(s: typeof view, focus = true) {
		focusOnLoad = focus;
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- href() starts with resolve()
		void goto(href(s), { noScroll: true, keepFocus: true });
	}

	async function load(s: typeof view, k: string) {
		loading = true;
		try {
			const got = await fetchEventsPage(projectId, s);
			if (k !== key) return; // moved on while this was in flight
			if (got === 'newest') {
				// eslint-disable-next-line svelte/no-navigation-without-resolve -- href() starts with resolve()
				await goto(href(newestState(s)), { replaceState: true, noScroll: true, keepFocus: true });
				return;
			}
			loaded = got;
			baseTotal = got.total;
			newerCount = 0;
			failure.ok();
		} catch (err) {
			failure.fail(err);
		} finally {
			if (k === key) loading = false;
		}
		if (focusOnLoad && k === key) {
			focusOnLoad = false;
			await tick();
			list?.focus({ preventScroll: true });
			// Back to the card's top only when its heading has gone up under
			// the sticky header.
			if (card && card.getBoundingClientRect().top < 64) {
				card.scrollIntoView({ block: 'start' });
			}
		}
	}

	$effect(() => {
		const k = key;
		untrack(() => {
			if (loaded?.key === k) {
				if (baseTotal === undefined) baseTotal = loaded.total;
				return;
			}
			void load(view, k);
		});
	});

	/** The newest page reloads its rows; a deeper one only counts. */
	async function poll() {
		const k = key;
		if (loading || loaded?.key !== k) return;
		const s = view;
		try {
			if (isNewest(s)) {
				const got = await fetchEventsPage(projectId, s);
				if (k !== key || loading || got === 'newest') return;
				const seen = new Set(events.map((e) => e.id));
				const fresh = got.events.filter((e) => !seen.has(e.id)).length;
				loaded = got;
				baseTotal = got.total;
				if (fresh > 0) announce = fresh === 1 ? '1 new event' : `${fresh} new events`;
			} else {
				const res = await listEventsPage(projectId, {
					limit: 1,
					kinds: s.kind ? [...EVENT_FILTERS[s.kind]] : undefined
				});
				if (k !== key || res.total === undefined || baseTotal === undefined) return;
				newerCount = Math.max(0, res.total - baseTotal);
			}
			failure.ok();
		} catch (err) {
			failure.fail(err);
		}
	}

	onMount(() => pollWhileVisible(poll));

	/** Each row, with a day's heading before its first row on the page. */
	let rows = $derived.by(() => {
		const out: ({ day: string; id: string } | { event: ProjectEvent; id: string })[] = [];
		let last = '';
		for (const e of events) {
			const d = dayLabel(e.ts);
			if (d !== last) out.push({ day: d, id: `day-${e.id}` });
			last = d;
			out.push({ event: e, id: e.id });
		}
		return out;
	});
</script>

{#snippet pager()}
	{@const newerOff = onNewest}
	{@const olderOff = !hasOlder}
	{@const offerNewer = newerCount > 0 && !onNewest}
	<!-- At the foot of the list, where a reader finishes a page: Newest and
	     Newer, the position (or the newer events' link in its place on a
	     phone), Older. Newest waits for a wider screen. -->
	<div class="pager">
		{#if offerNewer}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm max-sm:hidden"
				onclick={() => to(newestState(view))}>{newerCount} newer</button
			>
		{/if}
		<button
			type="button"
			class="hk-btn-ghost hk-btn-sm max-sm:hidden"
			aria-disabled={newerOff}
			title={newerOff ? 'This is the newest page' : undefined}
			onclick={() => !newerOff && to(newestState(view))}>Newest</button
		>
		<button
			type="button"
			class="hk-btn-ghost hk-btn-sm"
			aria-disabled={newerOff}
			title={newerOff ? 'No newer events' : undefined}
			onclick={() => !newerOff && events.length > 0 && to(newerState(view, events[0].id))}
			>Newer</button
		>
		{#if offerNewer}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm sm:hidden"
				onclick={() => to(newestState(view))}>{newerCount} newer</button
			>
		{/if}
		<span class="position {offerNewer ? 'max-sm:hidden' : ''}" data-testid="events-position"
			>{position}</span
		>
		<button
			type="button"
			class="hk-btn-ghost hk-btn-sm"
			aria-disabled={olderOff}
			title={olderOff ? 'No older events' : undefined}
			onclick={() =>
				!olderOff && events.length > 0 && to(olderState(view, events[events.length - 1].id))}
			>Older</button
		>
	</div>
{/snippet}

<section
	class="hk-section"
	id="events"
	aria-labelledby="events-title"
	data-testid="events-card"
	bind:this={card}
>
	<div class="hk-section-head head">
		<div class="title">
			<h2 id="events-title" class="hk-h2-app">Events</h2>
			{#if total !== undefined}
				<span class="total" data-testid="events-total"
					>{total === 1 ? '1 event' : `${total} events`}</span
				>
			{/if}
		</div>
		<!-- The filter changes the URL, so its choices are links with
		     aria-current. An api with no count ignores kind, so it gets none. -->
		{#if !loaded || total !== undefined}
			<nav aria-label="Filter events" class="hk-seg--links hk-seg--sm filter">
				{#each FILTERS as [kind, name] (name)}
					<!-- eslint-disable svelte/no-navigation-without-resolve -- href() starts with resolve() -->
					<a
						href={href(kindState(kind))}
						aria-current={view.kind === kind ? 'page' : undefined}
						data-sveltekit-noscroll
						data-sveltekit-keepfocus
						onclick={() => (focusOnLoad = true)}>{name}</a
					>
					<!-- eslint-enable svelte/no-navigation-without-resolve -->
				{/each}
			</nav>
		{/if}
	</div>

	<!-- New rows on the newest page are said here, once each poll. -->
	<p class="hk-sr" role="status">{announce}</p>

	{#if loaded && events.length === 0}
		<div class="hk-empty-row">
			<span>{view.kind ? 'No events of this kind.' : 'No events yet.'}</span>
		</div>
	{:else}
		<ul
			bind:this={list}
			aria-label="Events, page {current ?? position}"
			aria-busy={loading}
			tabindex="-1"
			class="events-list"
			class:hold={holdHeight}
			class:loading
		>
			{#each rows as r (r.id)}
				{#if 'day' in r}
					<li class="day-row" data-testid="event-day">{r.day}</li>
				{:else}
					<EventRow event={r.event} />
				{/if}
			{/each}
		</ul>
		<!-- One page has no pager: the count says it all (I-680). -->
		{#if holdHeight || newerCount > 0}
			<nav aria-label="Event pages" class="pager-nav">{@render pager()}</nav>
		{/if}
	{/if}
</section>

<style>
	.head {
		align-items: center;
	}
	.title {
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
	}
	.total {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	@media (max-width: 767.98px) {
		.filter {
			width: 100%;
		}
	}
	.events-list {
		margin: 0;
		padding: 0;
		list-style: none;
		border-bottom: 1px solid var(--hk-hairline);
		transition: opacity 120ms;
	}
	.events-list:focus-visible {
		outline-offset: 4px;
	}
	.events-list.loading {
		opacity: 0.6;
	}
	/* 10 rows of 41px and one day heading; a page across midnight grows
	   by a heading. A short last page keeps the list's height, so the
	   section under it does not jump. */
	.events-list.hold {
		min-height: calc(10 * 2.5625rem + 2.25rem);
	}
	@media (max-width: 639.98px) {
		.events-list.hold {
			min-height: calc(10 * 4rem + 2.25rem);
		}
	}
	.day-row {
		padding: 1rem 0 0.375rem;
		font-size: 0.6875rem;
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.day-row:first-child {
		padding-top: 0.25rem;
	}
	.pager-nav {
		margin-top: 0.5rem;
	}
	.pager {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem;
		margin-left: -0.625rem;
	}
	.position {
		padding: 0 0.5rem;
		min-width: 5.5rem;
		text-align: center;
		white-space: nowrap;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		font-variant-numeric: tabular-nums;
	}
	@media (max-width: 639.98px) {
		.pager {
			display: grid;
			grid-template-columns: auto minmax(0, 1fr) auto;
		}
	}
	/* Forced colours draw a ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.pager {
			margin-inline: 0;
		}
	}
</style>
