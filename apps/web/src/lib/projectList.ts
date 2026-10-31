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

/**
 * The state filter above the list. Running is the default and writes no
 * URL state (the owner's call, 2026-10-17: "Project list defaults to
 * Running"): an account holds up to 100 projects, most of them stopped,
 * and the ones that are up or want you are the ones read daily (I-841).
 */
export const FILTERS = ['up', 'off', 'all'] as const;
export type Filter = (typeof FILTERS)[number];
export const DEFAULT_FILTER: Filter = 'up';

/** What each filter is called above the list. */
export const FILTER_LABELS: Record<Filter, string> = {
	up: 'Running',
	off: 'Stopped',
	all: 'All'
};

/**
 * Whether a project passes the state filter. Running keeps every machine
 * that is up and every one that wants you, a failed one included: its
 * row leads the list under "Needs you", so the default view never hides
 * a machine the user has to act on. Stopped keeps the rest, the machines
 * of the "Stopped" band, so its count is the one the rail and Running's
 * "All N stopped" show: a failed machine is counted once, where it leads.
 */
export function matchesFilter(p: Project, f: Filter, waiting: boolean): boolean {
	switch (f) {
		case 'all':
			return true;
		case 'up':
			return !atRest(p) || needsYou(p, waiting);
		case 'off':
			return band(p, waiting) === 'off';
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

/**
 * Whether what was typed matches a project only on its remote, so the
 * row shows the remote that matched even where it hides it (a stopped
 * row, a stacked row).
 */
export function matchesOnRemote(p: Project, query: string): boolean {
	const q = query.trim().toLowerCase();
	return (
		!!q &&
		!p.name.toLowerCase().includes(q) &&
		!p.slug.toLowerCase().includes(q) &&
		(p.remote_url ?? '').toLowerCase().includes(q)
	);
}

/**
 * A filter from the URL's `state`. The two values the list had before
 * Running was the default (`waiting`, Needs you, whose rows now lead
 * Running) read as Running; anything else unknown is Running too.
 */
export function readFilter(q: URLSearchParams): Filter {
	const f = q.get('state');
	return FILTERS.includes(f as Filter) ? (f as Filter) : DEFAULT_FILTER;
}

/** Whether the URL names a filter, rather than leaving the default. */
export const filterChosen = (q: URLSearchParams) => FILTERS.includes(q.get('state') as Filter);

/** What was typed, from the URL's `q`. */
export const readQuery = (q: URLSearchParams) => q.get('q') ?? '';

/**
 * The URL's query for a filter and a search, keeping every other
 * parameter as it was. The default filter and an empty search write
 * nothing, so the plain list has a plain URL. Running chosen with a
 * search is written out (`state=up`), since a search on the unchosen
 * default looks through every project (effectiveFilter); `undefined`
 * is that unchosen default.
 */
export function listSearch(current: URLSearchParams, f: Filter | undefined, query: string): string {
	const q = new URLSearchParams(current);
	q.delete('state');
	q.delete('q');
	if (f && (f !== DEFAULT_FILTER || query.trim())) q.set('state', f);
	if (query.trim()) q.set('q', query.trim());
	const str = q.toString();
	return str ? `?${str}` : '';
}

/**
 * The filter the list is drawn with. A search typed while no filter is
 * chosen looks through every project, stopped ones included: a name the
 * user types is one they want, whatever its state.
 *
 * A short list used to be forced to `all`, which with RECENT_STOPPED at
 * 0 would be the one account that still sees its stopped machines under
 * Running, and it had no toolbar to change that with; the toolbar is
 * now drawn whenever there is a stopped machine to filter out (I-896).
 */
export function effectiveFilter(f: Filter, chosen: boolean, count: number, query: string): Filter {
	if (!chosen && query.trim()) return 'all';
	return f;
}

/**
 * Stopped machines the Running view keeps under it.
 *
 * It was 5, so Running was not a filter: an account with a dozen
 * stopped projects read half of them under the heading it had chosen to
 * exclude them with, and the two durations on a long-unused row ran the
 * table into its own scrollbar. Running now shows what is running, and
 * "All N stopped" in the toolbar is the way to the rest (I-896).
 */
export const RECENT_STOPPED = 0;

/** One band of the drawn list, with its rows in order. */
export interface Group {
	band: Band;
	rows: Project[];
}

/** What the list draws: its bands, and how many stopped rows Running folds. */
export interface ListView {
	/** The filter drawn with, which the filter control marks as current. */
	filter: Filter;
	groups: Group[];
	/** Rows matched before any cap. */
	matched: number;
	/** Machines at rest the Running view leaves out; 0 elsewhere. */
	folded: number;
	/**
	 * Machines at rest, under every filter: what decides whether the
	 * filter control is drawn at all (I-896).
	 */
	resting: number;
}

/**
 * The rows /projects draws, in bands. `ordered` is every project in the
 * held order. Running draws every machine that is up or wants you, and
 * under them the RECENT_STOPPED stopped ones that ran last (0 since
 * I-896, so none). Stopped and All, and any search, cap at `cap` rows
 * until "Show all".
 *
 * `bandOf` is the band each row was in when the order was taken (see
 * byHeldOrder): a machine stopped from its row keeps its place under
 * Running until the set of projects changes, instead of jumping to the
 * stopped rows under the pointer that pressed Stop.
 */
export function listView(
	ordered: Project[],
	opts: {
		filter: Filter;
		chosen: boolean;
		query: string;
		waiting: (p: Project) => boolean;
		bandOf: (p: Project) => Band;
		cap: number;
	}
): ListView {
	let f = effectiveFilter(opts.filter, opts.chosen, ordered.length, opts.query);
	// Running with nothing running is an empty page, not a filter: an
	// account whose machines are all stopped reads them instead, which
	// is what `all` would show anyway since none of them is up (I-896).
	if (f === 'up' && !opts.query.trim() && !ordered.some((p) => opts.bandOf(p) !== 'off')) {
		f = 'off';
	}
	const matched = ordered.filter(
		(p) =>
			(f === 'all' || (opts.bandOf(p) === 'off') === (f === 'off')) && matchesQuery(p, opts.query)
	);
	let rows: Project[];
	let folded = 0;
	// Machines at rest, counted under every filter. The toolbar is drawn
	// whenever this is above 0, since the filter control is the only way
	// to the stopped rows now that Running draws none of them; computing
	// it only under Running took the toolbar away as soon as the reader
	// pressed Stopped, and left them there (I-896).
	const resting = ordered.filter((p) => opts.bandOf(p) === 'off').length;
	if (f === 'up') {
		const rest = ordered
			.filter((p) => opts.bandOf(p) === 'off')
			.sort((a, b) => (lastActive(a) < lastActive(b) ? 1 : lastActive(a) > lastActive(b) ? -1 : 0));
		// Running chosen beside a search keeps to what is up.
		const recent = opts.query.trim() ? [] : rest.slice(0, RECENT_STOPPED);
		folded = opts.query.trim() ? 0 : rest.length - recent.length;
		rows = [...matched, ...recent];
	} else {
		rows = matched.slice(0, opts.cap);
	}
	// One group per band, in BANDS order, whatever order the rows came in
	// (before the order is first held, they come in the api's).
	const groups: Group[] = BANDS.map((b) => ({
		band: b,
		rows: rows.filter((p) => opts.bandOf(p) === b)
	})).filter((g) => g.rows.length > 0);
	return {
		filter: f,
		groups,
		matched: f === 'up' ? rows.length + folded : matched.length,
		folded,
		resting
	};
}

/** The heads over the bands where more than one is drawn. */
export const BAND_LABELS: Record<Band, string> = {
	waiting: 'Needs you',
	up: 'Running',
	off: 'Stopped'
};

/**
 * Rows shown before "Show all N". Every other growing list on the
 * dashboard is capped the same way (snapshots 8, config revisions 8,
 * Recently destroyed 10, DESIGN-LANGUAGE "Long lists"); the live list
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
