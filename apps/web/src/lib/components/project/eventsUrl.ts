// The project page's events pager keeps its place in the URL (I-662), so
// Back steps between pages and a reload keeps the page:
//   events_before=<id>  the page starts after (older than) this event
//   events_after=<id>   the page newer than this event (Newer from a deep
//                       link, or a page reached with Newer)
//   events_page=<n>     the page's number, counted from the newest
//   events_kind=<k>     agents, errors or machine (EVENT_FILTERS)
// Page 1 has no cursor. These helpers are pure, so the rules are tested
// without a browser.
import { EVENT_FILTERS, type EventFilter } from '$lib/events';

/** Events on one page, at every width. */
export const EVENTS_PAGE_SIZE = 10;

export interface EventsState {
	before?: string;
	after?: string;
	/** Known from the URL; absent on a cold deep link with a cursor alone. */
	page?: number;
	kind?: EventFilter;
}

const KEYS = ['events_before', 'events_after', 'events_page', 'events_kind'] as const;

function isFilter(k: string | null): k is EventFilter {
	return k !== null && Object.hasOwn(EVENT_FILTERS, k);
}

/** The pager's state from a URL's query. A bad page or kind is dropped; both cursors keep before. */
export function readEventsState(q: URLSearchParams): EventsState {
	const s: EventsState = {};
	const before = q.get('events_before');
	const after = q.get('events_after');
	if (before) s.before = before;
	else if (after) s.after = after;
	const n = Number(q.get('events_page'));
	if (Number.isInteger(n) && n >= 1) s.page = n;
	const kind = q.get('events_kind');
	if (isFilter(kind)) s.kind = kind;
	// Page 1 with a cursor is not page 1; without one it is.
	if (!s.before && !s.after) s.page = undefined;
	if (s.page === 1 && (s.before || s.after)) s.page = undefined;
	return s;
}

/** The URL's query for a state, keeping every other parameter as it was. */
export function eventsSearch(current: URLSearchParams, s: EventsState): string {
	const q = new URLSearchParams(current);
	for (const k of KEYS) q.delete(k);
	if (s.before) q.set('events_before', s.before);
	else if (s.after) q.set('events_after', s.after);
	if ((s.before || s.after) && s.page !== undefined && s.page > 1)
		q.set('events_page', String(s.page));
	if (s.kind) q.set('events_kind', s.kind);
	const str = q.toString();
	return str ? `?${str}` : '';
}

/** Whether a state is the newest page: no cursor. */
export const isNewest = (s: EventsState) => !s.before && !s.after;

/** The page older than one that ends at lastId. */
export function olderState(s: EventsState, lastId: string): EventsState {
	const page = isNewest(s) ? 2 : s.page !== undefined ? s.page + 1 : undefined;
	return { before: lastId, page, kind: s.kind };
}

/** The page newer than one that starts at firstId; page 1 has no cursor. */
export function newerState(s: EventsState, firstId: string): EventsState {
	if (s.page !== undefined && s.page - 1 <= 1) return newestState(s);
	return { after: firstId, page: s.page !== undefined ? s.page - 1 : undefined, kind: s.kind };
}

/** The newest page, under the same filter. */
export function newestState(s: EventsState): EventsState {
	return s.kind ? { kind: s.kind } : {};
}

/** A filter's first page; undefined is All. */
export function kindState(kind: EventFilter | undefined): EventsState {
	return kind ? { kind } : {};
}

/** The number of pages for a total, at least one. */
export function pageCount(total: number, size = EVENTS_PAGE_SIZE): number {
	return Math.max(1, Math.ceil(total / size));
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const pad2 = (n: number) => String(n).padStart(2, '0');
const day = (d: Date) => `${d.getDate()} ${MONTHS[d.getMonth()]}`;
const clock = (d: Date) => `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;

/**
 * A page's span in local time, oldest to newest: "9 Oct 14:02 to 15:10",
 * or "8 Oct 23:50 to 9 Oct 01:10" across midnight; one time for one event.
 * For a page whose number is not known (a cold deep link).
 */
export function timeRangeLabel(newestIso: string, oldestIso: string): string {
	const a = new Date(oldestIso);
	const b = new Date(newestIso);
	if (a.getTime() === b.getTime()) return `${day(a)} ${clock(a)}`;
	const sameDay =
		a.getFullYear() === b.getFullYear() &&
		a.getMonth() === b.getMonth() &&
		a.getDate() === b.getDate();
	return sameDay
		? `${day(a)} ${clock(a)} to ${clock(b)}`
		: `${day(a)} ${clock(a)} to ${day(b)} ${clock(b)}`;
}

/**
 * The pager's position: "Page 3 of 8" with a total, "Page 3" without
 * one, and the page's time range when its number is not known.
 */
export function positionLabel(o: {
	page?: number;
	total?: number;
	range?: { newest: string; oldest: string };
}): string {
	if (o.page === undefined) return o.range ? timeRangeLabel(o.range.newest, o.range.oldest) : '';
	if (o.total === undefined) return `Page ${o.page}`;
	return `Page ${o.page} of ${Math.max(o.page, pageCount(o.total))}`;
}
