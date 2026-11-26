import { describe, expect, it } from 'vitest';
import { oomStopReason, platformStopReason } from './abuse';
import { planMemoryGB, sizeText, unsized } from './format';
import { planRoom } from './planRoom';

// A machine on a host of the account's own has no size (DECISIONS I-1630):
// the dashboard shows what it uses, and the plan does not meter it (I-1632).
describe('a machine on a host of your own', () => {
	const own = { class: 'large', sized: false, state: 'running', mem_used_bytes: 1.4 * 2 ** 30 };
	it('shows what it uses, not a size', () => {
		expect(unsized(own)).toBe(true);
		expect(sizeText(own)).toBe('1.4 GB used');
		expect(sizeText({ ...own, state: 'stopped', mem_used_bytes: undefined })).toBe('');
		expect(sizeText({ ...own, mem_used_bytes: undefined })).toBe('');
	});
	it('keeps the size on repose servers and from an older api', () => {
		expect(sizeText({ class: 'large', sized: true, state: 'running' })).toBe('large · 8 GB');
		expect(sizeText({ class: 'xl', state: 'stopped' })).toBe('xl · 16 GB');
		expect(unsized({})).toBe(false);
	});
	it('counts no plan memory', () => {
		expect(planMemoryGB(own)).toBe(0);
		expect(planMemoryGB({ class: 'large' })).toBe(8);
		const r = planRoom('active', { usage: { memory_gb: 8 } } as never, [
			{ id: 'a', name: 'a', slug: 'a', class: 'xl', state: 'running', sized: false },
			{ id: 'b', name: 'b', slug: 'b', class: 'small', state: 'running' }
		]);
		expect(r).toMatchObject({ limitGB: 8, usedGB: 4 });
		expect(r!.holders.map((h) => h.name)).toEqual(['b']);
	});
});

describe('oomStopReason', () => {
	const le =
		'oom_stopped: stopped: your host mac-mini ran out of memory, so repose stopped api, the machine started last, to keep the others running. `repose start api` starts it again';
	it('is the sentence without the command, which the Start button replaces', () => {
		const want =
			'stopped: your host mac-mini ran out of memory, so repose stopped api, the machine started last, to keep the others running.';
		expect(oomStopReason({ state: 'stopped', last_error: le })).toBe(want);
		expect(platformStopReason({ state: 'stopped', last_error: le })).toBe(want);
	});
	it('is empty for a machine that is not stopped, or stopped for another reason', () => {
		expect(oomStopReason({ state: 'running', last_error: le })).toBe('');
		expect(oomStopReason({ state: 'stopped', last_error: 'build_failed: x' })).toBe('');
		expect(oomStopReason({ state: 'stopped', last_error: null })).toBe('');
	});
});
