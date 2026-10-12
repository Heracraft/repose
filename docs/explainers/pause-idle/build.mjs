import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as k from "../kit/iso-kit.mjs";
import { kitScript } from "../kit/inline-kit.mjs";
import * as M from "./model.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const theme = process.argv.includes("--light") ? "light" : "dark";

const WIDTH = 600;
const HEIGHT = 312;

// World: x along the rail, y across, z up. One GiB of host RAM is U units of rail.
const U = 1.9;
const HEAD_X = -228;
const BUDGET_X = HEAD_X + M.BUDGET_GIB * U; // 212.8: where 232 GiB of guests end
const FULL_X = HEAD_X + M.SEATS * M.SEAT_GIB * U; // 228: all 240 GiB, reserve included
const END_X = 238;

const BASE = { x: -262, y: -38, w: 524, d: 76, r: 6 };
const BASE_H = 16;
const FOOT_H = 6;
const RAILS = [-18, 13];
const RAIL_H = 4;
const PLATE_Z = RAIL_H;
const PLATE_H = 64;
const BODY_Z = 9;
const BODY_H = 54;
const BODY_Y = 17;
const FOLD = 2.4;

const HEAD = { x: -250, y: -28, w: 22, d: 56, r: 2 };
const HEAD_H = 82;
const END = { x: END_X, y: -28, w: 12, d: 56, r: 2 };
const END_H = 58;
const SPRING_Z = 36;

const STRIP_Y = 24;
const GLASS_X0 = -204;
const GLASS_PER_GIB = 1.7;
const GLASS_MAX_GIB = 200;
const GLASS_V = [5.5, 11.5];

const P = k.fitProjection(
  [...k.boxCorners(BASE, -BASE_H - FOOT_H, 0), [HEAD.x, HEAD.y, HEAD_H + 1], [HEAD.x, HEAD.y + HEAD.d, HEAD_H + 1], [END.x + END.w, 0, END_H + 8]],
  WIDTH,
  HEIGHT,
  { pad: 26, azimuth: 60, elevation: 35 },
);

const at = (point) => k.iso(point, P);
const S = (paths, style) => k.solidSvg(paths, style);
const L = (d, style) => k.lineSvg(d, style);
const D = (points, style) => k.dotsSvg(points, style);
const byDepth = (points) => [...points].sort((a, b) => k.depthOf([a[0], a[1], 0], P) - k.depthOf([b[0], b[1], 0], P));

function base() {
  const out = [`<path class="iso-halo" d="${k.haloOf(BASE, -BASE_H - FOOT_H, BASE_H, P)}"/>`];
  const feet = [];
  for (const x of [-232, 0, 232]) for (const y of [-26, 26]) feet.push([x, y]);
  for (const [x, y] of byDepth(feet)) {
    out.push(S(k.cylinder(x, y, 8, -BASE_H - FOOT_H, 1.8, P, 28), { tone: "lo" }));
    out.push(S(k.cylinder(x, y, 6, -BASE_H - FOOT_H + 1.8, FOOT_H - 1.8, P, 28), { tone: "mid" }));
  }
  out.push(S(k.slabOf(BASE, -BASE_H, BASE_H, P, 8, 1.6), { tone: "mid", inner: L(k.sideSeam(BASE, -BASE_H + 4, P, 8), { tone: "faint" }) }));
  out.push(L(k.planOutline(k.insetPlan(BASE, 5), 0, P), { tone: "lo" }));
  const screws = k.corners(BASE, 8);
  out.push(L(screws.map(([x, y]) => k.ring(x, y, 2.2, 0, P, 16)).join(""), { tone: "lo" }));
  out.push(D(screws.map(([x, y]) => at([x, y, 0])), { size: 0.5 }));
  out.push(sightGlass());
  return out.join("");
}

