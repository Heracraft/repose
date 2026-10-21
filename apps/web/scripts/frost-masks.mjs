// Writes src/lib/hk/frost-masks.css: the masks a frozen machine's frost is
// painted through (lib/hk/frost.css, DECISIONS I-841 amendment 2026-10-21).
//
//   node scripts/frost-masks.mjs           writes the file
//   node scripts/frost-masks.mjs --check   fails if the file is stale
//
// Window frost grows in from an edge as feathers: a stem with side feathers
// at 60 degrees (ice is hexagonal), longest where two edges meet. Each mask
// is an SVG with a few fern sprites placed many times with <use>, so the
// file stays small, and a seeded random, so a rerun writes the same bytes.
//
// One mask serves both themes and both layers. The frost is drawn white and
// opaque, a cold halo around it black and translucent, and a crust of rime
// (feTurbulence, clumped) along the edge white. As an alpha mask it is the
// frost with its halo, painted in --hk-frost-shade; as a luminance mask it
// is the frost alone, painted in --hk-frost-ice. The colours stay tokens.

import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

function rng(seed) {
	let a = seed >>> 0;
	return () => {
		a = (a + 0x6d2b79f5) | 0;
		let t = Math.imul(a ^ (a >>> 15), 1 | a);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const n1 = (n) => {
	const v = Math.round(n * 10) / 10;
	return (Object.is(v, -0) ? 0 : v).toString().replace(/^(-?)0\./, '$1.');
};

function poly(pts) {
	const parts = [];
	for (let i = 1; i < pts.length; i++) {
		parts.push(`${n1(pts[i][0] - pts[i - 1][0])} ${n1(pts[i][1] - pts[i - 1][1])}`);
	}
	return `M${n1(pts[0][0])} ${n1(pts[0][1])}l${parts.join(' ').replace(/ -/g, '-')}`;
}

/** A feather pointing along +x from 0,0: a stem of length L with side
 *  feathers at +-60 degrees, longest near the base; long ones fork again. */
function fern(R, L, needle = false) {
	const main = [[0, 0]];
	const sides = [];
	let x = 0;
	let y = 0;
	let a = (R() - 0.5) * 0.2;
	let s = 0;
	let next = 2.6 * (0.6 + R());
	while (s < L) {
		a += (R() - 0.5) * 0.12;
		const st = Math.min(3, L - s + 0.01);
		x += Math.cos(a) * st;
		y += Math.sin(a) * st;
		s += st;
		main.push([x, y]);
		while (!needle && next <= s && s < L * 0.95) {
			const bl = Math.max(1.2, (L - next) * 0.42 * (0.55 + R() * 0.7));
			for (const side of [1, -1]) {
				if (R() < 0.18) continue;
				const ang = a + side * (Math.PI / 3) * (0.85 + R() * 0.3);
				const b = [[x, y]];
				let bx = x;
				let by = y;
				let ba = ang;
				let bs = 0;
				while (bs < bl) {
					ba += (R() - 0.5) * 0.15;
					const bst = Math.min(2.5, bl - bs + 0.01);
					bx += Math.cos(ba) * bst;
					by += Math.sin(ba) * bst;
					bs += bst;
					b.push([bx, by]);
				}
				sides.push(poly(b));
				if (L > 18 && bl > 7) {
					for (let t = 2.5; t < bl * 0.8; t += 2.2 + R() * 1.5) {
						const px = x + Math.cos(ang) * t;
						const py = y + Math.sin(ang) * t;
						const sl = (bl - t) * 0.35 * (0.6 + R() * 0.6);
						const sa = ang + (R() < 0.5 ? 1 : -1) * (Math.PI / 3);
						if (sl > 1) {
							sides.push(
								poly([
									[px, py],
									[px + Math.cos(sa) * sl, py + Math.sin(sa) * sl]
								])
							);
						}
					}
				}
			}
			next += 2.6 * (0.6 + R() * 0.8);
		}
	}
	return {
		d: `<path d='${poly(main)}'/><path d='${sides.join('')}' stroke-opacity='.7'/>`,
		tip: [x, y]
	};
}

/**
 * One mask. place(R) gives the feathers as {x, y, ang (degrees), L, o};
 * crust gives rects of rime, each fading away from its edge ('up' fades
 * upward from the rect's bottom, and so on).
 */
function mask({
	w,
	h,
	seed,
	sprites,
	place,
	crust = [],
	sparkles = 0,
	halo = 2.6,
	haloO = 0.45,
	needles = false
}) {
	const R = rng(seed);
	const ferns = sprites.map((L) => ({ L, ...fern(R, L, needles) }));
	const uses = [];
	const tips = [];
	for (const p of place(R)) {
		let bi = 0;
		for (let i = 1; i < ferns.length; i++) {
			if (Math.abs(ferns[i].L - p.L) < Math.abs(ferns[bi].L - p.L)) bi = i;
		}
		const sc = p.L / ferns[bi].L;
		const scale = Math.abs(sc - 1) > 0.04 ? `scale(${sc.toFixed(2)})` : '';
		const op = p.o < 0.97 ? ` opacity='${p.o.toFixed(2)}'` : '';
		uses.push(
			`<use href='#f${bi}' transform='translate(${n1(p.x)} ${n1(p.y)})rotate(${Math.round(p.ang)})${scale}'${op}/>`
		);
		const r = (p.ang * Math.PI) / 180;
		const [tx, ty] = ferns[bi].tip;
		tips.push([
			p.x + (tx * Math.cos(r) - ty * Math.sin(r)) * sc,
			p.y + (tx * Math.sin(r) + ty * Math.cos(r)) * sc,
			p.o
		]);
	}
	const dir = { up: [0, 1, 0, 0], left: [1, 0, 0, 0], down: [0, 0, 0, 1], right: [0, 0, 1, 0] };
	let defs = ferns.map((f, i) => `<g id='f${i}'>${f.d}</g>`).join('');
	defs += `<g id='a' fill='none' stroke-linecap='round'>${uses.join('')}</g>`;
	if (crust.length) {
		defs += `<filter id='n' x='0' y='0' width='100%' height='100%'><feTurbulence type='fractalNoise' baseFrequency='.24' numOctaves='3' seed='${seed % 97}'/><feColorMatrix values='0 0 0 0 1 0 0 0 0 1 0 0 0 0 1 0 0 0 9 -4.3'/><feComposite in2='SourceGraphic' operator='in'/></filter>`;
		crust.forEach((c, i) => {
			const [x1, y1, x2, y2] = dir[c.dir];
			if (c.along) {
				let stops = '';
				for (let k = 0; k <= 10; k++) {
					stops += `<stop offset='${k / 10}' stop-color='#fff' stop-opacity='${c.along((c.w * k) / 10 + c.x).toFixed(2)}'/>`;
				}
				defs += `<linearGradient id='e${i}'>${stops}</linearGradient><mask id='m${i}'><rect x='${n1(c.x)}' y='${n1(c.y)}' width='${n1(c.w)}' height='${n1(c.h)}' fill='url(#e${i})'/></mask>`;
			}
			defs += `<linearGradient id='c${i}' x1='${x1}' y1='${y1}' x2='${x2}' y2='${y2}'><stop offset='0' stop-color='#fff'/><stop offset='.35' stop-color='#fff' stop-opacity='.6'/><stop offset='1' stop-color='#fff' stop-opacity='0'/></linearGradient>`;
		});
	}
	let body = `<use href='#a' stroke='#000' stroke-width='${halo}' opacity='${haloO}'/>`;
	crust.forEach((c, i) => {
		const m = c.along ? ` mask='url(#m${i})'` : '';
		body += `<rect x='${n1(c.x)}' y='${n1(c.y)}' width='${n1(c.w)}' height='${n1(c.h)}' fill='url(#c${i})' filter='url(#n)'${m}/>`;
	});
	body += `<use href='#a' stroke='#fff' stroke-width='.85'/>`;
	if (sparkles) {
		const lit = tips.filter((t) => t[2] > 0.5);
		let sp = '';
		for (let i = 0; i < sparkles && lit.length; i++) {
			const t = lit[Math.floor(R() * lit.length)];
			sp += `<circle cx='${n1(t[0])}' cy='${n1(t[1])}' r='.75'/>`;
		}
		body += `<g fill='#fff'>${sp}</g>`;
	}
	const svg = `<svg xmlns='http://www.w3.org/2000/svg' width='${w}' height='${h}'><defs>${defs}</defs>${body}</svg>`;
	return dataUrl(svg);
}

/** Inside url("…") only % # < > need escaping; the SVG quotes with '. */
function dataUrl(svg) {
	const enc = svg
		.replace(/%/g, '%25')
		.replace(/#/g, '%23')
		.replace(/</g, '%3C')
		.replace(/>/g, '%3E');
	return `url("data:image/svg+xml,${enc}")`;
}

/** The project head: frost rising from the tab rule, the one edge drawn
 *  there, heaviest two thirds of the way along and thinning to nothing at
 *  both ends, so it never stops on a line the page does not draw. It stays
 *  low (h), under the head's actions. */
function head(seed) {
	const w = 440;
	const h = 56;
	const peak = 0.66;
	// 0 at both ends, 1 at the peak.
	const env = (x) => {
		const u = x / w;
		return u < peak ? Math.pow(u / peak, 1.8) : Math.pow((1 - u) / (1 - peak), 1.2);
	};
	return mask({
		w,
		h,
		seed,
		sprites: [4, 6, 9, 13, 18, 25, 34, 44],
		sparkles: 16,
		place: (R) => {
			const P = [];
			for (let x = 2; x < w - 2; x += 1.6 + R() * 2.8) {
				const f = env(x);
				if (R() > f + 0.03) continue;
				let L = 2.5 + 16 * f * Math.pow(R(), 2.2) + 20 * f * f * R();
				if (R() < 0.05 * f) L += 10 + 8 * R();
				P.push({
					x,
					y: h + 1,
					ang: 270 + (R() - 0.5) * 110,
					L: Math.min(L, h - 8),
					o: Math.min(1, 0.15 + f)
				});
			}
			return P;
		},
		crust: [{ x: 0, y: h - 4, w, h: 4, dir: 'up', along: env }]
	});
}

/** Frost seen through: a cloudy, streaked white in a fixed field, wider
 *  than any row, so it never tiles. frost.css paints it under a feathered
 *  mask along an edge (a /projects row's foot, the tab rule on a phone). */
function cloud(seed) {
	const w = 1600;
	const h = 64;
	const svg =
		`<svg xmlns='http://www.w3.org/2000/svg' width='${w}' height='${h}'>` +
		`<filter id='c' x='0' y='0' width='100%' height='100%'>` +
		// Streaks of cloudy frost, longer than they are tall.
		`<feTurbulence type='fractalNoise' baseFrequency='.014 .09' numOctaves='4' seed='${seed}'/>` +
		`<feColorMatrix values='0 0 0 0 1 0 0 0 0 1 0 0 0 0 1 0 0 0 2.4 -.9' result='s'/>` +
		// Grains of rime, where the streaks are.
		`<feTurbulence type='fractalNoise' baseFrequency='.7' numOctaves='2' seed='${seed + 1}'/>` +
		`<feColorMatrix values='0 0 0 0 1 0 0 0 0 1 0 0 0 0 1 0 0 0 7 -3.9'/>` +
		`<feComposite in2='s' operator='in' result='g'/>` +
		`<feMerge><feMergeNode in='s'/><feMergeNode in='g'/><feMergeNode in='g'/></feMerge>` +
		`</filter>` +
		`<rect width='100%' height='100%' filter='url(#c)'/></svg>`;
	return dataUrl(svg);
}

const css = `/* Generated by scripts/frost-masks.mjs; edit that and rerun it. The masks a
   frozen machine's frost is painted through (lib/hk/frost.css). */
:root {
	--hk-frost-head: ${head(7)};
	--hk-frost-cloud: ${cloud(9)};
}
`;

const out = fileURLToPath(new URL('../src/lib/hk/frost-masks.css', import.meta.url));
if (process.argv.includes('--check')) {
	// lib/hk/frost-masks.test.ts: the file is what this script writes.
	if (readFileSync(out, 'utf8') !== css) {
		console.error(`${out} is stale: run node scripts/frost-masks.mjs`);
		process.exit(1);
	}
} else {
	writeFileSync(out, css);
	console.log(`${out}: ${css.length} bytes`);
}
