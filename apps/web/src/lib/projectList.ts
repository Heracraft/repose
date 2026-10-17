// What the projects list shows, and in what order. The api answers GET
// /projects in `order by created_at` (internal/api/store/store.go), which
// is the order a user made them in and no use for reading: an account may
// hold 100 projects on any plan (docs/features/projects.md "Limits",
// I-569), so the three that are running sit wherever their creation date
// put them. These helpers are pure, so the rules are tested without a
// browser.
import type { Project } from '$lib/api/types';

/**
 * The states that hold the plan's memory: running, and the ones on their
 * way to running. The same five as internal/billing/usage.go's
 * memoryStates, which is what refuses a start, so a page that counts any
 * other set tells the user a figure the api will not honour.
 */
export const MEMORY_STATES: ReadonlySet<Project['state']> = new Set([
	'running',
	'starting',
	'restoring',
	'creating',
	'building'
]);

/** Whether a machine is holding the plan's memory right now. */
export const holdsMemory = (p: Pick<Project, 'state'>) => MEMORY_STATES.has(p.state);

/** The list's three bands, in the order they are shown. */
export const BANDS = ['waiting', 'up', 'off'] as const;
export type Band = (typeof BANDS)[number];

/**
 * Which band a row sits in. `waiting` is anything that wants the user:
 * an agent's question, or a machine that failed. `up` is a machine
 * holding memory. Everything else — stopped, stopping, being destroyed —
 * is `off`.
 */
export function band(p: Project, waiting: boolean): Band {
	if (waiting || p.state === 'error') return 'waiting';
	if (holdsMemory(p)) return 'up';
	return 'off';
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
 * are up, then the rest; inside a band the most recently active first,
 * and by name where that ties. Stable: nothing in it changes on a poll
 * unless the machine itself did, so no row moves under the pointer.
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

/** The state filter above the list; `all` is the default and has no link state. */
export const FILTERS = ['all', 'up', 'off'] as const;
export type Filter = (typeof FILTERS)[number];

/** What each filter is called above the list. */
export const FILTER_LABELS: Record<Filter, string> = {
	all: 'All',
	up: 'Running',
	off: 'Stopped'
};

/** Whether a project passes the state filter. */
export function matchesFilter(p: Project, f: Filter): boolean {
	if (f === 'all') return true;
	return f === 'up' ? holdsMemory(p) : !holdsMemory(p);
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
 * Recently destroyed 10, DESIGN-LANGUAGE "Card layout"); the list is the
 * one that grows to 100.
 */
export const PROJECTS_SHOWN = 25;