// The swap gauge: a long window on the base's front face, 0 to 200 GiB.
function sightGlass() {
  const face = BASE.y + BASE.d;
  const out = [];
  const ticks = k.sideTicks(GLASS_X0, GLASS_X0 + GLASS_MAX_GIB * GLASS_PER_GIB, 5 * GLASS_PER_GIB, 4, face, 0, [2, 3.6], P);
  out.push(L(ticks.minor, { tone: "lo" }), L(ticks.major, { tone: "mid" }));
  const m = k.sideMatrix([GLASS_X0, face], 0, P, "left");
  const len = GLASS_MAX_GIB * GLASS_PER_GIB;
  const [v0, v1] = GLASS_V;
  const rect = (u0, u1, a, b, r) => `M${u0 + r} ${a}H${u1 - r}Q${u1} ${a} ${u1} ${a + r}V${b - r}Q${u1} ${b} ${u1 - r} ${b}H${u0 + r}Q${u0} ${b} ${u0} ${b - r}V${a + r}Q${u0} ${a} ${u0 + r} ${a}Z`;
  const part = M.SWAP_PARTITION_GIB * GLASS_PER_GIB;
  out.push(`<g transform="${m}">`);
  out.push(`<path d="${rect(-1.2, len + 1.2, v0 - 1.2, v1 + 1.2, 2)}" style="fill:var(--anatomy-shade-3);stroke:var(--anatomy-lo);stroke-width:var(--anatomy-weight);vector-effect:non-scaling-stroke"/>`);
  out.push(`<path d="${rect(0, len, v0, v1, 1.2)}" style="fill:var(--anatomy-shade-0)"/>`);
  out.push(`<clipPath id="swap-window"><path d="${rect(0, len, v0, v1, 1.2)}"/></clipPath>`);
  out.push(`<path data-part="swap" class="swap-fill" clip-path="url(#swap-window)" d="M0 ${v0 + 0.8}H0.01V${v1 - 0.8}H0Z"/>`);
  out.push(`<path d="M${part} ${v0}V${v1}" class="iso-line" data-tone="hi" style="stroke-width:1"/>`);
  out.push(`<path d="${rect(0, len, v0, v1, 1.2)}" style="fill:none;stroke:var(--anatomy-lo);stroke-width:var(--anatomy-weight);vector-effect:non-scaling-stroke"/>`);
  out.push(`</g>`);
  out.push(k.faceTextSvg(k.sideMatrix([GLASS_X0 + len + 11, face], -9.9, P, "left"), "SWAP GiB", { size: 3.6, tone: "mid" }));
  // two screws holding the glass bezel
  out.push(D([at([GLASS_X0 - 5, face, -(v0 + v1) / 2]), at([GLASS_X0 + len + 5, face, -(v0 + v1) / 2])], { size: 0.5 }));
  return out.join("");
}

function railsAndStrips() {
  const out = [];
  // seat ruler: one major tick per 8 GiB seat, minor every 2 GiB
  const strip = { x: HEAD_X, y: STRIP_Y, w: BUDGET_X - HEAD_X, d: 7, r: 1 };
  out.push(S(k.slabOf(strip, 0, 0.6, P, 3), { tone: "lo", crease: "none" }));
  const t = k.topTicks(HEAD_X, BUDGET_X, 2 * U, 4, STRIP_Y, 0.6, [2.2, 4], P);
  out.push(L(t.minor, { tone: "lo" }), L(t.major, { tone: "mid" }));
  // the wake reserve: one seat held back, on a raised strip of its own
  const reserve = { x: BUDGET_X + 0.8, y: STRIP_Y, w: FULL_X - BUDGET_X - 0.8, d: 7, r: 1 };
  out.push(S(k.slabOf(reserve, 0, 1.4, P, 3, 0.3), { tone: "mid" }));
  const rt = k.topTicks(BUDGET_X + 1, FULL_X - 1, 2 * U, 4, STRIP_Y, 1.4, [2.2, 4], P);
  out.push(L(rt.minor, { tone: "mid" }));
  out.push(D([at([BUDGET_X + 3, STRIP_Y + 5.4, 1.4]), at([FULL_X - 2.2, STRIP_Y + 5.4, 1.4])], { size: 0.42 }));
  for (const y of RAILS) {
    out.push(S(k.slabOf({ x: HEAD_X - 1, y, w: END_X - HEAD_X + 1, d: 5, r: 1 }, 0, RAIL_H, P, 3, 0.4), { tone: "mid" }));
  }
  // manifold slot between the rails: where a bellows' air goes down to swap
  out.push(L(k.planOutline({ x: HEAD_X + 2, y: -6, w: BUDGET_X - HEAD_X - 4, d: 4, r: 2 }, 0, P, 6), { tone: "faint" }));
  return out.join("");
}

