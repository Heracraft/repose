// Fig 1 for docs/proposals/2026-10-11-price-level.md: a counting balance that weighs
// one Hetzner AX162-2's monthly bill against the Solo seats sold on it.
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import * as k from "../kit/iso-kit.mjs";
import { kitScript } from "../kit/inline-kit.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const theme = process.argv.includes("--light") ? "light" : "dark";
mkdirSync(join(HERE, "shots"), { recursive: true });

// ---- The model: the proposal's section 4, Solo seats on one AX162-2 -------------------
const HOST = 992.1; // EUR 842.30 a month, I-642
const SEATS = 30; // one AX162-2 holds 30 seats of 8 GB
const LADDERS = [
  { id: "A", solo: 29 },
  { id: "B", solo: 39 },
  { id: "C", solo: 49 },
  { id: "D", solo: 59 },
].map((l) => ({ ...l, net: Math.round((l.solo * 0.95 - 0.5) * 100) / 100 })); // Polar 5% + 50c
const restLadder = 2; // C $49 at rest
const evenAt = LADDERS.map((l) => HOST / l.net); // 36.7, 27.1, 21.5, 17.9 seats
const FULL = 1000; // the dial reads -$1,000 .. +$1,000 a month

// ---- World ---------------------------------------------------------------------------
const WIDTH = 600;
const HEIGHT = 380;
const PLATE = { x: -112, y: -48, w: 264, d: 96, r: 8 };
const PLATE_H = 10;
const FOOT_H = 5;

const CASE = { x: -96, y: -30, w: 152, d: 52, r: 6 };
const FRONT = CASE.y + CASE.d; // y = 22
const FOOT_Z = 4;
const BODY_TOP = 34;
const LID_TOP = 40;
const STEM_TOP = 54;
const SLEEVE = [48, 56];
const PAN_Z = 56.6;
const PAN_H = 2.4;
const PAN_R = 22;
const TRAVEL = 4; // pans move +-4 between the stops
const PANS = { left: [-58, -4], right: [18, -4] };

const COIN_R = 5.2;
const STACK_R = 12.4;
const coinT = (net) => net / 10; // thickness carries the weight: $27.05 -> 2.705
const STACKS = [0, 1, 2, 3, 4, 5].map((i) => {
  const a = (Math.PI / 3) * i + Math.PI / 6;
  return [PANS.right[0] + Math.cos(a) * STACK_R, PANS.right[1] + Math.sin(a) * STACK_R];
});

const DIAL = { x0: -42, x1: 2, z0: 9, z1: 32, r: 3, depth: 2.6 };
const PIVOT = [-20, 12.4];
const ARC_R = 15.4;
const SWING = 50; // degrees at the end of the scale

const SIZER = { x: 74, y: -28, w: 70, d: 46, r: 4 };
const SIZER_H = 7;
const WELLS = [86, 100, 114, 128];
const WELL_Y = -10;

// the seat ruler on the bench in front of the case: one tick per seat, a rider at the
// seats sold and a stop at the selected ladder's break-even
const RULER = { x: -34, y: 27, w: 98, d: 11.5, r: 1.2 };
const RULER_H = 1;
const seatX = (s) => 3 * s - 30;
const SLOT = { x: -31.8, y: 31.6, w: 93.6, d: 2.6, r: 1.3 };

const P = k.fitProjection(
  [...k.boxCorners(PLATE, -PLATE_H - FOOT_H, 0), [PANS.left[0], PANS.left[1] + 6, 92], [PANS.right[0] + PAN_R, PANS.right[1], PAN_Z + 30]],
  WIDTH,
  HEIGHT,
  { pad: 16, azimuth: 58 },
);

const at = (point) => k.iso(point, P);
const S = (paths, style) => k.solidSvg(paths, style);
const L = (d, style) => k.lineSvg(d, style);
const D = (points, style) => k.dotsSvg(points, style);
const depth2 = ([x, y]) => k.depthOf([x, y, 0], P);
const byDepth = (points) => [...points].sort((a, b) => depth2(a) - depth2(b));
const planeOf = (y) => k.sideMatrix([0, y], 0, P, "left");
const flat = (x0, z0, x1, z1, r, steps = 6) => k.pathOf(k.roundedPlan({ x: x0, y: z0, w: x1 - x0, d: z1 - z0, r }, steps).map(([u, z]) => [u, -z]), true);

