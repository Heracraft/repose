// The arithmetic behind the project page's usage charts (UsageChart.svelte,
// DECISIONS I-492): points into line segments with gaps where the machine
// was stopped, and the words a chart's reading uses.

/** One value at one time; a null value is a gap (not reported). */
export interface Pt {
	t: number;
	v: number | null;
}

/**
 * Split points into runs to draw as one line each. A run ends at a null
 * value or where the next point is more than one and a half steps later,
 * which is a time the machine was not running (the api sends no bucket
 * for it), so the chart shows a gap instead of a line across the stop.
 */
export function segments(points: Pt[], stepS: number): { t: number; v: number }[][] {
	const out: { t: number; v: number }[][] = [];
	let run: { t: number; v: number }[] = [];
	let prev: number | undefined;
	for (const p of points) {
		const late = prev !== undefined && p.t - prev > stepS * 1500;
		if (p.v === null || late) {
			if (run.length > 0) out.push(run);
			run = [];
		}
		if (p.v !== null) run.push({ t: p.t, v: p.v });
		prev = p.t;
	}
	if (run.length > 0) out.push(run);
	return out;
}

/** An SVG path through a run, and the closed area under it to y0. */
export function paths(
	run: { t: number; v: number }[],
	x: (t: number) => number,
	y: (v: number) => number,
	y0: number
): { line: string; area: string } {
	const pts = run.map((p) => `${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`);
	const line = `M${pts.join('L')}`;
	const first = x(run[0].t).toFixed(1);
	const last = x(run[run.length - 1].t).toFixed(1);
	return { line, area: `${line}L${last},${y0}L${first},${y0}Z` };
}

/** The largest reported value, or undefined when none was. */
export function peak(points: Pt[]): number | undefined {
	let m: number | undefined;
	for (const p of points) if (p.v !== null && (m === undefined || p.v > m)) m = p.v;
	return m;
}

/** The newest reported value. */
export function latest(points: Pt[]): number | undefined {
	for (let i = points.length - 1; i >= 0; i--) if (points[i].v !== null) return points[i].v!;
	return undefined;
}

/** The index of the point nearest t, or -1 for no points. */
export function nearest(points: Pt[], t: number): number {
	let best = -1;
	let dist = Infinity;
	points.forEach((p, i) => {
		const d = Math.abs(p.t - t);
		if (d < dist) {
			dist = d;
			best = i;
		}
	});
	return best;
}

/** A share as a whole percent: "62%", and "<1%" for a share above zero. */
export function pct(v: number): string {
	if (v > 0 && v < 0.005) return '<1%';
	return `${Math.round(v * 100)}%`;
}

/** CPU seconds as "45 min", "2.1 h" or "38 s". */
export function cpuTime(s: number): string {
	if (s >= 3600) return `${(s / 3600).toFixed(1)} h`;
	if (s >= 60) return `${Math.round(s / 60)} min`;
	return `${Math.round(s)} s`;
}