function head() {
  const out = [];
  out.push(S(k.slabOf(HEAD, 0, HEAD_H, P, 3, 0.8), { tone: "mid", inner: L(k.sideSeam(HEAD, 10, P, 3), { tone: "faint" }) }));
  // label plate on top: three rule lines for printed text
  const plate = { x: HEAD.x + 4, y: HEAD.y + 8, w: 14, d: 34, r: 1 };
  out.push(S(k.slabOf(plate, HEAD_H, 0.8, P, 3), { tone: "lo", crease: "none" }));
  out.push(L(k.planOutline(k.insetPlan(plate, 1.4), HEAD_H + 0.8, P, 3), { tone: "faint" }));
  const rule = (x, y0, y1) => k.lineOnTop([x, y0], [x, y1], HEAD_H + 0.8, P);
  out.push(L(rule(plate.x + 4.5, plate.y + 5, plate.y + 28) + rule(plate.x + 7.5, plate.y + 5, plate.y + 22) + rule(plate.x + 10.5, plate.y + 5, plate.y + 25), { tone: "lo", free: true }));
  out.push(D([at([plate.x + 7, plate.y + 2.4, HEAD_H + 0.8]), at([plate.x + 7, plate.y + plate.d - 2.4, HEAD_H + 0.8])], { size: 0.42 }));
  out.push(D([at([HEAD.x + 4, HEAD.y + 3.5, HEAD_H]), at([HEAD.x + HEAD.w - 4, HEAD.y + 3.5, HEAD_H]), at([HEAD.x + HEAD.w - 4, HEAD.y + HEAD.d - 3.5, HEAD_H])], { size: 0.45 }));
  // subscription counter in the head's front face
  const face = HEAD.y + HEAD.d;
  out.push(coverPlate(HEAD.x + 3, 16, face, 8, 50));
  const m = k.sideMatrix([HEAD.x + 3, face], 70, P, "left");
  const W = 16, H = 13;
  out.push(`<g transform="${m}">`);
  out.push(`<rect x="-1.2" y="-1.2" width="${W + 2.4}" height="${H + 2.4}" rx="1.6" style="fill:var(--anatomy-shade-3);stroke:var(--anatomy-lo);stroke-width:var(--anatomy-weight);vector-effect:non-scaling-stroke"/>`);
  out.push(`<rect x="0" y="0" width="${W}" height="${H}" rx="1" style="fill:var(--anatomy-shade-0);stroke:var(--anatomy-lo);stroke-width:var(--anatomy-weight);vector-effect:non-scaling-stroke"/>`);
  out.push(`<line x1="${W / 2}" y1="0" x2="${W / 2}" y2="${H}" class="iso-line" data-tone="faint"/>`);
  for (const [i, d] of [[0, "3"], [1, "0"]]) out.push(`<text data-part="digit${i}" x="${W / 4 + (i * W) / 2}" y="${H - 3}" text-anchor="middle" font-size="9" style="font-family:var(--anatomy-mono);fill:var(--anatomy-strong)">${d}</text>`);
  out.push(`</g>`);
  out.push(D([at([HEAD.x + 3 + W / 2, face, 70 - H - 4.2])], { size: 0.5 }));
  return out.join("");
}

