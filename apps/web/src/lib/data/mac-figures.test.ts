import { describe, expect, it } from 'vitest';
import {
	REST,
	RESTART,
	SNAPSHOT,
	START,
	bar,
	figure,
	seconds,
	ticks,
	type Chart
} from './mac-figures';

describe('mac-figures', () => {
	it('prints a range once, a single figure alone, and whole seconds from 30', () => {
		expect(seconds({ lo: 6.4, hi: 6.8, n: 2 })).toBe('6.4–6.8 s');
		expect(seconds({ lo: 0.69, hi: 0.72, n: 3 })).toBe('0.7 s');
		expect(seconds({ lo: 11.9, hi: 12.0, n: 2 })).toBe('11.9–12.0 s');
		expect(seconds({ lo: 67.4, hi: 82.9, n: 3 })).toBe('67–83 s');
		expect(seconds({ lo: 43, hi: 43, n: 1 })).toBe('43 s');
		expect(figure({ lo: 7.63, hi: 7.63, n: 1 }, 'GB')).toBe('7.6 GB');
		expect(figure({ lo: 0, hi: 0, n: 4 }, 'GB')).toBe('0 GB');
	});

	it('draws a bar to lo solid and to hi light, clamped to the axis', () => {
		expect(bar({ lo: 5, hi: 10, n: 1 }, 20)).toEqual({ lo: 25, hi: 50 });
		expect(bar({ lo: 30, hi: 40, n: 1 }, 25)).toEqual({ lo: 100, hi: 100 });
	});

	it('every figure is measured (n >= 1, lo <= hi) and fits its axis', () => {
		const charts: Chart[] = [START, SNAPSHOT, REST.chart, RESTART];
		for (const c of charts) {
			expect(c.max % c.step).toBe(0);
			for (const r of c.rows) {
				for (const v of [r.own, r.nested]) {
					expect(v.n).toBeGreaterThanOrEqual(1);
					expect(v.lo).toBeLessThanOrEqual(v.hi);
					expect(v.hi, `${r.label} overruns its axis`).toBeLessThanOrEqual(c.max);
				}
			}
			expect(ticks(c).at(-1)).toBe(c.max);
		}
		for (const v of [REST.save, REST.wake]) expect(v.n).toBeGreaterThanOrEqual(1);
	});

	it('the claims in the titles hold for the figures', () => {
		// "Snapshot in under a second"
		expect(SNAPSHOT.rows[0].own.hi).toBeLessThan(1);
		// "Idle sandboxes give their memory back"
		expect(REST.chart.rows[0].own.hi).toBe(0);
		// "Start a sandbox in seconds": under a minute, and ahead of the nested layout
		for (const r of START.rows) {
			expect(r.own.hi).toBeLessThan(60);
			expect(r.own.hi).toBeLessThan(r.nested.lo);
		}
	});
});