// ---- Bench ---------------------------------------------------------------------------
function bench() {
  const out = [`<path class="iso-halo" d="${k.haloOf(PLATE, -PLATE_H - FOOT_H, PLATE_H, P)}"/>`];
  for (const [x, y] of byDepth(k.corners(PLATE, 20))) {
    out.push(S(k.cylinder(x, y, 7, -PLATE_H - FOOT_H, 1.6, P, 28), { tone: "lo" }));
    out.push(S(k.cylinder(x, y, 5.4, -PLATE_H - FOOT_H + 1.6, FOOT_H - 1.6, P, 28), { tone: "mid" }));
  }
  out.push(S(k.slabOf(PLATE, -PLATE_H, PLATE_H, P, 8, 1.6), { tone: "mid" }));
  out.push(L(k.planOutline(k.insetPlan(PLATE, 5), 0, P), { tone: "lo" }));
  const screws = k.corners(PLATE, 10);
  out.push(L(screws.map(([x, y]) => k.ring(x, y, 2.2, 0, P, 16)).join(""), { tone: "lo" }));
  out.push(D(screws.map(([x, y]) => at([x, y, 0])), { size: 0.5 }));
  // label plate on the bench, in front of the balance
  const label = { x: -78, y: 30, w: 40, d: 9, r: 1.2 };
  out.push(S(k.slabOf(label, 0, 0.8, P, 3), { tone: "lo", crease: "none" }));
  out.push(L(k.planOutline(k.insetPlan(label, 1.2), 0.8, P, 3), { tone: "faint" }));
  out.push(L([28, 20, 24].map((w, i) => k.lineOnTop([label.x + 4, label.y + 2.6 + i * 1.9], [label.x + 4 + w, label.y + 2.6 + i * 1.9], 0.8, P)).join(""), { tone: "lo", free: true }));
  out.push(D([at([label.x + label.w - 3, label.y + 2.4, 0.8]), at([label.x + label.w - 3, label.y + label.d - 2.4, 0.8])], { size: 0.45 }));
  return out.join("");
}

// ---- The ladder sizer: four reference stacks and a selector carriage ---------------------
function sizer() {
  const out = [`<path class="iso-halo" d="${k.haloOf(k.insetPlan(SIZER, 2), 0, 2, P)}" style="filter:blur(5px)"/>`];
  out.push(S(k.slabOf(SIZER, 0, SIZER_H, P, 5, 0.8), { tone: "mid" }));
  out.push(L(k.planOutline(k.insetPlan(SIZER, 3), SIZER_H, P, 4), { tone: "faint" }));
  out.push(D(k.corners(SIZER, 5.5).map(([x, y]) => at([x, y, SIZER_H])), { size: 0.45 }));
  // rail and tick strip in front of the wells
  const rail = { x: 80, y: 3, w: 56, d: 3, r: 1 };
  out.push(S(k.slabOf(rail, SIZER_H, 1.4, P, 3, 0.3), { tone: "lo" }));
  out.push(D([at([rail.x + 2, rail.y + 1.5, SIZER_H + 1.4]), at([rail.x + rail.w - 2, rail.y + 1.5, SIZER_H + 1.4])], { size: 0.4 }));
  const strip = { x: 79, y: 10.5, w: 58, d: 4, r: 0.8 };
  out.push(S(k.slabOf(strip, SIZER_H, 0.5, P, 3), { tone: "lo", crease: "none" }));
  const ticks = k.topTicks(WELLS[0], WELLS[3], 3.5, 4, strip.y + 0.2, SIZER_H + 0.5, [1.4, 3], P);
  out.push(L(ticks.minor, { tone: "lo" }), L(ticks.major, { tone: "mid" }));
  // wells, back to front
  const wells = WELLS.map((x, i) => [x, WELL_Y, i]).sort((a, b) => depth2(a) - depth2(b));
  for (const [x, y, i] of wells) {
    out.push(S(k.cylinder(x, y, COIN_R + 1.4, SIZER_H, 1.2, P, 40, 0.3), { tone: "lo" }));
    const t = coinT(LADDERS[i].net);
    const stack = (tone) => [0, 1, 2].map((n) => coinSvg(x, y, SIZER_H + 1.2 + n * t, t, "", tone)).join("");
    out.push(stack("mid"), `<g data-ref="${i}" style="opacity:${i === restLadder ? 1 : 0}">${stack("hi")}</g>`);
  }
  return out.join("");
}

function selector() {
  const plan = { x: -5, y: 1.6, w: 10, d: 7.2, r: 1.4 };
  const z = SIZER_H + 1.4;
  const out = [];
  out.push(S(k.slabOf(plan, z, 3.2, P, 3, 0.4), { tone: "mid" }));
  out.push(L(k.lineOnTop([0, plan.y + 0.6], [0, plan.y + plan.d - 0.6], z + 3.2, P), { tone: "hi" }));
  out.push(D([at([-3, plan.y + plan.d - 1.4, z + 3.2]), at([3, plan.y + plan.d - 1.4, z + 3.2])], { size: 0.4 }));
  return out.join("");
}

function coinSvg(x, y, z, t, extra = "", tone = "hi") {
  const paths = k.cylinder(x, y, COIN_R, z, t, P, 40, 0.3);
  return `<g${extra}>${S(paths, { tone, crease: "lo", inner: L(k.knurl(x, y, COIN_R, z, z + t, 30, P, 0.25), { tone: "faint" }) })}</g>`;
}