// A bolted access cover on a block's front face: a thin raised plate with a screw at each corner.
function coverPlate(x, w, face, z0, z1) {
  const plan = { x, y: face, w, d: 0.8, r: 0.3 };
  const y = face + 0.8;
  const screws = [[x + 2, z0 + 2], [x + w - 2, z0 + 2], [x + 2, z1 - 2], [x + w - 2, z1 - 2]].map(([sx, sz]) => at([sx, y, sz]));
  return S(k.slabOf(plan, z0, z1 - z0, P, 2, 0.3), { tone: "mid", crease: "none" }) + D(screws, { size: 0.45 });
}

function endBlock() {
  const out = [];
  out.push(S(k.slabOf(END, 0, END_H, P, 3, 0.6), { tone: "mid", inner: L(k.sideSeam(END, 10, P, 3), { tone: "faint" }) }));
  out.push(coverPlate(END.x + 2, 8, END.y + END.d, 14, 48));
  // preload knob on top
  const kx = END.x + END.w / 2, ky = 0;
  out.push(S(k.cylinder(kx, ky, 4.2, END_H, 1.4, P, 24), { tone: "lo" }));
  out.push(S(k.cylinder(kx, ky, 5.4, END_H + 1.4, 5, P, 32), { tone: "mid" }));
  out.push(L(k.knurl(kx, ky, 5.4, END_H + 1.4, END_H + 6.4, 32, P), { tone: "lo" }));
  out.push(L(k.lineOnTop([kx, ky], [kx + 4.4, ky], END_H + 6.4, P), { tone: "hi" }));
  out.push(D([at([kx, ky, END_H + 6.4])], { size: 0.5, tone: "hi" }));
  out.push(D([at([END.x + 3, END.y + 4, END_H]), at([END.x + 3, END.y + END.d - 4, END_H])], { size: 0.45 }));
  return out.join("");
}

// Live pieces, written once here and once in the page from the same source.
function guestSvg(x0, gib, lit) {
  const W = gib * U;
  const pt = Math.min(0.9, W / 3);
  const bw = W - 2 * pt;
  const plate = (x) => solidSvg(slabOf({ x, y: -22, w: pt, d: 44, r: Math.min(0.4, pt / 2) }, PLATE_Z, PLATE_H, P, 2), { tone: gib < 4.75 ? "lo" : "mid", lit });
  let body = "";
  if (bw > 0.05) {
    const folds = Math.max(1, Math.round(bw / FOLD));
    const xs = [];
    for (let j = 0; j <= 2 * folds; j++) xs.push(x0 + pt + (bw * j) / (2 * folds));
    const yIn = BODY_Y - 2.4;
    const ring = [];
    xs.forEach((x, j) => ring.push([x, j % 2 ? BODY_Y : yIn]));
    for (let j = xs.length - 1; j >= 0; j--) ring.push([xs[j], j % 2 ? -BODY_Y : -yIn]);
    body = solidSvg(extrude(ring, BODY_Z, BODY_H, P, { convex: false }), { tone: "hi", crease: "lo", lit });
  }
  const shut = Math.max(0, Math.min(1, (2.6 - gib) / 1.0));
  let clasp = "";
  if (shut > 0.02) {
    const top = PLATE_Z + PLATE_H;
    clasp = `<g opacity="${shut.toFixed(2)}">` + solidSvg(slabOf({ x: x0 - 0.6, y: -12, w: W + 1.2, d: 24, r: 0.8 }, top, 4.5, P, 2, 0.4), { tone: "hi", crease: "lo", lit }) + `</g>`;
  }
  return plate(x0) + body + plate(x0 + W - pt) + clasp;
}

