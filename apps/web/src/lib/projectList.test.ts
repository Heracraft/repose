import { describe, expect, it } from 'vitest';
import type { Project } from '$lib/api/types';
import {
	atRest,
	band,
	holdsMemory,
	lastActive,
	byHeldOrder,
	idSetKey,
	listSearch,
	matchesFilter,
	matchesQuery,
	orderProjects,
	readFilter,
	readQuery
} from './projectList';

/** A project with only the fields the order and the filters read. */
function p(over: Partial<Project> & { name: string }): Project {
	return {
		id: over.id ?? over.name,
		slug: over.slug ?? over.name,
		remote_url: '',
		class: 'large',
		state: 'stopped',
		agent_default: 'claude',
		hold_base_updates: false,
		base_version: '2026.09.15',
		config_revision_id: 'r1',
		volume_bytes: 0,
		created_at: '2026-01-01T00:00:00Z',
		cost_today_cents: 0,
		cost_month_cents: 0,
		...over
	} as Project;
}

const none = () => false;

describe('holdsMemory and atRest', () => {
	it('counts the five states the api charges memory for', () => {
		for (const state of ['running', 'starting', 'restoring', 'creating', 'building'] as const) {
			expect(holdsMemory({ state })).toBe(true);
		}
		for (const state of ['stopped', 'stopping', 'destroying', 'destroyed', 'error'] as const) {
			expect(holdsMemory({ state })).toBe(false);
		}
	});

	it('calls a machine that is down at rest, whichever way it got there', () => {
		expect(atRest({ state: 'stopped' })).toBe(true);
		expect(atRest({ state: 'destroyed' })).toBe(true);
		// Down, and the row offers it Start, as a stopped one.
		expect(atRest({ state: 'error' })).toBe(true);
		// Still doing something, so still worth seeing above the stopped ones.
		expect(atRest({ state: 'stopping' })).toBe(false);
		expect(atRest({ state: 'building' })).toBe(false);
	});
});

describe('band', () => {
	it('puts a question and a failure first, then what is up, then what is at rest', () => {
		expect(band(p({ name: 'a', state: 'running' }), true)).toBe('waiting');
		expect(band(p({ name: 'b', state: 'error' }), false)).toBe('waiting');
		expect(band(p({ name: 'c', state: 'running' }), false)).toBe('up');
		expect(band(p({ name: 'd', state: 'stopping' }), false)).toBe('up');
		expect(band(p({ name: 'e', state: 'stopped' }), false)).toBe('off');
	});

	it('leaves no state of the enum without a band', () => {
		const states = [
			'creating',
			'building',
			'starting',
			'running',
			'stopping',
			'stopped',
			'restoring',
			'destroying',
			'destroyed',
			'error'
		] as const;
		for (const state of states) {
			expect(['waiting', 'up', 'off']).toContain(band(p({ name: state, state }), false));
		}
	});
});

describe('lastActive', () => {
	it('takes the newest of created, started and the last snapshot', () => {
		expect(
			lastActive(
				p({
					name: 'a',
					created_at: '2026-01-01T00:00:00Z',
					started_at: '2026-03-01T00:00:00Z',
					last_snapshot_at: '2026-02-01T00:00:00Z'
				})
			)
		).toBe('2026-03-01T00:00:00Z');
	});

	it('falls back to when it was made, for a machine never started', () => {
		expect(lastActive(p({ name: 'a', created_at: '2026-01-01T00:00:00Z' }))).toBe(
			'2026-01-01T00:00:00Z'
		);
	});
});

describe('orderProjects', () => {
	it('lifts the running machines out of creation order', () => {
		// The owner's case: ten projects, seven stopped, the running ones
		// wherever `order by created_at` put them.
		const list = Array.from({ length: 10 }, (_, i) =>
			p({
				name: `p${i}`,
				state: [1, 4, 9].includes(i) ? 'running' : 'stopped',
				created_at: `2026-01-${String(i + 1).padStart(2, '0')}T00:00:00Z`,
				started_at: [1, 4, 9].includes(i) ? `2026-02-0${i % 9}T00:00:00Z` : undefined
			})
		);
		const names = orderProjects(list, none).map((x) => x.name);
		expect(names.slice(0, 3).sort()).toEqual(['p1', 'p4', 'p9']);
	});

	it('puts a project an agent is waiting on above a running one', () => {
		const asked = p({ name: 'asked', state: 'running', started_at: '2026-01-01T00:00:00Z' });
		const busy = p({ name: 'busy', state: 'running', started_at: '2026-06-01T00:00:00Z' });
		expect(orderProjects([busy, asked], (x) => x.name === 'asked').map((x) => x.name)).toEqual([
			'asked',
			'busy'
		]);
	});

	it('puts a failed machine above a healthy one', () => {
		const broken = p({ name: 'broken', state: 'error', created_at: '2026-01-01T00:00:00Z' });
		const fine = p({ name: 'fine', state: 'running', created_at: '2026-06-01T00:00:00Z' });
		expect(orderProjects([fine, broken], none).map((x) => x.name)).toEqual(['broken', 'fine']);
	});

	it('orders inside a band by what was active most recently', () => {
		const old = p({ name: 'old', state: 'stopped', created_at: '2026-01-01T00:00:00Z' });
		const recent = p({
			name: 'recent',
			state: 'stopped',
			created_at: '2026-01-01T00:00:00Z',
			last_snapshot_at: '2026-09-01T00:00:00Z'
		});
		expect(orderProjects([old, recent], none).map((x) => x.name)).toEqual(['recent', 'old']);
	});

	it('breaks a tie by name, so the order never depends on what the api sent first', () => {
		const a = p({ name: 'alpha', state: 'stopped' });
		const b = p({ name: 'beta', state: 'stopped' });
		expect(orderProjects([b, a], none).map((x) => x.name)).toEqual(['alpha', 'beta']);
		expect(orderProjects([a, b], none).map((x) => x.name)).toEqual(['alpha', 'beta']);
	});

	it('does not change the array it was given', () => {
		const list = [p({ name: 'b', state: 'stopped' }), p({ name: 'a', state: 'running' })];
		orderProjects(list, none);
		expect(list.map((x) => x.name)).toEqual(['b', 'a']);
	});
});

