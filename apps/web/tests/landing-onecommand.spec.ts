// DECISIONS I-668, I-675: "Your laptop's setup, in one command". Through
// one whole loop, sampled every 40ms on a paused clock, a travelling copy
// is never seen over a chip, a wire or its own source row, nor outside the
// panel it lands in; at most two are in
// flight; each lands on its slot's exact box; and the agent's commit comes
// back to the laptop under a `git fetch repose` chip. Side by side the two
// chips stand in the middle of the gap, halfway down the panels, and a
// copy in flight rides on the accent's tint with an accent edge (web-polish
// keeps both from its own picture; see the decision entry).
import { test, expect, type Page } from '@playwright/test';
import { holdClock } from './helpers';

type Box = { x0: number; y0: number; x1: number; y1: number };
type Sample = {
	over: string[];
	flying: number;
	off: string[];
	back: boolean;
};

// Runs in the page: what the copies in flight cover right now.
function sample(): Sample {
	const pic = document.querySelector<HTMLElement>('.oc .pic')!;
	const b = pic.getBoundingClientRect();
	const rel = (r: DOMRect): Box => ({
		x0: r.left - b.left,
		y0: r.top - b.top,
		x1: r.right - b.left,
		y1: r.bottom - b.top
	});
	const cut = (a: Box, c: Box): Box | null => {
		const r = {
			x0: Math.max(a.x0, c.x0),
			y0: Math.max(a.y0, c.y0),
			x1: Math.min(a.x1, c.x1),
			y1: Math.min(a.y1, c.y1)
		};
		return r.x1 - r.x0 > 0.5 && r.y1 - r.y0 > 0.5 ? r : null;
	};
	const fly = pic.querySelector<HTMLElement>('.fly')!;
	// The clip the script set: path('M x y H x V y H x Z ...'), boxes only.
	const clip = [
		...fly.style.clipPath.matchAll(
			/M\s*(-?[\d.]+)[\s,]+(-?[\d.]+)\s*H\s*(-?[\d.]+)\s*V\s*(-?[\d.]+)\s*H/g
		)
	].map((m) => {
		const [x0, y0, x1, y1] = m.slice(1).map(Number);
		return {
			x0: Math.min(x0, x1),
			y0: Math.min(y0, y1),
			x1: Math.max(x0, x1),
			y1: Math.max(y0, y1)
		};
	});
	const [lap, mac] = pic.querySelectorAll<HTMLElement>('.side');
	const legs = [...pic.querySelectorAll<HTMLElement>('.hop .leg')].filter(
		(l) => Number(getComputedStyle(l).opacity) > 0.01
	);
	const out: Sample = { over: [], flying: 0, off: [], back: false };
	for (const c of fly.querySelectorAll<HTMLElement>('.clone')) {
		const o = Number(getComputedStyle(c).opacity);
		if (o < 0.01) continue;
		const key = c.dataset.copy!;
		const r = rel(c.getBoundingClientRect());
		if (!clip.length) out.over.push(`${key} is not clipped`);
		const seen = clip.map((k) => cut(r, k)).filter((x): x is Box => !!x);
		const src =
			key === 'agent'
				? mac.querySelector<HTMLElement>('.slot')!
				: lap.querySelector<HTMLElement>(`[data-from="${key}"]`)!;
		const dst =
			key === 'agent'
				? lap.querySelector<HTMLElement>('.slot')!
				: mac.querySelector<HTMLElement>(`[data-to="${key}"]`)!;
		const s = rel(src.getBoundingClientRect());
		const d = rel(dst.getBoundingClientRect());
		const win = rel(
			(key === 'agent' ? lap : mac).querySelector<HTMLElement>('.win')!.getBoundingClientRect()
		);
		for (const v of seen) {
			// inside the walls of the panel it lands in, never in the gap
			if (v.x0 < win.x0 + 0.5 || v.x1 > win.x1 - 0.5 || v.y0 < win.y0 || v.y1 > win.y1)
				out.over.push(`${key} outside its panel`);
			for (const l of legs)
				if (cut(v, rel(l.getBoundingClientRect()))) out.over.push(`${key} over a chip`);
			if (cut(v, s)) out.over.push(`${key} over its source`);
		}
		const landed = Math.abs(r.x0 - d.x0) < 0.5 && Math.abs(r.y0 - d.y0) < 0.5;
		if (!landed) out.flying++;
		// Showing (16ms) where it starts, over its source row's box.
		const showing =
			Math.abs(r.x0 - parseFloat(c.style.left)) < 0.5 &&
			Math.abs(r.y0 - parseFloat(c.style.top)) < 0.5;
		// Fading off where it can be seen: it has set down, on its slot
		// exactly.
		if (o < 0.99 && !landed && !showing && seen.length)
			out.off.push(
				`${key} fades ${Math.round(r.x0 - d.x0)},${Math.round(r.y0 - d.y0)} off its slot`
			);
		if (key === 'agent' && seen.length) out.back = true;
	}
	return out;
}

