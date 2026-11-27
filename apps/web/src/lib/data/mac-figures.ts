/*
 * The Mac figures of the landing's "03 Your hardware" band (DECISIONS I-1674).
 * Nothing in MacFigures.svelte is typed in: a bar's length, its value, the
 * screen reader's sentence and the notes all come from here, so a better
 * measurement is one edit to one number.
 *
 * `own` is design 2: each sandbox its own Virtualization.framework VM on
 * macOS, hostd native (branch design2). `nested` is repose 0.1.66 on the
 * same Mac: every sandbox inside one Linux VM (the appliance), Cloud
 * Hypervisor nested in it. Both were driven through the production api from
 * woker-1 as a customer would, with the CLI of their branch.
 *
 * Rules for an update: a figure is measured, never estimated; it keeps its
 * range (lo..hi) and n; its comment names who measured it, when (UTC) and
 * where the raw data is. Compare like with like, or say what differs in the
 * chart's `note`. When a number moves, change it here and in its comment;
 * the axis (`max`) only when a bar would overrun it (mac-figures.test.ts
 * fails then, and when a title's claim stops being true of the numbers).
 */

export interface Range {
	/** In the chart's unit. */
	lo: number;
	hi: number;
	/** Runs behind the range. */
	n: number;
}

export interface Row {
	label: string;
	own: Range;
	nested: Range;
	/** Words under the own figure's value, if any ("memory kept"). */
	ownSays?: string;
	nestedSays?: string;
}

export interface Chart {
	unit: 's' | 'GB';
	/** The axis's end, in the unit; ticks every `step`. */
	max: number;
	step: number;
	rows: Row[];
	/** What differs between the two runs, or a condition, said on the page. */
	note?: string;
}

/**
 * The switch. The Mac figures below are design 2's, a prototype on branch
 * design2; repose 0.1.66 on a Mac is the nested layout they beat. While
 * design 2 is not shipped they stay off the page (no ghost features), and
 * the band "03 Your hardware" says only what ships today. The day design 2
 * ships, and its public docs say what these figures say, set this to true:
 * the band then adds "And on a Mac, each sandbox is its own VM" with the
 * four charts. Nothing else changes.
 */
export const MAC_DESIGN2_LIVE = false;

/** Where and when every figure below was measured. */
export const MEASURED = {
	// AWS mac-m4.metal (Apple M4 Mac mini hardware), eu-west-1b, 10 cores,
	// 24 GiB, macOS 15.8.3 24H241: docs/reports/2026-11-26-design2-ux.md
	// "What was measured", docs/proposals/2026-11-26-design2.md. The start,
	// snapshot, restore, rest and restart rows were re-measured on 27
	// November (d2-lab, design2 66ee08f2, report section "Re-check on
	// d2-runtime-5"); the new-sandbox and stopped-memory rows are from 26
	// November (design2 d62bdf73), a path d2-runtime-5 did not change.
	machine: 'an M4 Mac mini with 10 cores and 24 GB',
	date: '27 November 2026',
	iso: '2026-11-27'
} as const;

/** The two ways of running sandboxes on one Mac, as the legend names them. */
export const LAYOUTS = {
	own: 'Each sandbox its own VM',
	nested: 'Every sandbox inside one Linux VM',
	nestedVersion: 'repose 0.1.66'
} as const;