describe('matchesFilter', () => {
	const running = p({ name: 'r', state: 'running' });
	const stopped = p({ name: 's', state: 'stopped' });
	const broken = p({ name: 'e', state: 'error' });

	it('keeps everything under All', () => {
		for (const x of [running, stopped, broken]) expect(matchesFilter(x, 'all', false)).toBe(true);
	});

	it('keeps a machine with a question under Running as well as under Needs you', () => {
		expect(matchesFilter(running, 'up', true)).toBe(true);
		expect(matchesFilter(running, 'waiting', true)).toBe(true);
	});

	it('puts a failed machine under Needs you and under Stopped, not Running', () => {
		expect(matchesFilter(broken, 'waiting', false)).toBe(true);
		expect(matchesFilter(broken, 'up', false)).toBe(false);
	});

	it('separates running from stopped', () => {
		expect(matchesFilter(running, 'up', false)).toBe(true);
		expect(matchesFilter(running, 'off', false)).toBe(false);
		expect(matchesFilter(stopped, 'off', false)).toBe(true);
		expect(matchesFilter(stopped, 'up', false)).toBe(false);
	});
});

describe('matchesQuery', () => {
	const proj = p({
		name: 'todo-app',
		slug: 'todo-app',
		remote_url: 'https://github.com/heracraft/todo-app.git'
	});

	it('keeps everything for an empty or blank search', () => {
		expect(matchesQuery(proj, '')).toBe(true);
		expect(matchesQuery(proj, '   ')).toBe(true);
	});

	it('matches the name, whatever the case', () => {
		expect(matchesQuery(proj, 'TODO')).toBe(true);
		expect(matchesQuery(proj, 'do-ap')).toBe(true);
	});

	it('matches the remote, so an org finds its projects', () => {
		expect(matchesQuery(proj, 'heracraft')).toBe(true);
	});

	it('does not match what is not there', () => {
		expect(matchesQuery(proj, 'invoice')).toBe(false);
	});
});

describe('the URL', () => {
	it('reads a filter and a search back', () => {
		const q = new URLSearchParams('state=up&q=todo');
		expect(readFilter(q)).toBe('up');
		expect(readQuery(q)).toBe('todo');
	});

	it('falls back to All for a filter it does not know', () => {
		expect(readFilter(new URLSearchParams('state=sideways'))).toBe('all');
		expect(readFilter(new URLSearchParams())).toBe('all');
	});

	it('writes nothing for the plain list', () => {
		expect(listSearch(new URLSearchParams(), 'all', '')).toBe('');
		expect(listSearch(new URLSearchParams(), 'all', '  ')).toBe('');
	});

	it('writes the filter and the search, trimmed', () => {
		expect(listSearch(new URLSearchParams(), 'up', ' todo ')).toBe('?state=up&q=todo');
	});

	it('keeps every other parameter', () => {
		expect(listSearch(new URLSearchParams('restored=1'), 'off', '')).toBe('?restored=1&state=off');
	});

	it('round-trips', () => {
		const s = listSearch(new URLSearchParams(), 'waiting', 'auth');
		const q = new URLSearchParams(s);
		expect(readFilter(q)).toBe('waiting');
		expect(readQuery(q)).toBe('auth');
	});
});

describe('the held order', () => {
	it('changes its key only when a project appears or disappears', () => {
		const a = p({ name: 'a', state: 'running' });
		const b = p({ name: 'b', state: 'stopped' });
		const key = idSetKey([a, b]);
		// A state change is not a change to the set.
		expect(idSetKey([{ ...a, state: 'stopped' } as Project, b])).toBe(key);
		// Nor is the api sending them the other way round.
		expect(idSetKey([b, a])).toBe(key);
		// A new one is.
		expect(idSetKey([a, b, p({ name: 'c' })])).not.toBe(key);
	});

	it('keeps a stopped machine where it was, instead of sliding it away', () => {
		const up = p({ name: 'up', state: 'running' });
		const off = p({ name: 'off', state: 'stopped' });
		const held = orderProjects([up, off], none).map((x) => x.id);
		expect(held).toEqual(['up', 'off']);
		// The user presses Stop: the same two projects, one now stopped.
		const after = [{ ...up, state: 'stopped' } as Project, off].sort(byHeldOrder(held));
		expect(after.map((x) => x.name)).toEqual(['up', 'off']);
	});

	// A project the held order has not seen goes last, for the one render
	// before the id set changes and the order is taken again.
	it('puts a project it has never seen last, until the order is retaken', () => {
		const held = ['a', 'b'];
		const fresh = p({ name: 'c', state: 'running' });
		const sorted = [p({ name: 'b', id: 'b' }), fresh, p({ name: 'a', id: 'a' })].sort(
			byHeldOrder(held)
		);
		expect(sorted.map((x) => x.name)).toEqual(['a', 'b', 'c']);
	});
});