// ---- The balance case ------------------------------------------------------------------
let clips = 0;
function recess({ x0, x1, z0, z1, r, depth }, y, back = "") {
  const id = `pl-clip-${clips++}`;
  const ring = k.roundedPlan({ x: x0, y: z0, w: x1 - x0, d: z1 - z0, r }, 6);
  const cx = (x0 + x1) / 2;
  const cz = (z0 + z1) / 2;
  const { sinA } = k.cameraOf(P);
  const tops = [];
  const sides = [[], [], [], []];
  ring.forEach(([ax, az], index) => {
    const [bx, bz] = ring[(index + 1) % ring.length];
    const length = Math.hypot(bx - ax, bz - az) || 1;
    let [nx, nz] = [(bz - az) / length, -(bx - ax) / length];
    if (nx * (cx - (ax + bx) / 2) + nz * (cz - (az + bz) / 2) < 0) [nx, nz] = [-nx, -nz];
    if (nx <= 0 && nz <= 0) return;
    const quad = k.pathOf([at([ax, y, az]), at([bx, y, bz]), at([bx, y - depth, bz]), at([ax, y - depth, az])], true);
    if (nz >= nx) tops.push(quad);
    else sides[Math.min(3, Math.max(0, Math.floor(((nx * -sinA + 1) / 2) * 4)))].push(quad);
  });
  const shape = flat(x0, z0, x1, z1, r);
  return [
    `<clipPath id="${id}"><path transform="${planeOf(y)}" d="${shape}"/></clipPath>`,
    `<g clip-path="url(#${id})">`,
    `<path class="iso-top" d="${tops.join("")}"/>`,
    ...sides.map((d, shade) => (d.length ? `<path class="iso-shade" data-shade="${shade}" d="${d.join("")}"/>` : "")),
    `<g transform="${planeOf(y - depth)}"><path class="pl-dial" d="${shape}"/>${back}<path class="iso-line" data-tone="faint" d="${shape}"/></g>`,
    `</g>`,
    `<g transform="${planeOf(y)}"><path class="iso-line" data-tone="lo" d="${shape}"/></g>`,
  ].join("");
}

const polar = (deg, r) => {
  const a = (deg * Math.PI) / 180;
  return [PIVOT[0] + Math.sin(a) * r, -(PIVOT[1] + Math.cos(a) * r)];
};
const angleOf = (result) => SWING * Math.max(-1, Math.min(1, result / FULL));
const pt = ([u, v]) => `${u.toFixed(2)} ${v.toFixed(2)}`;

function dialFace() {
  const out = [];
  // the scale arc, ticks rooted on it, every $100 with a major every $500
  const arc = [];
  for (let d = -SWING; d <= SWING + 0.01; d += 2) arc.push(polar(d, ARC_R));
  out.push(`<path class="iso-line" data-tone="mid" d="M${arc.map(pt).join("L")}"/>`);
  const minor = [];
  const major = [];
  for (let v = -FULL; v <= FULL; v += 100) {
    const d = angleOf(v);
    const big = v % 500 === 0;
    const seg = `M${pt(polar(d, ARC_R))}L${pt(polar(d, ARC_R + (big ? 3 : 1.7)))}`;
    if (v === 0) continue;
    (big ? major : minor).push(seg);
  }
  out.push(`<path class="iso-line" data-tone="lo" d="${minor.join("")}"/>`);
  out.push(`<path class="iso-line" data-tone="mid" d="${major.join("")}"/>`);
  out.push(`<path class="iso-line" data-tone="hi" d="M${pt(polar(0, ARC_R))}L${pt(polar(0, ARC_R + 4))}"/>`);
  // a second, inner arc over the black: the stretch where the host pays for itself
  const black = [];
  for (let d = 0; d <= SWING + 0.01; d += 2) black.push(polar(d, ARC_R - 1.4));
  out.push(`<path class="iso-line" data-tone="hi" d="M${black.map(pt).join("L")}"/>`);
  out.push(`<path class="iso-line" data-tone="mid" d="M${pt(polar(0, ARC_R - 1.4))}L${pt(polar(0, ARC_R))}M${pt(polar(SWING, ARC_R - 1.4))}L${pt(polar(SWING, ARC_R))}"/>`);
  return out.join("");
}

const needleD = (deg) => `M${pt(polar(deg + 180, 3))}L${pt(polar(deg, ARC_R + 2.2))}`;

