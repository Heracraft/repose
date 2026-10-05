import { describe, it, expect } from 'vitest';
import { segments, paths, peak, latest, nearest, pct, cpuTime } from './usage';

const m = 60_000;

describe('segments', () => {
	it('splits at a null and at a stop', () => {
		const pts = [
			{ t: 0, v: 0.1 },
			{ t: m, v: 0.2 },
			{ t: 2 * m, v: null },
			{ t: 3 * m, v: 0.3 },
			{ t: 4 * m, v: 0.4 },
			// The machine was stopped for ten minutes.
			{ t: 14 * m, v: 0.5 }
		];
		expect(segments(pts, 60)).toEqual([
			[
				{ t: 0, v: 0.1 },
				{ t: m, v: 0.2 }
			],
			[
				{ t: 3 * m, v: 0.3 },
				{ t: 4 * m, v: 0.4 }
			],
			[{ t: 14 * m, v: 0.5 }]
		]);
	});
	it('keeps a late tick inside one and a half steps', () => {
		expect(
			segments(
				[
					{ t: 0, v: 1 },
					{ t: 85_000, v: 1 }
				],
				60
			)
		).toHaveLength(1);
	});
	it('gives nothing for nothing', () => {
		expect(segments([], 60)).toEqual([]);
		expect(segments([{ t: 0, v: null }], 60)).toEqual([]);
	});
});

describe('paths', () => {
	it('draws the line and closes the area at the baseline', () => {
		const { line, area } = paths(
			[
				{ t: 0, v: 0 },
				{ t: 10, v: 1 }
			],
			(t) => t * 10,
			(v) => 100 - v * 100,
			100
		);
		expect(line).toBe('M0.0,100.0L100.0,0.0');
		expect(area).toBe('M0.0,100.0L100.0,0.0L100.0,100L0.0,100Z');
	});
});

describe('readings', () => {
	const pts = [
		{ t: 0, v: 0.2 },
		{ t: m, v: 0.9 },
		{ t: 2 * m, v: 0.4 },
		{ t: 3 * m, v: null }
	];
	it('peak and latest skip gaps', () => {
		expect(peak(pts)).toBe(0.9);
		expect(latest(pts)).toBe(0.4);
		expect(peak([])).toBeUndefined();
		expect(latest([{ t: 0, v: null }])).toBeUndefined();
	});
	it('nearest finds the closest time', () => {
		expect(nearest(pts, 70_000)).toBe(1);
		expect(nearest([], 0)).toBe(-1);
	});
	it('pct and cpuTime', () => {
		expect(pct(0)).toBe('0%');
		expect(pct(0.001)).toBe('<1%');
		expect(pct(0.625)).toBe('63%');
		expect(pct(1)).toBe('100%');
		expect(cpuTime(38.2)).toBe('38 s');
		expect(cpuTime(2700)).toBe('45 min');
		expect(cpuTime(7560)).toBe('2.1 h');
	});
});
