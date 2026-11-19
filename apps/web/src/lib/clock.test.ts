import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The page's 30-second clock, which the question rows draw their "asked 3m
// ago" and "expires in 4m" against. read() is for a fact that has just
// arrived: a question held open (I-1242) comes back from the api as five
// minutes from now, and measured against a tick up to 30 seconds old
// minutesLeft rounds that up to a minute more than the owner has.

describe('clock', () => {
	beforeEach(() => {
		vi.resetModules();
		vi.useFakeTimers();
		vi.setSystemTime(new Date('2026-11-19T10:00:00Z'));
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it('ticks every 30 seconds while a user holds it, and stops with the last one', async () => {
		const { clock } = await import('./clock.svelte');
		const stop = clock.use();
		const start = clock.now;
		vi.advanceTimersByTime(29_000);
		expect(clock.now).toBe(start);
		vi.advanceTimersByTime(1_000);
		expect(clock.now).toBe(start + 30_000);
		stop();
		vi.advanceTimersByTime(60_000);
		expect(clock.now).toBe(start + 30_000);
	});

	it('read() takes the time between ticks', async () => {
		const { clock } = await import('./clock.svelte');
		const stop = clock.use();
		const start = clock.now;
		vi.advanceTimersByTime(4_000);
		expect(clock.now).toBe(start);
		expect(clock.read()).toBe(start + 4_000);
		expect(clock.now).toBe(start + 4_000);
		stop();
	});
});