function body() {
  const out = [`<path class="iso-halo" d="${k.haloOf(k.insetPlan(CASE, 2), 0, 2, P)}" style="filter:blur(6px)"/>`];
  for (const [x, y] of byDepth(k.corners(CASE, 9))) {
    out.push(S(k.cylinder(x, y, 4.4, 0, 1.2, P, 24), { tone: "lo" }));
    out.push(S(k.cylinder(x, y, 3.4, 1.2, FOOT_Z - 1.2, P, 24), { tone: "mid", inner: L(k.knurl(x, y, 3.4, 1.2, FOOT_Z, 16, P, 0.2), { tone: "lo" }) }));
  }
  const seam = k.sideSeam(CASE, 6.4, P, 6);
  out.push(S(k.slabOf(CASE, FOOT_Z, BODY_TOP - FOOT_Z, P, 6, 0.8), { tone: "mid", inner: L(seam, { tone: "faint" }) }));
  const lid = k.insetPlan(CASE, 1.2);
  out.push(S(k.slabOf(lid, BODY_TOP, LID_TOP - BODY_TOP, P, 6, 1.0), { tone: "mid" }));
  out.push(L(k.planOutline(k.insetPlan(CASE, 5), LID_TOP, P, 6), { tone: "faint" }));
  out.push(D(k.corners(CASE, 7).map(([x, y]) => at([x, y, LID_TOP])), { size: 0.45 }));
  // front: dial window, nameplate with screws
  out.push(recess(DIAL, FRONT, dialFace() + `<path class="iso-line" data-tone="lit" data-part="needle" data-free="" d="${needleD(angleOf(-HOST))}" style="stroke-width:1.1"/><circle class="pl-hub" cx="${PIVOT[0]}" cy="${-PIVOT[1]}" r="1.3"/>`));
  const name = { x0: 12, x1: 46, z0: 14, z1: 26 };
  const front = (d) => `<g transform="${planeOf(FRONT)}">${d}</g>`;
  out.push(front(`<path class="iso-line" data-tone="lo" d="${flat(name.x0, name.z0, name.x1, name.z1, 1.4)}"/>`));
  out.push(front(`<path class="iso-line" data-tone="lo" data-free="" d="M${name.x0 + 4} ${-(name.z1 - 3.4)}H${name.x0 + 24}M${name.x0 + 4} ${-(name.z1 - 6)}H${name.x0 + 18}M${name.x0 + 4} ${-(name.z1 - 8.6)}H${name.x0 + 21}"/>`));
  out.push(D([at([name.x1 - 3, FRONT, name.z1 - 3]), at([name.x1 - 3, FRONT, name.z0 + 3])], { size: 0.45 }));
  // the meter's zero adjuster: a slotted screw head beside the window
  const [zu, zz] = [7, 13];
  out.push(front(`<circle class="iso-line" data-tone="lo" cx="${zu}" cy="${-zz}" r="2"/><path class="iso-line" data-tone="lo" d="M${zu - 1.4} ${-zz - 1.4}L${zu + 1.4} ${-zz + 1.4}"/>`));
  out.push(endFace());
  // a vent slot row under the dial (closed slots)
  const slots = [];
  for (let i = 0; i < 9; i++) slots.push(flat(-88 + i * 4.2, 9, -86.4 + i * 4.2, 18, 0.8, 3));
  out.push(front(`<path class="iso-line" data-tone="lo" d="${slots.join("")}"/>`));
  return out.join("");
}

// things on the lid: spirit level, tare knob, column collars and slots
function lid() {
  const out = [];
  const vial = { x: -27, y: -27, w: 14, d: 6, r: 2.6 };
  out.push(S(k.slabOf(vial, LID_TOP, 2, P, 4, 0.4), { tone: "mid" }));
  out.push(L(k.planOutline(k.insetPlan(vial, 1.2), LID_TOP + 2, P, 4), { tone: "lo" }));
  out.push(L(k.ring(vial.x + vial.w / 2, vial.y + vial.d / 2, 1.2, LID_TOP + 2, P, 20), { tone: "hi" }));
  out.push(L(k.lineOnTop([vial.x + vial.w / 2 - 2.4, vial.y + 1.2], [vial.x + vial.w / 2 - 2.4, vial.y + vial.d - 1.2], LID_TOP + 2, P) + k.lineOnTop([vial.x + vial.w / 2 + 2.4, vial.y + 1.2], [vial.x + vial.w / 2 + 2.4, vial.y + vial.d - 1.2], LID_TOP + 2, P), { tone: "lo" }));
  // tare knob at the front right corner
  const knob = [44, 13];
  out.push(S(k.cylinder(knob[0], knob[1], 5.4, LID_TOP, 1.2, P, 28), { tone: "lo" }));
  out.push(S(k.cylinder(knob[0], knob[1], 4.2, LID_TOP + 1.2, 4, P, 28, 0.3), { tone: "mid", inner: L(k.knurl(knob[0], knob[1], 4.2, LID_TOP + 1.2, LID_TOP + 5.2, 26, P), { tone: "lo" }) }));
  out.push(L(k.lineOnTop([knob[0], knob[1]], [knob[0] + 3.4, knob[1]], LID_TOP + 5.2, P), { tone: "hi" }));
  out.push(D([at([knob[0], knob[1], LID_TOP + 5.2])], { size: 0.5, tone: "hi" }));
  return out.join("");
}

function column([x, y]) {
  const slot = { x: x - 7.5, y: y - 6, w: 15, d: 12, r: 5 };
  const out = [L(k.planOutline(slot, LID_TOP, P, 6), { tone: "lo" })];
  out.push(S(k.cylinder(x, y, 4.6, LID_TOP, 1.4, P, 28, 0.3), { tone: "lo" }));
  out.push(S(k.cylinder(x, y, 2.3, LID_TOP + 1.4, STEM_TOP - LID_TOP - 1.4, P, 20), { tone: "mid", crease: "none" }));
  return out.join("");
}

