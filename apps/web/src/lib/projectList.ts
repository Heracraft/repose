// What the projects list shows, and in what order. The api answers GET
// /projects in `order by created_at` (internal/api/store/store.go), which
// is the order a user made them in and no use for reading: an account may
// hold 100 projects on any plan (docs/features/projects.md "Limits",
// I-569), so the machines that are running sit wherever their creation
// date put them. These helpers are pure, so the rules are tested without
// a browser.
import type { Project } from '$lib/api/types';

// The plan's memory states live in $lib/plans.ts, beside the figures they
// are counted against, and are pinned to internal/billing/usage.go there.
export { MEMORY_STATES, holdsMemory } from '$lib/plans';

/**
 * Whether a machine is down: nothing is running and nothing is on its
 * way. `error` counts, because the row offers such a machine Start, the
 * same as a stopped one. Every other state in the enum
 * (lib/api/types.ts) is a machine doing something, so no state falls
 * through.
 */
export const atRest = (p: Pick<Project, 'state'>) =>
	p.state === 'stopped' || p.state === 'destroyed' || p.state === 'error';

/** Whether a machine wants the user: an agent's question, or a failure. */
export const needsYou = (p: Pick<Project, 'state'>, waiting: boolean) =>
	waiting || p.state === 'error';

/** The list's three bands, in the order they are shown. */
export const BANDS = ['waiting', 'up', 'off'] as const;
export type Band = (typeof BANDS)[number];

/** Which band a row sits in, which is the order the list is drawn in. */
export function band(p: Project, waiting: boolean): Band {
	if (needsYou(p, waiting)) return 'waiting';
	return atRest(p) ? 'off' : 'up';
}

/**
 * When the machine was last doing something, for ordering inside a band
 * and for the row's own "last run" line: its last start, its last
 * snapshot (a stop writes one) or, for a machine never started, when it
 * was made.
 */
export function lastActive(p: Project): string {
	let t = p.created_at;
	if (p.started_at && p.started_at > t) t = p.started_at;
	if (p.last_snapshot_at && p.last_snapshot_at > t) t = p.last_snapshot_at;
	return t;
}

/**
 * The list's order: the machines that want the user, then the ones that
 * are up, then the ones at rest; inside a band the most recently active
 * first, and by name where that ties. Nothing in it moves on a poll
 * unless the machine itself changed, so no row jumps under the pointer.
 */
export function orderProjects(projects: Project[], waiting: (p: Project) => boolean): Project[] {
	const rank = new Map(projects.map((p) => [p.id, BANDS.indexOf(band(p, waiting(p)))]));
	return [...projects].sort((a, b) => {
		const d = rank.get(a.id)! - rank.get(b.id)!;
		if (d !== 0) return d;
		const ta = lastActive(a);
		const tb = lastActive(b);
		if (ta !== tb) return ta < tb ? 1 : -1;
		return a.name.localeCompare(b.name);
	});
}

/** The state filter above the list; `all` is the default and writes no URL state. */
export const FILTERS = ['all', 'waiting', 'up', 'off'] as const;
export type Filter = (typeof FILTERS)[number];

/** What each filter is called above the list. */
export const FILTER_LABELS: Record<Filter, string> = {
	all: 'All',
	waiting: 'Needs you',
	up: 'Running',
	off: 'Stopped'
};

/**
 * Whether a project passes the state filter. The filters are not the
 * bands: a running machine with a question is under Running as well as
 * under Needs you, because someone filtering Running wants every machine
 * that is up.
 */
export function matchesFilter(p: Project, f: Filter, waiting: boolean): boolean {
	switch (f) {
		case 'all':
			return true;
		case 'waiting':
			return needsYou(p, waiting);
		case 'up':
			return !atRest(p);
		case 'off':
			return atRest(p);
	}
}

/**
 * Whether a project matches what was typed: a case-insensitive substring
 * of its name, its slug or its remote, so both `todo` and `heracraft`
 * find github.com/heracraft/todo-app.
 */
export function matchesQuery(p: Project, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (!q) return true;
	return (
		p.name.toLowerCase().includes(q) ||
		p.slug.toLowerCase().includes(q) ||
		(p.remote_url ?? '').toLowerCase().includes(q)
	);
}

/** A filter from the URL's `state`; anything else is All. */
export function readFilter(q: URLSearchParams): Filter {
	const f = q.get('state');
	return FILTERS.includes(f as Filter) ? (f as Filter) : 'all';
}

/** What was typed, from the URL's `q`. */
export const readQuery = (q: URLSearchParams) => q.get('q') ?? '';

/**
 * The URL's query for a filter and a search, keeping every other
 * parameter as it was. The default filter and an empty search write
 * nothing, so the plain list has a plain URL.
 */
export function listSearch(current: URLSearchParams, f: Filter, query: string): string {
	const q = new URLSearchParams(current);
	q.delete('state');
	q.delete('q');
	if (f !== 'all') q.set('state', f);
	if (query.trim()) q.set('q', query.trim());
	const str = q.toString();
	return str ? `?${str}` : '';
}

/**
 * Rows shown before "Show all N". Every other growing list on the
 * dashboard is capped the same way (snapshots 8, config revisions 8,
 * Recently destroyed 10, DESIGN-LANGUAGE "Card layout"); the live list
 * is the one that grows to 100 and never was.
 */
export const PROJECTS_SHOWN = 25;

/** Above this many rows the list is worth a search box and filters. */
export const TOOLBAR_FROM = 8;

/**
 * The order is taken when the page arrives and whenever a project
 * appears or disappears, and held in between: a machine the user stops
 * keeps its place instead of sliding down the list, or out of the 25
 * drawn, under the pointer that just pressed Stop. A question arriving
 * is surfaced by the "Waiting on you" card above the list and the row's
 * own badge (I-693), which is the channel for it; the row does not have
 * to jump for the user to see it.
 *
 * The key changes only when the set of projects does.
 */
export const idSetKey = (projects: Project[]) =>
	projects
		.map((p) => p.id)
		.sort()
		.join(',');

/** Sorts by a held order, newcomers (not in it) keeping the live order's place. */
export function byHeldOrder(held: string[]): (a: Project, b: Project) => number {
	const at = new Map(held.map((id, i) => [id, i]));
	return (a, b) =>
		(at.get(a.id) ?? Number.MAX_SAFE_INTEGER) - (at.get(b.id) ?? Number.MAX_SAFE_INTEGER);
}
