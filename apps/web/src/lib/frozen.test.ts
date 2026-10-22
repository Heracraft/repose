import { describe, it, expect } from 'vitest';
import { frozenSince } from './frozen';

describe('frozenSince', () => {
	it('is the time the api sent, for a running machine', () => {
		const since = '2026-10-12T10:00:00Z';
		expect(frozenSince({ state: 'running', resting: { since } })).toBe(since);
		expect(frozenSince({ state: 'running', resting: null })).toBeUndefined();
		expect(frozenSince({ state: 'running' })).toBeUndefined();
	});

	// `resting` is a marker on `running` (I-818 S2): a machine in any other
	// state is never drawn frozen, whatever the api sends.
	it('is never set for a machine that is not running', () => {
		const since = '2026-10-12T10:00:00Z';
		for (const state of ['stopped', 'starting', 'stopping', 'error'] as const) {
			expect(frozenSince({ state, resting: { since } }), state).toBeUndefined();
		}
	});
});