function springSvg(endX, lit) {
  const plate = { x: endX, y: -24, w: 3, d: 48, r: 0.8 };
  const from = endX + 3;
  const c = coil(from, END_X, [0, SPRING_Z], 6.5, 7, "x", P);
  return (
    solidSvg(slabOf(plate, PLATE_Z, PLATE_H + 4, P, 2, 0.4), { tone: "mid", lit }) +
    dotsSvg([iso([endX + 1.5, -19, PLATE_Z + PLATE_H + 4], P), iso([endX + 1.5, 19, PLATE_Z + PLATE_H + 4], P)], { size: 0.45 }) +
    lineSvg(sideRing([from, 0, SPRING_Z], 6.5, "x", P, 32) + sideRing([from, 0, SPRING_Z], 4, "x", P, 24), { tone: "lo" }) +
    dotsSvg([[0, 10], [0, -10], [10, 0], [-10, 0], [-17, 24], [17, 24], [-17, -24], [17, -24]].map(([dy, dz]) => iso([from, dy, SPRING_Z + dz], P)), { size: 0.45 }) +
    lineSvg(c.back, { tone: "lo" }) +
    solidSvg(slabOf({ x: from, y: -2, w: END_X - from, d: 4, r: 1.8 }, SPRING_Z - 2, 4, P, 3), { tone: "mid", crease: "none" }) +
    lineSvg(c.front, { tone: "mid" })
  );
}

// node-side aliases so the same functions run in the build
const { solidSvg, slabOf, extrude, coil, dotsSvg, lineSvg, iso, sideRing } = k;

const N0 = M.SEATS;
const startRow = Array.from({ length: N0 }, (_, i) => guestSvg(HEAD_X + i * M.SEAT_GIB * U, M.SEAT_GIB, false)).join("");

const svg = k.figureSvg({
  width: WIDTH,
  height: HEIGHT,
  label:
    "A row of bellows on two rails over a long base, pressed against a head block by a spring plate. Each bellows is one subscription's 8 GiB machine on a 30-seat host; a resting one folds to a 1.5 GiB floor and its air goes to swap, read in a window on the base. The ruler marks 232 GiB of seats and a raised 8 GiB wake reserve. Folding resting bellows lets 37 fit where 30 do today.",
  body: [
    base(),
    railsAndStrips(),
    head(),
    `<g data-part="row">${startRow}</g>`,
    `<g data-part="spring">${springSvg(FULL_X, true)}</g>`,
    endBlock(),
  ],
});

const modelSrc = readFileSync(join(HERE, "model.mjs"), "utf8").replace(/^export /gm, "");

