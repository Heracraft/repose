import { describe, expect, it } from 'vitest';
import {
	readEventsState,
	eventsSearch,
	olderState,
	newerState,
	newestState,
	kindState,
	pageCount,
	positionLabel,
	timeRangeLabel
} from './eventsUrl';

const q = (s: string) => new URLSearchParams(s);

describe('readEventsState', () => {
	it('reads the cursor, page and kind', () => {
		expect(readEventsState(q('events_before=b1&events_page=3&events_kind=errors'))).toEqual({
			before: 'b1',
			page: 3,
			kind: 'errors'
		});
		expect(readEventsState(q('events_after=a1&events_page=2'))).toEqual({ after: 'a1', page: 2 });
	});
	it('drops a page without a cursor, a bad page and an unknown kind', () => {
		expect(readEventsState(q('events_page=4'))).toEqual({});
		expect(readEventsState(q('events_before=b&events_page=0'))).toEqual({ before: 'b' });
		expect(readEventsState(q('events_before=b&events_page=x'))).toEqual({ before: 'b' });
		expect(readEventsState(q('events_before=b&events_page=1'))).toEqual({ before: 'b' });
		expect(readEventsState(q('events_kind=toString'))).toEqual({});
		expect(readEventsState(q('events_kind=nope'))).toEqual({});
	});
	it('keeps before when both cursors are given', () => {
		expect(readEventsState(q('events_before=b&events_after=a'))).toEqual({ before: 'b' });
	});
});

describe('eventsSearch', () => {
	it('writes the state and keeps other parameters', () => {
		expect(
			eventsSearch(q('tab=x&events_after=old'), { before: 'b', page: 2, kind: 'agents' })
		).toBe('?tab=x&events_before=b&events_page=2&events_kind=agents');
	});
	it('is empty for the newest page', () => {
		expect(eventsSearch(q('events_before=b&events_page=2'), {})).toBe('');
	});
	it('round-trips through readEventsState', () => {
		for (const s of [
			{ before: 'b', page: 5 },
			{ after: 'a', page: 2, kind: 'machine' as const },
			{}
		]) {
			expect(readEventsState(q(eventsSearch(q(''), s).slice(1)))).toEqual(s);
		}
	});
});

describe('stepping', () => {
	it('Older counts up from the newest page', () => {
		expect(olderState({}, 'x')).toEqual({ before: 'x', page: 2, kind: undefined });
		expect(olderState({ before: 'y', page: 2, kind: 'errors' }, 'x')).toEqual({
			before: 'x',
			page: 3,
			kind: 'errors'
		});
		// A cold deep link has no number to count from.
		expect(olderState({ before: 'y' }, 'x')).toEqual({
			before: 'x',
			page: undefined,
			kind: undefined
		});
	});
	it('Newer counts down, and page 2 goes to the newest page', () => {
		expect(newerState({ before: 'y', page: 3 }, 'f')).toEqual({
			after: 'f',
			page: 2,
			kind: undefined
		});
		expect(newerState({ before: 'y', page: 2, kind: 'agents' }, 'f')).toEqual({ kind: 'agents' });
		expect(newerState({ after: 'y' }, 'f')).toEqual({
			after: 'f',
			page: undefined,
			kind: undefined
		});
	});
	it('Newest and a filter clear the cursor', () => {
		expect(newestState({ before: 'b', page: 4, kind: 'machine' })).toEqual({ kind: 'machine' });
		expect(newestState({ before: 'b', page: 4 })).toEqual({});
		expect(kindState('errors')).toEqual({ kind: 'errors' });
		expect(kindState(undefined)).toEqual({});
	});
});

describe('labels', () => {
	it('counts pages', () => {
		expect(pageCount(0)).toBe(1);
		expect(pageCount(25)).toBe(1);
		expect(pageCount(26)).toBe(2);
		expect(pageCount(183)).toBe(8);
	});
	it('says the position', () => {
		expect(positionLabel({ page: 3, total: 183 })).toBe('Page 3 of 8');
		expect(positionLabel({ page: 3 })).toBe('Page 3');
		// More pages than the total says (it grew since): never "Page 9 of 8".
		expect(positionLabel({ page: 9, total: 183 })).toBe('Page 9 of 9');
		expect(positionLabel({})).toBe('');
	});
	it('labels a page by its times when its number is not known', () => {
		const at = (h: number, m: number, d = 9) => new Date(2026, 9, d, h, m).toISOString();
		expect(timeRangeLabel(at(15, 10), at(14, 2))).toBe('9 Oct 14:02 to 15:10');
		expect(timeRangeLabel(at(1, 10, 9), at(23, 50, 8))).toBe('8 Oct 23:50 to 9 Oct 01:10');
		expect(timeRangeLabel(at(14, 2), at(14, 2))).toBe('9 Oct 14:02');
		expect(positionLabel({ range: { newest: at(15, 10), oldest: at(14, 2) } })).toBe(
			'9 Oct 14:02 to 15:10'
		);
	});
});
