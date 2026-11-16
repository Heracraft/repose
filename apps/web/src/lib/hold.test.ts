import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HOLD_EVERY_MS, Holder } from './hold';

// A held question is held at once and then every minute, once per id
// however often the row says so, and stops when told (I-1242).
describe('Holder', () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it('holds at once, then every HOLD_EVERY_MS, until stopped', () => {
		const h = new Holder();
		const hold = vi.fn();
		h.start('q1', hold);
		expect(hold).toHaveBeenCalledTimes(1);
		expect(h.holding('q1')).toBe(true);
		vi.advanceTimersByTime(HOLD_EVERY_MS - 1);
		expect(hold).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(1);
		expect(hold).toHaveBeenCalledTimes(2);
		vi.advanceTimersByTime(HOLD_EVERY_MS);
		expect(hold).toHaveBeenCalledTimes(3);
		h.stop('q1');
		expect(h.holding('q1')).toBe(false);
		vi.advanceTimersByTime(5 * HOLD_EVERY_MS);
		expect(hold).toHaveBeenCalledTimes(3);
	});

	it('starts one timer per id, however often the row says holding', () => {
		const h = new Holder();
		const hold = vi.fn();
		h.start('q1', hold);
		h.start('q1', hold);
		h.start('q1', hold);
		expect(hold).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(HOLD_EVERY_MS);
		expect(hold).toHaveBeenCalledTimes(2);
	});

	it('stops every id at once, and a stop for an unknown id is nothing', () => {
		const h = new Holder(1000);
		const a = vi.fn();
		const b = vi.fn();
		h.start('a', a);
		h.start('b', b);
		h.stop('zzz');
		h.stopAll();
		vi.advanceTimersByTime(10_000);
		expect(a).toHaveBeenCalledTimes(1);
		expect(b).toHaveBeenCalledTimes(1);
		expect(h.holding('a')).toBe(false);
	});
});
