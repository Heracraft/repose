import { describe, it, expect, vi } from 'vitest';

describe('frozenSince', () => {
	it('is the time only for a running machine with the flag on', async () => {
		vi.resetModules();
		vi.doMock('./flags', () => ({ FROZEN_LOOK: true }));
		const { frozenSince } = await import('./frozen');
		const since = '2026-10-12T10:00:00Z';
		expect(frozenSince({ state: 'running', resting: { since } })).toBe(since);
		expect(frozenSince({ state: 'stopped', resting: { since } })).toBeUndefined();
		expect(frozenSince({ state: 'running', resting: null })).toBeUndefined();
		expect(frozenSince({ state: 'running' })).toBeUndefined();
	});
	it('is never set with the flag off, whatever the api sends', async () => {
		vi.resetModules();
		vi.doMock('./flags', () => ({ FROZEN_LOOK: false }));
		const { frozenSince } = await import('./frozen');
		expect(
			frozenSince({ state: 'running', resting: { since: '2026-10-12T10:00:00Z' } })
		).toBeUndefined();
	});
});