// the moving part of each column: sleeve, hub and pan
function pan([x, y]) {
  const out = [`<path class="iso-halo" d="${k.haloOf({ x: x - PAN_R + 4, y: y - PAN_R + 4, w: PAN_R * 2 - 8, d: PAN_R * 2 - 8, r: PAN_R - 4 }, LID_TOP, 1, P)}" style="filter:blur(7px);opacity:calc(var(--anatomy-halo-opacity) * .7)"/>`];
  out.push(S(k.cylinder(x, y, 3.4, SLEEVE[0], SLEEVE[1] - SLEEVE[0], P, 20), { tone: "mid", inner: L(k.sideArc(x, y, 3.4, SLEEVE[0] + 2, P, 14), { tone: "faint" }) }));
  out.push(S(k.cylinder(x, y, 7, SLEEVE[1], PAN_Z - SLEEVE[1], P, 32), { tone: "lo" }));
  out.push(S(k.cylinder(x, y, PAN_R, PAN_Z, PAN_H, P, 72, 0.6), { tone: "mid", inner: L(k.sideArc(x, y, PAN_R, PAN_Z + 0.8, P, 40), { tone: "faint" }) }));
  out.push(L(k.ring(x, y, PAN_R - 2.6, PAN_Z + PAN_H, P, 72), { tone: "faint" }));
  return out.join("");
}

// the counterweight: one host's monthly bill
function weight([x, y]) {
  const z = PAN_Z + PAN_H;
  const out = [];
  out.push(S(k.cylinder(x, y, 11.6, z, 1.6, P, 48, 0.3), { tone: "mid" }));
  const bodyZ = z + 1.6;
  const H = 20;
  out.push(S(k.cylinder(x, y, 10.6, bodyZ, H, P, 56, 0.6), { tone: "hi", crease: "lo", inner: L(k.sideArc(x, y, 10.6, bodyZ + 4, P, 32) + k.sideArc(x, y, 10.6, bodyZ + H - 4, P, 32), { tone: "faint" }) }));
  out.push(k.faceTextSvg(k.topMatrix([x, y + 7.4], bodyZ + H, P, "x"), "992.10", { size: 2.2, tone: "lo", anchor: "middle" }));
  out.push(S(k.cylinder(x, y, 3.6, bodyZ + H, 3, P, 24), { tone: "mid", crease: "lo" }));
  out.push(S(k.cylinder(x, y, 5.6, bodyZ + H + 3, 4.6, P, 32, 0.5), { tone: "hi", crease: "lo", inner: L(k.knurl(x, y, 5.6, bodyZ + H + 3, bodyZ + H + 7.6, 24, P, 0.3), { tone: "faint" }) }));
  out.push(L(k.ring(x, y, 2.2, bodyZ + H + 7.6, P, 20), { tone: "lo" }));
  return out.join("");
}

// the case's right end: a mains inlet with its fuse drawer, and a vent row
function endFace() {
  const X = CASE.x + CASE.w;
  const plane = (d) => `<g transform="${k.sideMatrix([X, 0], 0, P, "right")}">${d}</g>`;
  // in this plane u = -y and v = -z
  const out = [];
  out.push(plane(`<path class="iso-line" data-tone="lo" d="${flat(-9, 10, 5, 21, 1.8)}"/>`));
  out.push(plane(`<path class="iso-line" data-tone="faint" d="${flat(-7.6, 11.4, 3.6, 19.6, 1.2)}"/>`));
  out.push(plane(`<path class="iso-line" data-tone="mid" d="${flat(-5.4, 13.2, -4.4, 16.4, 0.4, 2)}${flat(-2.5, 13.2, -1.5, 16.4, 0.4, 2)}${flat(0.4, 13.2, 1.4, 16.4, 0.4, 2)}"/>`));
  out.push(D([at([X, 11.4, 15.5]), at([X, -9.4, 15.5])], { size: 0.45 }));
  // fuse drawer: a round cap with a screwdriver slot that ends on its rim
  const [fu, fz] = [13, 15.5];
  out.push(plane(`<circle class="iso-line" data-tone="lo" cx="${fu}" cy="${-fz}" r="3"/><circle class="iso-line" data-tone="faint" cx="${fu}" cy="${-fz}" r="2.2"/><path class="iso-line" data-tone="lo" d="M${fu - 2.2} ${-fz}H${fu + 2.2}"/>`));
  // vents above, closed slots
  const vents = [];
  for (let i = 0; i < 7; i++) vents.push(flat(-16 + i * 4.4, 24.5, -14.6 + i * 4.4, 30, 0.6, 3));
  out.push(plane(`<path class="iso-line" data-tone="lo" d="${vents.join("")}"/>`));
  return out.join("");
}

