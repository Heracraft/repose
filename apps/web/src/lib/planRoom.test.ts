import { describe, expect, it } from 'vitest';
import { defaultClass, fits, freeGB, planRoom } from './planRoom';

const usage = (memory_gb: number) => ({ usage: { memory_gb } }) as never;
const p = (name: string, cls: string, state = 'running') =>
	({ id: name, name, slug: name, class: cls, state }) as never;

describe('planRoom', () => {
	it('is the plan and what holds it, for a metered plan only', () => {
		const r = planRoom('active', usage(8), [p('a', 'large'), p('b', 'small', 'stopped')]);
		expect(r).toMatchObject({ limitGB: 8, usedGB: 8 });
		expect(r!.holders.map((h) => h.name)).toEqual(['a']);
		for (const mode of ['off', 'exempt', 'none', 'unknown', 'past_due'] as const) {
			expect(planRoom(mode, usage(8), [])).toBeUndefined();
		}
		// Own hardware sells no fleet memory: nothing to meter.
		expect(planRoom('active', usage(0), [])).toBeUndefined();
	});
});

describe('defaultClass', () => {
	it("is the api's default when it fits, and never larger", () => {
		expect(defaultClass(undefined)).toBe('large');
		expect(defaultClass(planRoom('active', usage(32), []))).toBe('large');
	});
	it('steps down to what the plan has room for', () => {
		const room = planRoom('active', usage(8), [p('a', 'small')]);
		expect(freeGB(room)).toBe(4);
		expect(defaultClass(room)).toBe('small');
		expect(defaultClass(planRoom('trial', usage(8), [p('a', 'small'), p('b', 'tiny')]))).toBe(
			'tiny'
		);
	});
	it('stays on the default when nothing fits, for the form to say so', () => {
		const room = planRoom('active', usage(8), [p('a', 'large')]);
		expect(defaultClass(room)).toBe('large');
		expect(fits(room, 'tiny')).toBe(false);
	});
});
