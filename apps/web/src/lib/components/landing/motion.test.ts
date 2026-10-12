// DECISIONS I-666: a picture rebuilds on a change of its own width, never
// of height alone (F20).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { watchWidth } from './motion.svelte';

type Cb = (entries: { contentRect: { width: number; height: number } }[]) => void;

let fire: Cb = () => {};
class FakeRO {
	constructor(cb: Cb) {
		fire = cb;
	}
	observe() {}
	disconnect() {}
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('watchWidth', () => {
	it('ignores the first size and height-only changes, and reports width changes', () => {
		vi.stubGlobal('ResizeObserver', FakeRO);
		const seen: [number, number][] = [];
		const stop = watchWidth(document.createElement('div'), (w, before) => seen.push([w, before]));
		const size = (width: number, height: number) => fire([{ contentRect: { width, height } }]);
		size(358, 900);
		size(358, 840);
		size(358.2, 784);
		expect(seen).toEqual([]);
		size(388, 784);
		expect(seen).toEqual([[388, 358]]);
		stop();
	});

	it('is a no-op without ResizeObserver', () => {
		vi.stubGlobal('ResizeObserver', undefined);
		const stop = watchWidth(document.createElement('div'), () => {});
		expect(stop).toBeTypeOf('function');
		stop();
	});
});

// DECISIONS I-676: grid pictures take turns, a loop each, and a picture
// that gains the turn from its still frame is told so.
describe('turns', () => {
	type IOCb = (entries: { isIntersecting: boolean; intersectionRatio: number }[]) => void;
	const seen = new Map<Element, IOCb>();
	class FakeIO {
		cb: IOCb;
		constructor(cb: IOCb) {
			this.cb = cb;
		}
		observe(el: Element) {
			seen.set(el, this.cb);
		}
		disconnect() {}
	}

	it('hands the turn to the next in-view grid picture at the seam', async () => {
		vi.stubGlobal('IntersectionObserver', FakeIO);
		vi.stubGlobal('requestAnimationFrame', (f: FrameRequestCallback) => setTimeout(() => f(0), 0));
		vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id));
		const { Picture } = await import('./motion.svelte');
		const row = document.createElement('div');
		const a = document.createElement('div');
		const b = document.createElement('div');
		row.append(a, b);
		document.body.append(row);
		// Same distance from the middle: a row of two cells.
		for (const el of [a, b]) el.getBoundingClientRect = () => ({ top: 0, height: 100 }) as DOMRect;
		const log: string[] = [];
		const pa = new Picture({ group: 'grid' });
		const pb = new Picture({ group: 'grid' });
		pa.start(a, (m, still) => log.push(`a ${m}${still ? ' still' : ''}`));
		pb.start(b, (m, still) => log.push(`b ${m}${still ? ' still' : ''}`));
		seen.get(a)!([{ isIntersecting: true, intersectionRatio: 1 }]);
		seen.get(b)!([{ isIntersecting: true, intersectionRatio: 1 }]);
		await new Promise((r) => setTimeout(r, 5));
		expect(log).toEqual(['a play still', 'b rest still']);

		// b reporting its seam changes nothing: it is not the leader.
		pb.looped();
		expect(log.length).toBe(2);

		pa.looped();
		expect(log.slice(2)).toEqual(['a rest', 'b play still']);
		pb.looped();
		expect(log.slice(4)).toEqual(['a play still', 'b rest']);

		pa.stop();
		pb.looped();
		expect(log.length).toBe(6);
		pb.stop();
		row.remove();
	});
});