/** 3.1 Start: from the command to the sandbox answering. */
export const START: Chart = {
	unit: 's',
	max: 35,
	step: 5,
	rows: [
		{
			label: 'New sandbox from your repo',
			// own: warm create, `repose run -d` from a real checkout to the
			// first `ssh SLUG.repose true`, the same three repos as the nested
			// runs: 12.2-33.9 s, n=6, 5 of 6 in 12.2-18.8 s, median 16.2 (d2-lab,
			// design2 d62bdf73, 2026-11-26 20:56-21:03Z; report section 2,
			// create.csv in /mnt/nixstore/sharing/d2-lab3/data/). The 33.9 s
			// run is in the note.
			own: { lo: 12.2, hi: 33.9, n: 6 },
			// nested: warm create, a further machine on a running appliance,
			// `run -d` to the first `ssh SLUG.repose true`: 19.9, 20.9, 23.1 s
			// (d2-lab, 12:57Z and 15:13Z; report section 2, create.csv).
			nested: { lo: 19.9, hi: 23.1, n: 3 }
		},
		{
			label: 'Start a stopped one',
			// own: `repose start`, command returns: 3.79-5.40 s, n=6 (4.70-5.40
			// then 3.79-4.06; to the first ssh 4.61-6.16 s) (d2-lab, design2
			// 66ee08f2, 2026-11-27 00:36-00:38Z; report "Re-check on
			// d2-runtime-5", /mnt/nixstore/sharing/d2-lab4/data/).
			own: { lo: 3.8, hi: 5.4, n: 6 },
			// nested: `repose start`, command returns: 10.3-13.5 s, n=6
			// (d2-lab 12:59-13:01Z; report section 3, stopstart.csv).
			nested: { lo: 10.3, hi: 13.5, n: 6 }
		}
	],
	note: 'One new sandbox of six took 34 s; the other five 12.2–18.8 s.'
};

/** 3.2 Snapshots: taking one of a running sandbox, and restoring one. */
export const SNAPSHOT: Chart = {
	unit: 's',
	max: 90,
	step: 30,
	rows: [
		{
			label: 'Take a snapshot',
			// own: `repose snapshots create` on a running machine with 3.9 GB in
			// use, 1.1 GB stored each: 13.1, 10.9, 10.7 s, n=3 (d2-lab, e2e-unwrap,
			// design2 66ee08f2, 2026-11-27 00:48Z; report "Re-check on
			// d2-runtime-5"). The skeleton's 0.7 s was a nearly empty machine;
			// the time follows the memory and data written, not the clone.
			own: { lo: 10.7, hi: 13.1, n: 3 },
			// nested: 16.3-25.4 s, n=3, a full copy, 1.5 GB stored each, on a
			// machine holding a 500 MB file (d2-lab 14:19Z, snap.csv).
			nested: { lo: 16.3, hi: 25.4, n: 3 }
		},
		{
			label: 'Restore one',
			// own: `repose snapshots restore -y` in place, to the command's
			// return: 4.41-5.12 s, n=3; to the file reading back over ssh
			// 5.31-6.02 s (d2-lab, e2e-unwrap, design2 66ee08f2, 2026-11-27
			// 00:48-00:49Z; report "Re-check on d2-runtime-5").
			own: { lo: 4.4, hi: 5.1, n: 3 },
			// nested: the same command in place, to its return: 80.9, 66.9,
			// 66.0 s, n=3 (d2-lab 14:20-14:23Z, snap.csv; the report's 67-83 s
			// is to the 500 MB file reading back over ssh).
			nested: { lo: 66.0, hi: 80.9, n: 3 }
		}
	],
	note: 'Each own-VM snapshot stored 1.1 GB of a sandbox using 3.9 GB; each one inside the Linux VM stored 1.5 GB.'
};

/** 3.3 Memory: what a stopped sandbox, and an idle one, do with the Mac's memory. */
export const REST = {
	// An idle sandbox rests by pause on design2 66ee08f2 (d2-runtime-5; decision on branch design2):
	// its VM process and memory stay, and it is saved to disk (memory back to
	// macOS) only when macOS is under 25 percent free, which did not happen in
	// the measured runs (75 percent free), so no save time is on the page.
	// Three idle machines held 11.9 GB while resting (0.82, 4.11, 7.00 GB;
	// d2-lab 00:49-01:05Z). Whether the product rests by pause or by save is
	// open (board ENG-266); the copy says what 66ee08f2 does.
	// wake: the first `ssh SLUG.repose true` of a rested machine, 0.91-0.94 s,
	// n=3 (d2-lab 2026-11-27 01:05:59-01:06:01Z, hostd wake_ms 22-27; report
	// "Re-check on d2-runtime-5").
	wake: { lo: 0.91, hi: 0.94, n: 3 } satisfies Range,
	chart: {
		unit: 'GB',
		max: 20,
		step: 5,
		rows: [
			{
				label: 'Memory held once stopped',
				// own: 0, each VM process ends at its stop: three machines' 2.82
				// GB gone by the next 5 s sample (d2-lab, design2 d62bdf73,
				// 2026-11-26 23:41:48-23:41:52Z, report section 8, mac.csv in
				// d2-lab3); d2-runtime-5 did not change the stop path.
				own: { lo: 0, hi: 0, n: 1 },
				// nested: the appliance (the whole Linux VM) held 7.63 GB 4 min
				// after every machine was stopped (14:50-14:54Z), and its whole
				// 18 GB after builds (13:19Z), never returned while it runs
				// (d2-lab, report section 8 and finding 3, mac.csv).
				nested: { lo: 7.63, hi: 7.63, n: 1 }
			}
		],
		note: 'For the Linux VM, 4 minutes after every sandbox in it stopped; it gives nothing back while it runs.'
	} as Chart
};