const live = `
${modelSrc}
const P = ${JSON.stringify(P)};
const U = ${U}, HEAD_X = ${HEAD_X}, END_X = ${END_X}, PLATE_Z = ${PLATE_Z}, PLATE_H = ${PLATE_H}, BODY_Z = ${BODY_Z}, BODY_H = ${BODY_H}, BODY_Y = ${BODY_Y}, FOLD = ${FOLD}, SPRING_Z = ${SPRING_Z};
const GLASS_PER_GIB = ${GLASS_PER_GIB}, GLASS_V = ${JSON.stringify(GLASS_V)};
${guestSvg.toString()}
${springSvg.toString()}
const stage = document.querySelector(".iso-stage");
const svgEl = stage.querySelector("svg");
const rowEl = svgEl.querySelector('[data-part="row"]');
const springEl = svgEl.querySelector('[data-part="spring"]');
const swapEl = svgEl.querySelector('[data-part="swap"]');
const digitEls = [svgEl.querySelector('[data-part="digit0"]'), svgEl.querySelector('[data-part="digit1"]')];
const readout = document.querySelector("[data-readout]");
const still = matchMedia("(prefers-reduced-motion: reduce)");
const MAX = 60;
const ORDER = restOrder(MAX);
const cur = new Array(MAX).fill(0).map((_, i) => (i < SEATS ? SEAT_GIB : 0));
const tgt = cur.slice();
let stop = 0, n = SEATS, resting = new Set(), lit = -1, phase = "", pending = null, woke = 0;
const fmt = (v) => v.toFixed(2);

function setStop(index) {
  stop = index;
  const p = STOPS[index].p;
  n = subscriptionsAt(p);
  const r = restingAt(n, p);
  resting = new Set(ORDER.filter((i) => i < n).slice(0, r));
  for (let i = 0; i < MAX; i++) tgt[i] = i < n ? (resting.has(i) ? FLOOR_GIB : SEAT_GIB) : 0;
  lit = -1; phase = ""; pending = null;
  describe();
}
function wake() {
  if (!resting.size || pending) return;
  const list = [...resting].sort((a, b) => a - b);
  const r = list[(woke++ * 5 + 3) % list.length];
  resting.delete(r);
  tgt[r] = SEAT_GIB;
  lit = r; phase = "wake";
  const q = ORDER.find((i) => i < n && i !== r && !resting.has(i) && i % 3 === woke % 3) ?? ORDER.find((i) => i < n && i !== r && !resting.has(i));
  pending = { at: 1.7, q };
  describe();
}
function restNext() {
  const { q } = pending;
  pending = null;
  resting.add(q);
  tgt[q] = FLOOR_GIB;
  lit = q; phase = "rest";
  describe();
}
function measure() {
  let present = 0, rest = 0, swap = 0, row = 0;
  for (let i = 0; i < MAX; i++) {
    const w = cur[i];
    row += w;
    if (w > 0.3) {
      present++;
      if (w < (SEAT_GIB + FLOOR_GIB) / 2) rest++;
      swap += Math.max(0, Math.min(1, (SEAT_GIB - w) / (SEAT_GIB - FLOOR_GIB))) * SWAP_PER_REST_GIB;
    }
  }
  return { present, rest, swap, row };
}
function say(m) {
  let text;
  const used = m.row - BUDGET_GIB;
  if (STOPS[stop].p > 0 && used > 0.25) text = "waking · reserve " + used.toFixed(1) + " of 8 GiB used";
  else text = m.present + " on 30 seats · " + m.rest + " resting · $" + fmt(rentPerSeat(Math.max(1, m.present))) + "/seat";
  if (readout.textContent !== text) readout.textContent = text;
  const c = String(m.present).padStart(2, "0");
  if (digitEls[0].textContent !== c[0]) digitEls[0].textContent = c[0];
  if (digitEls[1].textContent !== c[1]) digitEls[1].textContent = c[1];
}
function describe() {
  const r = restingAt(n, STOPS[stop].p);
  stage.setAttribute("aria-valuenow", String(stop));
  stage.setAttribute("aria-valuetext", STOPS[stop].name + ": " + n + " subscriptions on 30 seats, " + r + " resting, swap " + r * SWAP_PER_REST_GIB + " GiB, rent $" + fmt(rentPerSeat(n)) + " a sold seat");
}
function draw() {
  let x = HEAD_X, html = "";
  for (let i = 0; i < MAX; i++) {
    const w = cur[i];
    if (w > 0.3) html += guestSvg(x, w, i === lit);
    x += w * U;
  }
  rowEl.innerHTML = html;
  springEl.innerHTML = springSvg(x, lit < 0);
  const m = measure();
  const len = Math.max(0.01, m.swap * GLASS_PER_GIB);
  swapEl.setAttribute("d", "M0 " + (GLASS_V[0] + 0.8) + "H" + len.toFixed(2) + "V" + (GLASS_V[1] - 0.8) + "H0Z");
  say(m);
}
const TOUR = [[0, () => setStop(0)], [3, () => setStop(1)], [6.6, wake], [11.4, () => setStop(2)], [14.8, wake], [20, null]];
let frame = 0, last = 0, clock = 0, cue = 0, touring = !still.matches, visible = false, idleTimer = 0, dirty = true;
function tick(now) {
  const dt = last ? Math.min((now - last) / 1000, 1 / 30) : 1 / 60;
  last = now;
  const calm = still.matches;
  if (touring) {
    clock += dt;
    while (cue < TOUR.length && clock >= TOUR[cue][0]) { const fn = TOUR[cue][1]; cue++; if (fn) fn(); else { clock = 0; cue = 0; } }
  }
  if (pending) { pending.at -= calm ? 99 : dt; if (pending.at <= 0) restNext(); }
  let moving = false;
  for (let i = 0; i < MAX; i++) {
    if (cur[i] === tgt[i]) continue;
    cur[i] = calm ? tgt[i] : cur[i] + (tgt[i] - cur[i]) * (1 - Math.exp(-dt / 0.34));
    if (Math.abs(tgt[i] - cur[i]) < 0.004) cur[i] = tgt[i];
    moving = true;
  }
  if (moving || dirty) { draw(); dirty = false; }
  const settled = !touring && !moving && !pending;
  frame = visible && !settled ? requestAnimationFrame(tick) : 0;
}
const run = () => { if (!frame && visible) { last = 0; frame = requestAnimationFrame(tick); } };
const hold = () => { touring = false; clearTimeout(idleTimer); idleTimer = setTimeout(() => { if (!still.matches) { touring = true; clock = 0; cue = 0; } run(); }, 3600); run(); };
const choose = (index) => { if (index !== stop) setStop(index); dirty = true; hold(); };
new IntersectionObserver((entries) => { visible = entries[entries.length - 1].isIntersecting; if (visible) run(); }, { rootMargin: "120px 0px" }).observe(stage);
stage.addEventListener("pointermove", (event) => {
  if (event.pointerType === "touch") return;
  const box = svgEl.getBoundingClientRect();
  const share = (event.clientX - box.left) / box.width;
  choose(share < 0.36 ? 0 : share < 0.68 ? 1 : 2);
});
stage.addEventListener("click", () => { wake(); dirty = true; hold(); });
stage.addEventListener("keydown", (event) => {
  let to = null;
  if (event.key === "ArrowRight" || event.key === "ArrowUp") to = Math.min(STOPS.length - 1, stop + 1);
  if (event.key === "ArrowLeft" || event.key === "ArrowDown") to = Math.max(0, stop - 1);
  if (event.key === "Home") to = 0;
  if (event.key === "End") to = STOPS.length - 1;
  if (event.key === "Enter" || event.key === " ") { event.preventDefault(); wake(); dirty = true; hold(); return; }
  if (to === null) return;
  event.preventDefault();
  choose(to);
});
setStop(still.matches ? 1 : 0);
if (still.matches) for (let i = 0; i < MAX; i++) cur[i] = tgt[i];
draw();
`;

