import { describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

import MemoryMeter from './MemoryMeter.svelte';

function draw(props: { running: number; frozen?: number; limit: number }) {
	const target = document.createElement('div');
	const m = mount(MemoryMeter, { target, props });
	flushSync();
	const meter = target.querySelector('[role=meter]')!;
	const spans = [...target.querySelectorAll<HTMLElement>('.seg')].map((s) => ({
		kind: s.className.match(/seg--(\w+)/)![1],
		left: s.style.left,
		width: s.style.width
	}));
	const out = {
		value: target.querySelector('.hk-meter-value')!.textContent,
		split: [...target.querySelectorAll('[data-testid=memory-split] > span')].map(
			(s) => s.textContent
		),
		valueText: meter.getAttribute('aria-valuetext'),
		valueNow: meter.getAttribute('aria-valuenow'),
		spans,
		tick: !!target.querySelector('.tick'),
		neutral: target.querySelector('.hk-meter')!.classList.contains('hk-meter--neutral')
	};
	unmount(m);
	return out;
}

// Memory running is used against the plan: running, frozen, left, and
// over past the plan's tick (owner feedback 2026-10-21).
describe('MemoryMeter', () => {
	it('draws running, frozen and the part over the plan past a tick', () => {
		const r = draw({ running: 12, frozen: 24, limit: 32 });
		expect(r.value).toBe('36 GB of 32 GB · over');
		expect(r.split).toEqual(['12 GB running', '24 GB frozen', '4 GB over']);
		expect(r.valueText).toBe('36 GB of 32 GB, over: 12 GB running, 24 GB frozen, 4 GB over');
		expect(r.valueNow).toBe('32');
		expect(r.spans.map((s) => s.kind)).toEqual(['run', 'ice', 'over']);
		// Frozen fills the plan up to the tick; the bar holds all 36 GB.
		expect(r.spans[1].left).toBe(`${(12 / 36) * 100}%`);
		expect(r.spans[2].left).toBe(`${(32 / 36) * 100}%`);
		expect(r.spans[2].width).toBe(`${(4 / 36) * 100}%`);
		expect(r.tick).toBe(true);
	});

	it('leaves what is left as the empty track', () => {
		const r = draw({ running: 12, frozen: 16, limit: 32 });
		expect(r.value).toBe('28 GB of 32 GB');
		expect(r.split).toEqual(['12 GB running', '16 GB frozen', '4 GB left']);
		expect(r.spans.map((s) => s.kind)).toEqual(['run', 'ice']);
		expect(r.tick).toBe(false);
	});

	it('is a grey track with nothing running, and does not name frozen', () => {
		const r = draw({ running: 0, limit: 16 });
		expect(r.split).toEqual(['0 GB running', '16 GB left']);
		expect(r.spans).toEqual([]);
		expect(r.neutral).toBe(true);
	});

	it('counts all of it as over with no plan memory', () => {
		const r = draw({ running: 8, limit: 0 });
		expect(r.value).toBe('8 GB of 0 GB · over');
		expect(r.spans.map((s) => s.kind)).toEqual(['over']);
	});
});
