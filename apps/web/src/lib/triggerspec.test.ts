import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { buildSpec, parseSpec, everyText, SHAPE_LABELS, WEEKDAYS } from './triggerspec';

// The grammar is one grammar (I-1252). `internal/triggerspec` is what
// the CLI types against and the api fires on; this holds the browser's
// form builder to it, so a schedule made on the dashboard is a schedule
// the api stores unchanged. A spec the api rewrites would open in the
// wrong fields the next time somebody pressed Edit.
describe('the schedule a form builds', () => {
	it('writes the canonical text of each of the six shapes', () => {
		expect(buildSpec({ shape: 'every', minutes: 15, at: '', days: [], day: 1 })).toEqual({
			spec: 'every 15m'
		});
		expect(buildSpec({ shape: 'every', minutes: 120, at: '', days: [], day: 1 })).toEqual({
			spec: 'every 2h'
		});
		expect(buildSpec({ shape: 'every', minutes: 90, at: '', days: [], day: 1 })).toEqual({
			spec: 'every 1h30m'
		});
		expect(buildSpec({ shape: 'daily', minutes: 0, at: '03:00', days: [], day: 1 })).toEqual({
			spec: 'daily 03:00'
		});
		expect(buildSpec({ shape: 'weekdays', minutes: 0, at: '07:00', days: [], day: 1 })).toEqual({
			spec: 'weekdays 07:00'
		});
		expect(buildSpec({ shape: 'weekends', minutes: 0, at: '10:00', days: [], day: 1 })).toEqual({
			spec: 'weekends 10:00'
		});
		expect(
			buildSpec({ shape: 'days', minutes: 0, at: '18:30', days: ['thu', 'mon'], day: 1 })
		).toEqual({ spec: 'mon,thu 18:30' });
		expect(buildSpec({ shape: 'monthly', minutes: 0, at: '09:00', days: [], day: 1 })).toEqual({
			spec: 'monthly 1 09:00'
		});
	});

	// Monday first, as the Go parser sorts them: `sat,sun`, never
	// `sun,sat`, so the text the api stores is the text the form made.
	it('puts the days Monday first whatever order they were picked in', () => {
		expect(
			buildSpec({ shape: 'days', minutes: 0, at: '08:00', days: ['sun', 'sat'], day: 1 })
		).toEqual({ spec: 'sat,sun 08:00' });
	});

	it('refuses what the api would refuse, before the round trip', () => {
		expect(buildSpec({ shape: 'every', minutes: 5, at: '', days: [], day: 1 })).toEqual({
			error: 'An interval is 15 minutes to 24 hours, in whole minutes.'
		});
		expect(buildSpec({ shape: 'every', minutes: 1441, at: '', days: [], day: 1 })).toHaveProperty(
			'error'
		);
		expect(buildSpec({ shape: 'daily', minutes: 0, at: '25:00', days: [], day: 1 })).toEqual({
			error: 'A time of day is written 07:00.'
		});
		expect(buildSpec({ shape: 'monthly', minutes: 0, at: '09:00', days: [], day: 31 })).toEqual({
			error: 'A day of the month is 1 to 28, so that every month has it.'
		});
		expect(buildSpec({ shape: 'days', minutes: 0, at: '09:00', days: [], day: 1 })).toEqual({
			error: 'Pick at least one day.'
		});
	});

	it('reads back every spec it writes', () => {
		for (const f of [
			{ shape: 'every' as const, minutes: 15, at: '09:00', days: ['mon'], day: 1 },
			{ shape: 'every' as const, minutes: 90, at: '09:00', days: ['mon'], day: 1 },
			{ shape: 'daily' as const, minutes: 60, at: '03:00', days: ['mon'], day: 1 },
			{ shape: 'weekdays' as const, minutes: 60, at: '07:00', days: ['mon'], day: 1 },
			{ shape: 'weekends' as const, minutes: 60, at: '10:00', days: ['mon'], day: 1 },
			{ shape: 'days' as const, minutes: 60, at: '18:30', days: ['mon', 'thu'], day: 1 },
			{ shape: 'monthly' as const, minutes: 60, at: '09:00', days: ['mon'], day: 28 }
		]) {
			const built = buildSpec(f);
			expect(built).toHaveProperty('spec');
			const back = parseSpec((built as { spec: string }).spec);
			expect(back).toBeDefined();
			expect(buildSpec(back!)).toEqual(built);
		}
	});

	it('answers undefined for a spec it cannot open', () => {
		expect(parseSpec('fortnightly 09:00')).toBeUndefined();
		expect(parseSpec('monthly 31 09:00')).toBeUndefined();
		expect(parseSpec('every')).toBeUndefined();
	});

	it('writes an interval the way the Go copy does', () => {
		expect(everyText(45)).toBe('45m');
		expect(everyText(60)).toBe('1h');
		expect(everyText(1440)).toBe('24h');
	});
});

// The six shapes are the Go package's six kinds. A seventh kind added
// there with no field here would leave the dashboard unable to make it.
describe('the shapes', () => {
	const go = readFileSync(resolve('../../internal/triggerspec/triggerspec.go'), 'utf8');
	const kinds = [...go.matchAll(/Kind(\w+)\s+= "([^"]+)"/g)].map((m) => m[2]);

	it('are the Go package kinds', () => {
		expect(kinds.length).toBe(6);
		expect(Object.keys(SHAPE_LABELS).sort()).toEqual([...kinds].sort());
	});

	it('name the seven days', () => {
		expect(WEEKDAYS).toHaveLength(7);
	});
});