// the seat ruler on the bench
function ruler() {
  const out = [];
  out.push(S(k.slabOf(RULER, 0, RULER_H, P, 3, 0.3), { tone: "lo" }));
  const ticks = k.topTicks(seatX(0), seatX(SEATS), 3, 5, RULER.y + 0.3, RULER_H, [1.6, 3], P);
  out.push(L(ticks.minor, { tone: "lo" }), L(ticks.major, { tone: "mid" }));
  out.push(L(k.planOutline(SLOT, RULER_H, P, 3), { tone: "faint" }));
  out.push(L(k.planOutline({ x: SLOT.x, y: RULER.y + RULER.d - 2.6, w: SLOT.w, d: 1, r: 0.5 }, RULER_H, P, 3), { tone: "faint" }));
  out.push(D([at([RULER.x + 1.8, RULER.y + RULER.d - 1.8, RULER_H]), at([RULER.x + RULER.w - 1.8, RULER.y + RULER.d - 1.8, RULER_H])], { size: 0.4 }));
  return out.join("");
}
function rider() {
  const plan = { x: -2.2, y: SLOT.y - 0.9, w: 4.4, d: SLOT.d + 1.8, r: 0.8 };
  const z = RULER_H;
  return S(k.slabOf(plan, z, 1.8, P, 3, 0.3), { tone: "mid" }) + L(k.lineOnTop([0, plan.y + 0.5], [0, plan.y + plan.d - 0.5], z + 1.8, P), { tone: "hi" });
}
function stop() {
  // a wedge-nosed stop in its own front groove, clear of the rider
  const plan = { x: -1.4, y: RULER.y + RULER.d - 3.2, w: 2.8, d: 2.2, r: 0.5 };
  return S(k.slabOf(plan, RULER_H, 1.4, P, 2, 0.2), { tone: "hi", crease: "lo" }) + L(k.lineOnTop([0, plan.y + 0.4], [0, plan.y + plan.d - 0.4], RULER_H + 1.4, P), { tone: "lo" });
}

// thirty seats per ladder: seat i sits in stack i % 6 at level floor(i / 6)
function coins(ladder) {
  const t = coinT(ladder.net);
  const z0 = PAN_Z + PAN_H;
  const order = STACKS.map((p, s) => [p, s]).sort((a, b) => depth2(a[0]) - depth2(b[0]));
  const out = [];
  for (const [[x, y], s] of order) {
    for (let level = 0; level < SEATS / 6; level++) {
      const i = level * 6 + s;
      out.push(coinSvg(x, y, z0 + level * t, t, ` data-coin="${i}" style="opacity:0"`));
    }
  }
  return out.join("");
}

// ---- Assembly ------------------------------------------------------------------------
const svg = k.figureSvg({
  width: WIDTH,
  height: HEIGHT,
  label:
    "A counting balance on a bench. The left pan carries a single weight standing for one Hetzner AX162-2's monthly bill of $992.10. The right pan carries one coin per Solo seat sold on that host, up to 30, each coin as thick as the seat's net price after Polar under the chosen ladder. A dial on the front reads the monthly result from -$1,000 to +$1,000. Beside it, four reference stacks show the coin for ladders A to D, and a selector carriage marks the one in use.",
  body: [
    bench(),
    sizer(),
    `<g data-part="selector" transform="${k.translateAlong(P, [WELLS[restLadder], 0, 0])}">${selector()}</g>`,
    body(),
    ruler(),
    `<g data-part="rider" transform="${k.translateAlong(P, [seatX(22), 0, 0])}">${rider()}</g>`,
    `<g data-part="stop" transform="${k.translateAlong(P, [seatX(evenAt[restLadder]), 0, 0])}">${stop()}</g>`,
    lid(),
    column(PANS.left),
    `<g data-part="pan-left">${pan(PANS.left)}${weight(PANS.left)}</g>`,
    column(PANS.right),
    `<g data-part="pan-right">${pan(PANS.right)}${LADDERS.map((l, i) => `<g data-ladder="${i}" style="opacity:${i === restLadder ? 1 : 0}">${coins(l)}</g>`).join("")}</g>`,
  ],
});

