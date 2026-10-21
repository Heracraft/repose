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
			defs += `<linearGradient id='c${i}' x1='${x1}' y1='${y1}' x2='${x2}' y2='${y2}'><stop offset='0' stop-color='#fff'/><stop offset='.35' stop-color='#fff' stop-opacity='.6'/><stop offset='1' stop-color='#fff' stop-opacity='0'/></linearGradient>`;
		});
	}
	let body = `<use href='#a' stroke='#000' stroke-width='${halo}' opacity='${haloO}'/>`;
	crust.forEach((c, i) => {
		body += `<rect x='${n1(c.x)}' y='${n1(c.y)}' width='${n1(c.w)}' height='${n1(c.h)}' fill='url(#c${i})' filter='url(#n)'/>`;
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
	// Inside url("…") only % # < > need escaping; the SVG quotes with '.
	const enc = svg
		.replace(/%/g, '%25')
		.replace(/#/g, '%23')
		.replace(/</g, '%3C')
		.replace(/>/g, '%3E');
	return `url("data:image/svg+xml,${enc}")`;
}

/** The project head's far corner: frost up the right edge and along the
 *  tab rule, heaviest where they meet, fading leftward. */
function head(seed) {
	const w = 380;
	const h = 170;
	return mask({
		w,
		h,
		seed,
		sprites: [5, 8, 12, 17, 24, 33, 45, 60],
		sparkles: 22,
		place: (R) => {
			const P = [];
			for (let y = h * 0.1; y < h; y += 2 + R() * 3.5) {
				const up = Math.pow((y - h * 0.1) / (h * 0.9), 1.5);
				const k = Math.exp(-(h - y) / 45);
				let L = (4 + 22 * Math.pow(R(), 2.4)) * (0.4 + 0.6 * up) + 40 * k * R();
				if (R() < 0.06 * up) L += 22 + 18 * R();
				P.push({
					x: w + 1,
					y,
					ang: 180 + (R() - 0.5) * 120,
					L,
					o: (0.6 + 0.4 * R()) * (0.2 + 0.8 * up)
				});
			}
			for (let x = w; x > 0; x -= 2 + R() * 3.5) {
				const f = Math.pow(x / w, 2);
				if (R() > f + 0.04) continue;
				const k = Math.exp(-(w - x) / 55);
				let L = 3 + 16 * f * Math.pow(R(), 2.4) + 36 * k * R();
				if (R() < 0.05 * f) L += 18;
				P.push({ x, y: h + 1, ang: 270 + (R() - 0.5) * 120, L, o: Math.min(1, 0.3 + f) });
			}
			for (let i = 0; i < 9; i++) {
				P.push({
					x: w + 1,
					y: h + 1,
					ang: 225 + (R() - 0.5) * 80,
					L: 30 + 45 * R(),
					o: 0.7 + 0.3 * R()
				});
			}
			return P;
		},
		crust: [
			{ x: w - 6, y: h * 0.45, w: 6, h: h * 0.55, dir: 'left' },
			{ x: w * 0.35, y: h - 4, w: w * 0.65, h: 4, dir: 'up' }
		]
	});
}

/** The head on a phone: the tabs reach the column's edge, so the frost
 *  keeps to the gutter and the last few px of the column. */
function headNarrow(seed) {
	const w = 48;
	const h = 150;
	return mask({
		w,
		h,
		seed,
		sprites: [4, 6, 9, 13, 18, 24, 32],
		sparkles: 8,
		place: (R) => {
			const P = [];
			for (let y = 0; y < h; y += 2 + R() * 3) {
				const up = y / h;
				const k = Math.exp(-(h - y) / 28);
				const L = (3 + 10 * Math.pow(R(), 2)) * up * up + 18 * k * R();
				P.push({ x: w + 1, y, ang: 180 + (R() - 0.5) * 110, L, o: (0.6 + 0.4 * R()) * up });
			}
			for (let x = w; x > 0; x -= 2 + R() * 3) {
				const f = x / w;
				P.push({ x, y: h + 1, ang: 270 + (R() - 0.5) * 110, L: 2 + 6 * f * R(), o: f });
			}
			for (let i = 0; i < 7; i++) {
				P.push({
					x: w + 1,
					y: h + 1,
					ang: 215 + (R() - 0.5) * 40,
					L: 18 + 16 * R(),
					o: 0.8 + 0.2 * R()
				});
			}
			return P;
		},
		crust: [{ x: w - 5, y: h * 0.4, w: 5, h: h * 0.6, dir: 'left' }]
	});
}

/** A frozen row's rule, or the memory bar: needles of rime, thickest at
 *  the leading end and gone by reach. */
function row(seed, reach = 200) {
	const w = 300;
	const h = 10;
	return mask({
		w,
		h,
		seed,
		sprites: [2, 3, 4.5, 6],
		needles: true,
		sparkles: 5,
		halo: 2,
		haloO: 0.4,
		place: (R) => {
			const P = [];
			for (let x = 0.5; x < reach; x += 2 + R() * 4) {
				const f = Math.pow(1 - x / reach, 1.4);
				if (R() > f + 0.05) continue;
				const L = 1.5 + 5 * f * Math.pow(R(), 1.3);
				P.push({ x, y: h, ang: 270 + (R() - 0.5) * 120, L, o: Math.min(1, 0.25 + f) });
			}
			return P;
		},
		crust: [{ x: 0, y: h - 2, w: 150, h: 2, dir: 'up' }]
	});
}

const css = `/* Generated by scripts/frost-masks.mjs; edit that and rerun it. The masks a
   frozen machine's frost is painted through (lib/hk/frost.css). */
:root {
	--hk-frost-head: ${head(7)};
	--hk-frost-head-narrow: ${headNarrow(5)};
	--hk-frost-meter: ${row(3, 300)};
	--hk-frost-row-1: ${row(11)};
	--hk-frost-row-2: ${row(12)};
	--hk-frost-row-3: ${row(14)};
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