const PHONE_CSS = `<style>.swap-fill{fill:var(--anatomy-hi);fill-opacity:.5}.iso[data-theme="light"] .swap-fill{fill-opacity:.3}</style>`;
const body = k.plateHtml({
  fig: "Fig 3",
  title: "Rest to swap",
  hint: "Hover to set the share, click to wake",
  readout: `30 on 30 seats · 0 resting · $${(M.RENT_USD / 30).toFixed(2)}/seat`,
  keys: [
    { mark: "raised", label: "Bellows: one subscription's 8 GiB machine on the host" },
    { mark: "lit", label: "The machine waking or resting now, or the spring plate" },
    { mark: "flat", label: "Ruler: 29 seats of 8 GiB, then the raised wake reserve" },
  ],
  caption:
    "One AX162-2 host keeps 232 GiB for guests and holds one 8 GiB seat back as a wake reserve. A machine quiet for 15 minutes (no SSH or tmux client, agents idle or waiting, under 6 s of CPU and 64 KiB of traffic a minute) rests: its bellows folds to a 1.5 GiB floor and about 7 GiB goes to encrypted swap, the window on the base, with 160 GiB marked as the proposed partition. At the active account's 29% pausable share the rail carries 37 subscriptions where 30 fit today, and rent per sold seat falls from $33.07 to $26.81, about level with the $27.05 a Solo seat nets after Polar. A wake fills the reserve first, and another quiet machine then rests to free it. Figures from the 2026-10-11 proposal, section 9; wake time and floor size are estimates.",
  body: PHONE_CSS + `<div class="iso-stage" tabindex="0" role="slider" aria-label="Resting machines on one host: arrow keys pick today, the active account's share or the fleet bound; Enter wakes a resting machine" aria-valuemin="0" aria-valuemax="2" aria-valuenow="0" style="touch-action:pan-y">${svg}</div>`,
});

const out = theme === "light" ? "index-light.html" : "index.html";
writeFileSync(join(HERE, out), k.pageHtml({ title: "Rest to swap", theme, body, script: kitScript() + live, width: 760 }));
console.log("wrote", out);