const live = `
const P = ${JSON.stringify(P)};
const LADDERS = ${JSON.stringify(LADDERS)};
const HOST = ${HOST}, SEATS = ${SEATS}, FULL = ${FULL}, SWING = ${SWING}, TRAVEL = ${TRAVEL}, ARC_R = ${ARC_R};
const PIVOT = ${JSON.stringify(PIVOT)};
const WELLS = ${JSON.stringify(WELLS)};
const stage = document.querySelector(".iso-stage");
const svg = stage.querySelector("svg");
const q = (s) => svg.querySelector(s);
const needle = q('[data-part="needle"]');
const panL = q('[data-part="pan-left"]');
const panR = q('[data-part="pan-right"]');
const sel = q('[data-part="selector"]');
const rider = q('[data-part="rider"]');
const stopG = q('[data-part="stop"]');
const refs = [...svg.querySelectorAll("[data-ref]")];
const EVEN = ${JSON.stringify(evenAt)};
const seatX = (s) => 3 * s - 30;
const U0 = ${at([seatX(0), RULER.y, 0])[0] / WIDTH}, U30 = ${at([seatX(SEATS), RULER.y, 0])[0] / WIDTH};
const sets = [...svg.querySelectorAll("[data-ladder]")];
const coinsOf = sets.map((g) => { const list = []; g.querySelectorAll("[data-coin]").forEach((c) => (list[+c.dataset.coin] = c)); return list; });
const readout = document.querySelector("[data-readout]");
const still = matchMedia("(prefers-reduced-motion: reduce)");
const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const polar = ${polarSrc()};
const pt = ([u, v]) => u.toFixed(2) + " " + v.toFixed(2);
const needleD = (deg) => "M" + pt(polar(deg + 180, 3)) + "L" + pt(polar(deg, ARC_R + 2.2));
const money = (v) => (v < 0 ? "-$" : "+$") + Math.abs(v).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const resultOf = (ladder, seats) => seats * LADDERS[ladder].net - HOST;
const TOUR = [[0, 30], [1, 28], [2, 22], [3, 18], [2, 15], [0, 15]];
let ladder = ${restLadder}, seatsTo = 22, seats = 0, mix = LADDERS.map((_, i) => (i === ${restLadder} ? 1 : 0));
let selX = WELLS[${restLadder}], beam = { at: -1, rate: 0 };
let frame = 0, last = 0, clock = 0, touring = !still.matches, visible = false, idleTimer = 0, said = "";
const tourAt = (t) => TOUR[Math.floor(t / 3.2) % TOUR.length];
function describe() {
  const n = Math.round(seatsTo), l = LADDERS[ladder], r = resultOf(ladder, n);
  stage.setAttribute("aria-valuenow", String(n));
  stage.setAttribute("aria-valuetext", "Ladder " + l.id + ", Solo $" + l.solo + ", " + n + " of 30 seats sold: " + money(r) + " a month");
}
function draw() {
  // the beam: a spring-restored balance, so travel is proportional to the imbalance
  const share = clamp(beam.at, -1, 1);
  needle.setAttribute("d", needleD(SWING * share));
  panL.setAttribute("transform", translateAlong(P, [0, 0, TRAVEL * share]));
  panR.setAttribute("transform", translateAlong(P, [0, 0, -TRAVEL * share]));
  sel.setAttribute("transform", translateAlong(P, [selX, 0, 0]));
  rider.setAttribute("transform", translateAlong(P, [seatX(seats), 0, 0]));
  // the stop at break-even: off the scale for A, so it lifts away
  let even = 0, onScale = 0;
  mix.forEach((m, i) => { even += m * Math.min(EVEN[i], SEATS + 1); onScale += m * (EVEN[i] <= SEATS ? 1 : 0); });
  stopG.setAttribute("transform", translateAlong(P, [seatX(even), 0, 0]));
  stopG.style.opacity = onScale.toFixed(3);
  refs.forEach((g, i) => (g.style.opacity = mix[i].toFixed(3)));
  sets.forEach((g, i) => {
    g.style.opacity = mix[i].toFixed(3);
    g.style.display = mix[i] < 0.002 ? "none" : "";
    if (mix[i] < 0.002) return;
    coinsOf[i].forEach((c, n) => {
      const f = clamp(seats - n, 0, 1);
      c.style.opacity = f.toFixed(3);
      c.setAttribute("transform", f < 1 ? translateAlong(P, [0, 0, (1 - f) * 9]) : "");
    });
  });
  const n = Math.round(seats), l = LADDERS[ladder];
  const text = l.id + " $" + l.solo + " · " + n + " of 30 seats · " + money(resultOf(ladder, n)) + "/mo";
  if (readout.textContent !== text) readout.textContent = text;
}
function tick(now) {
  const dt = last ? Math.min((now - last) / 1000, 1 / 30) : 1 / 60;
  last = now;
  const calm = still.matches;
  if (touring) { clock += dt; const [lt, st] = tourAt(clock); if (lt !== ladder) { ladder = lt; describe(); } seatsTo = st; }
  const ease = (v, to, tau) => (calm ? to : v + (to - v) * (1 - Math.exp(-dt / tau)));
  seats = ease(seats, seatsTo, 0.45); if (Math.abs(seats - seatsTo) < 0.002) seats = seatsTo;
  selX = ease(selX, WELLS[ladder], 0.22); if (Math.abs(selX - WELLS[ladder]) < 0.01) selX = WELLS[ladder];
  mix = mix.map((m, i) => { const to = i === ladder ? 1 : 0; const v = ease(m, to, 0.18); return Math.abs(v - to) < 0.002 ? to : v; });
  let mass = 0; mix.forEach((m, i) => (mass += m * seats * LADDERS[i].net));
  const want = clamp((mass - HOST) / FULL, -1, 1);
  if (calm) { beam.at = want; beam.rate = 0; }
  else for (let i = 0; i < 4; i++) { beam.rate += ((want - beam.at) * 60 - beam.rate * 10.5) * dt / 4; beam.at += beam.rate * dt / 4; }
  if (Math.abs(want - beam.at) < 5e-4 && Math.abs(beam.rate) < 5e-4) { beam.at = want; beam.rate = 0; }
  draw();
  const settled = !touring && seats === seatsTo && beam.rate === 0 && beam.at === want && mix[ladder] === 1 && selX === WELLS[ladder];
  frame = visible && !settled ? requestAnimationFrame(tick) : 0;
}
const run = () => { if (!frame && visible) { last = 0; frame = requestAnimationFrame(tick); } };
const hold = () => { touring = false; clearTimeout(idleTimer); idleTimer = setTimeout(() => { touring = !still.matches; clock = 0; run(); }, 3600); };
const setSeats = (n) => { hold(); seatsTo = clamp(Math.round(n), 0, SEATS); describe(); run(); };
const setLadder = (i) => { hold(); ladder = (i + LADDERS.length) % LADDERS.length; describe(); run(); };
// the pointer's x maps onto the seat ruler, widened so the whole stage sells 0 to 30
const MID = (U0 + U30) / 2, SPAN = Math.max(U30 - U0, 0.5), SPLIT = 0.74;
const seatsAt = (f) => clamp((f - (MID - SPAN / 2)) / SPAN, 0, 1) * SEATS;
new IntersectionObserver((entries) => { visible = entries[entries.length - 1].isIntersecting; if (visible) run(); }, { rootMargin: "120px 0px" }).observe(stage);
stage.addEventListener("pointermove", (event) => {
  if (event.pointerType === "touch") return;
  const box = svg.getBoundingClientRect();
  setSeats(seatsAt((event.clientX - box.left) / box.width));
});
stage.addEventListener("click", (event) => {
  if (event.pointerType === "touch" || event.pointerType === "") {
    const box = svg.getBoundingClientRect();
    const f = (event.clientX - box.left) / box.width;
    if (f < SPLIT) { setSeats(seatsAt(f)); return; }
  }
  setLadder(ladder + 1);
});
stage.addEventListener("keydown", (event) => {
  const step = event.shiftKey ? 5 : 1;
  const k = event.key;
  if (k === "ArrowRight") setSeats(seatsTo + step);
  else if (k === "ArrowLeft") setSeats(seatsTo - step);
  else if (k === "ArrowUp") setLadder(ladder + 1);
  else if (k === "ArrowDown") setLadder(ladder - 1);
  else if (k === "Home") setSeats(0);
  else if (k === "End") setSeats(SEATS);
  else if ("1234".includes(k) && k.length === 1) setLadder(+k - 1);
  else if ("abcdABCD".includes(k) && k.length === 1) setLadder("abcd".indexOf(k.toLowerCase()));
  else return;
  event.preventDefault();
});
if (still.matches) { seats = seatsTo; }
describe();
draw();
`;