async function loop(page: Page, width: number) {
	await page.setViewportSize({ width, height: width < 600 ? 844 : 900 });
	// The clock is held from the first script; bring the machine's panel
	// into view: the loop starts there and moves only as the test runs it.
	await holdClock(page);
	await page.goto('/');
	await page.locator('.oc .side').nth(1).scrollIntoViewIfNeeded();
	await expect(page.locator('.oc')).toHaveAttribute('data-motion', 'play');
	await page.mouse.move(0, 0);

	const over = new Set<string>();
	const off = new Set<string>();
	let most = 0;
	let seen = 0;
	let back = false;
	for (let t = 0; t < 11000; t += 40) {
		await page.clock.runFor(40);
		const s = await page.evaluate(sample);
		s.over.forEach((x) => over.add(x));
		s.off.forEach((x) => off.add(x));
		most = Math.max(most, s.flying);
		if (s.flying) seen++;
		back ||= s.back;
	}
	expect(seen, 'copies were seen in flight').toBeGreaterThan(20);
	expect([...over]).toEqual([]);
	expect([...off]).toEqual([]);
	expect(most).toBeLessThanOrEqual(2);
	expect(back, "the agent's commit travelled back").toBe(true);
	// The end state: the laptop holds the fetched commit under its chip.
	await expect(page.locator('.oc .side').first().locator('.slot .rref')).toHaveText(
		'repose/fetch-timeout'
	);
	await expect(page.locator('.oc .fetch .chip')).toHaveText('git fetch repose');
}

for (const width of [1440, 390]) {
	test(`OneCommand's copies cover no chip and no source, at ${width}`, async ({ page }) => {
		test.setTimeout(120_000);
		await loop(page, width);
	});
}

test('OneCommand still frame holds the fetched commit on both sides', async ({ page }) => {
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await page.goto('/');
	const oc = page.locator('.oc');
	await oc.scrollIntoViewIfNeeded();
	for (const side of [0, 1])
		await expect(oc.locator('.side').nth(side).locator('.slot .hash')).toHaveText('e41b9c2');
	await expect(oc.locator('.slot')).toHaveCount(2);
	for (const el of await oc.locator('.slot, .rref, .fetch, .ready').all())
		await expect(el).toHaveCSS('opacity', '1');
	await expect(oc.locator('.ready')).toHaveText(/^Ready in \d+s$/);
});

test('side by side, the chips stand one over the other halfway down the gap', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await page.goto('/');
	const oc = page.locator('.oc');
	await oc.scrollIntoViewIfNeeded();
	const [lap, hop, run, fetch] = await Promise.all(
		[
			oc.locator('.side').first().locator('.win'),
			oc.locator('.hop'),
			oc.locator('.leg.run'),
			oc.locator('.leg.fetch')
		].map(async (l) => (await l.boundingBox())!)
	);
	const mid = (b: { y: number; height: number }) => b.y + b.height / 2;
	expect(Math.abs(mid(hop) - mid(lap))).toBeLessThan(8);
	expect(run.y + run.height).toBeLessThanOrEqual(fetch.y);
	expect(run.y).toBeGreaterThan(lap.y + lap.height / 4);
});

test('a copy in flight rides on the accent tint with an accent edge', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await holdClock(page);
	await page.goto('/');
	await page.locator('.oc .side').nth(1).scrollIntoViewIfNeeded();
	await expect(page.locator('.oc')).toHaveAttribute('data-motion', 'play');
	await page.mouse.move(0, 0);
	await page.clock.runFor(1200);
	const clone = page.locator('.oc .fly .clone').first();
	const [bg, shadow, surface] = await clone.evaluate((c) => {
		const win = document.querySelector('.oc .win')!;
		const s = getComputedStyle(c);
		return [s.backgroundColor, s.boxShadow, getComputedStyle(win).backgroundColor];
	});
	expect(bg).not.toBe(surface);
	expect(bg).not.toBe('rgba(0, 0, 0, 0)');
	expect(shadow).toContain('inset');
	expect(shadow).toMatch(/ 2px 0px 0px /);
});