/** 3.4 Restart: a Mac restart, from the command to the sandboxes back. */
export const RESTART: Chart = {
	unit: 's',
	max: 75,
	step: 15,
	rows: [
		{
			label: 'Restart to sandboxes back',
			// own: `repose host restart -y` with three running machines, from
			// the command to all three answering ssh, same boot_ids: 60.5 s,
			// n=1 (parked 3 in 9.0 s; hostd "parked guests restored" at 67.5 s)
			// (d2-lab, design2 66ee08f2, 2026-11-27 01:06Z; report "Re-check on
			// d2-runtime-5"). The next two runs (01:10Z, 01:14Z) were refused,
			// cause no_room: after a restore the park room counts each
			// machine's whole configured memory (21.3 GB needed, ~17.7 GB
			// room); nothing was lost (board ENG-263). The note says so.
			own: { lo: 60.5, hi: 60.5, n: 1 },
			ownSays: 'memory kept',
			// nested: `sudo shutdown -r now` with the user logged in, to every
			// machine answering ssh: 51-65 s and 59-72 s, n=2 restarts, 6
			// machines, each with a new boot_id; the Apple menu path ~57-61 s;
			// 0 of 6 kept memory on any path (d2-lab, report section 11).
			nested: { lo: 51, hi: 72, n: 2 },
			nestedSays: 'memory lost'
		}
	],
	// I-1662: a restart repose does not start (Apple menu, a macOS update, a
	// power loss) ends every VZ VM at its first signal, so memory is lost
	// there in design 2 too. The design-2 park decision (on branch design2,
	// not yet on main): parked sandboxes are restored once the user's
	// session is unlocked; the restarts above ran with autologin.
	note: 'With three sandboxes, two of three tries were refused for want of disk room, and nothing was lost. A restart from the Apple menu, a macOS update or a power cut still loses memory. Timed to every sandbox answering ssh.'
};

function num(v: number, unit: Chart['unit']): string {
	if (unit === 'GB') return v === 0 ? '0' : v.toFixed(1);
	return v >= 30 ? String(Math.round(v)) : v.toFixed(1);
}

/** "6.4–6.8 s", "43 s", "0.7 s", "7.6 GB": a range as the page prints it. */
export function figure(r: Range, unit: Chart['unit'] = 's'): string {
	const lo = num(r.lo, unit);
	const hi = num(r.hi, unit);
	return lo === hi ? `${lo} ${unit}` : `${lo}–${hi} ${unit}`;
}

/** The same, for seconds. */
export const seconds = (r: Range) => figure(r, 's');

/** A bar's two lengths on its chart, in percent: solid to lo, light to hi. */
export function bar(r: Range, max: number): { lo: number; hi: number } {
	const pct = (v: number) => Math.min(100, Math.max(0, (v / max) * 100));
	return { lo: pct(r.lo), hi: pct(r.hi) };
}

/** The axis's ticks, 0 to max. */
export function ticks(c: Chart): number[] {
	const out: number[] = [];
	for (let t = 0; t <= c.max; t += c.step) out.push(t);
	return out;
}