function polarSrc() {
  return `(deg, r) => { const a = (deg * Math.PI) / 180; return [PIVOT[0] + Math.sin(a) * r, -(PIVOT[1] + Math.cos(a) * r)]; }`;
}

const css = `<style>
.pl-dial{fill:var(--anatomy-shade-1);stroke:none}
.iso[data-theme="light"] .pl-dial{fill:var(--anatomy-face)}
.pl-hub{fill:var(--anatomy-lit);stroke:none}
.iso-stage{cursor:pointer;touch-action:pan-y;outline:none}
.iso-stage:focus-visible{outline:1px solid var(--anatomy-muted);outline-offset:6px}
@media (max-width:560px){.iso-plate{overflow:hidden}.iso-stage .iso-svg{width:106%;margin:0 -3%}}
</style>`;

const plate = k.plateHtml({
  fig: "Fig 1",
  title: "One host on the balance",
  hint: "Hover to sell seats · click for a ladder",
  readout: `C $49 · 22 of 30 seats · +$${(22 * LADDERS[2].net - HOST).toFixed(2)}/mo`,
  keys: [
    { mark: "lit", label: "Needle: the owner's monthly result on one AX162-2" },
    { mark: "raised", label: "Coins: one Solo seat each, as thick as its net price" },
    { mark: "flat", label: "Ruler: a rider at the seats sold, a stop at break-even" },
  ],
  caption:
    `A counting balance weighs one Hetzner AX162-2 against the Solo seats sold on it. ` +
    `The weight on the left is the server's bill, $992.10 a month (I-642, setup fee left out). ` +
    `Each coin on the right is one seat at the selected ladder's price after Polar's 5% and 50 cents: ` +
    LADDERS.map((l) => `${l.id} $${l.solo} nets $${l.net.toFixed(2)}`).join(", ") + `. ` +
    `With all 30 seats sold, A still loses $180.60 a month. ` +
    `B breaks even at 27.1 seats (91% fill), C at 21.5 (72%) and D at 17.9 (60%). At 15 seats C still loses $301.35 a month. ` +
    `The ruler in front has one tick per seat: its rider follows the seats sold and its stop sits at the selected ladder's break-even. ` +
    `Under A the stop leaves the ruler, since 30 seats never cover the bill.`,
  body: `<div class="iso-stage" tabindex="0" role="slider" aria-label="Seats sold on one host: left and right arrows sell seats, up and down or 1 to 4 pick ladder A to D" aria-valuemin="0" aria-valuemax="30" aria-valuenow="22">${svg}</div>`,
});

const file = theme === "light" ? "index-light.html" : "index.html";
writeFileSync(join(HERE, file), k.pageHtml({ title: "One host on the balance", theme, body: css + plate, script: kitScript() + live }));
console.log("wrote", file, "· break-even seats", evenAt.map((v) => v.toFixed(1)).join(" / "));
