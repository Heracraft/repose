/*
 * Every figure the landing's "03 Your Mac" band draws (DECISIONS I-1674).
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

/** Where and when every figure below was measured. */
export const MEASURED = {
	// AWS mac-m4.metal (Apple M4 Mac mini hardware), eu-west-1b, 10 cores,
	// 24 GiB, macOS 15.8.3 24H241: docs/reports/2026-11-26-design2-ux.md
	// "What was measured", docs/proposals/2026-11-26-design2.md.
	machine: 'an M4 Mac mini with 10 cores and 24 GB',
	date: '26 November 2026',
	iso: '2026-11-26'
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
	max: 25,
	step: 5,
	rows: [
		{
			label: 'New sandbox from your repo',
			// own: `repose run` create from a real checkout (sync included) to
			// "Ready in 12s": 11.9 s (e2e-j1, 16:04Z, design2 a4e25773) and
			// 12.0 s (e2e-j2, 16:41Z, design2 f9d660b6); d2-arch's journeys,
			// ~/briefs/design2-program.log, j1.log and j2.log in
			// /mnt/nixstore/sharing/d2-arch/. Its checkout is not one of the
			// three repos the nested runs used.
			own: { lo: 11.9, hi: 12.0, n: 2 },
			// nested: warm create, a further machine on a running appliance,
			// `run -d` to the first `ssh SLUG.repose true`: 19.9, 20.9, 23.1 s
			// (d2-lab, 12:57Z and 15:13Z; report section 2, create.csv).
			nested: { lo: 19.9, hi: 23.1, n: 3 }
		},
		{
			label: 'Start a stopped one',
			// own: `repose start` to its "ready in" line: 6.8 s (j1, 16:04Z)
			// and 6.4 s (j2, 16:41Z), d2-arch as above, network and edge in.
			own: { lo: 6.4, hi: 6.8, n: 2 },
			// nested: `repose start`, command returns: 10.3-13.5 s, n=6
			// (d2-lab 12:59-13:01Z; report section 3, stopstart.csv).
			nested: { lo: 10.3, hi: 13.5, n: 6 }
		}
	]
};

/** 3.2 Snapshots: taking one of a running sandbox, and restoring one. */
export const SNAPSHOT: Chart = {
	unit: 's',
	max: 90,
	step: 30,
	rows: [
		{
			label: 'Take a snapshot',
			// own: `repose snapshots create` on a running machine, 0.69-0.72 s,
			// n=3 (d2-lab 15:46Z, skeleton build 3c23e89e, e2e-d2n-pm, a 4.2 MB
			// snapshot with no checkout; report section 10, d2n.csv; d2-runtime
			// 0.7 s at 15:31Z). An APFS clone of a 2.2 GB volume takes 26 ms
			// (proposal section 1), so the time does not follow the data; the
			// note says the sandbox was nearly empty.
			own: { lo: 0.69, hi: 0.72, n: 3 },
			// nested: 16.3-25.4 s, n=3, a full copy, 1.5 GB stored each, on a
			// machine holding a 500 MB file (d2-lab 14:19Z, snap.csv).
			nested: { lo: 16.3, hi: 25.4, n: 3 }
		},
		{
			label: 'Restore one',
			// own: `repose snapshots restore -y` in place, to the command's
			// "it is running": 8.8-9.1 s (j1, 16:04Z) and 9.2 s (j2, 16:41Z),
			// d2-arch; the file written after the snapshot is gone.
			own: { lo: 8.8, hi: 9.2, n: 3 },
			// nested: the same command in place, to its return: 80.9, 66.9,
			// 66.0 s, n=3 (d2-lab 14:20-14:23Z, snap.csv; the report's 67-83 s
			// is to the 500 MB file reading back over ssh).
			nested: { lo: 66.0, hi: 80.9, n: 3 }
		}
	],
	note: 'The sandbox inside one Linux VM held a 500 MB file and the other almost nothing; an APFS clone of 2.2 GB takes 26 ms.'
};

/** 3.3 Memory: what an idle sandbox does with the Mac's memory. */
export const REST = {
	// own: idle rest by save (d2-runtime 16:17Z, d2rt-cite, a 6 GiB machine
	// with a 512 MB fill, n=3): the paused VM is saved in 2.42-3.36 s and its
	// process ends, all its memory back to macOS; the first `repose exec`
	// through the production gateway wakes it and returns in 2.81-2.86 s,
	// the fill's sha256 unchanged every time. Plus d2-arch 17:00Z (e2e-j2 on
	// design2 f9d660b6, after 15 min idle): save 1.81 s, a 220 MB image, its
	// VM process gone; the waking exec's own time was not logged, so `wake`
	// stays d2-runtime's three.
	save: { lo: 1.81, hi: 3.36, n: 4 } satisfies Range,
	wake: { lo: 2.81, hi: 2.86, n: 3 } satisfies Range,
	// Condition (d2-runtime 15:52Z and f9d660b6): the rest saves only when
	// the sandbox runs in the Mac user's unlocked session, since a restore
	// needs it unlocked (I-1662); otherwise the sandbox is paused and keeps
	// its memory. Said in the cell's line. Remove the words the day the
	// shipped design rests otherwise.
	unlocked: true,
	chart: {
		unit: 'GB',
		max: 20,
		step: 5,
		rows: [
			{
				label: 'Memory held once the sandboxes are idle',
				// own: 0, the rested VM's process is gone (d2-runtime 16:17Z n=3,
				// d2-arch 17:00Z n=1).
				own: { lo: 0, hi: 0, n: 4 },
				// nested: the appliance (the whole Linux VM) held 7.63 GB 4 min
				// after every machine was stopped (14:50-14:54Z), 4.1 GB with
				// three idle machines after a fresh boot (14:47Z), and its whole
				// 18 GB after builds (13:19Z), never returned while it runs
				// (d2-lab, report section 8 and finding 3, mac.csv). The page
				// shows the after-work figure.
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
			// own: `repose host restart`, from the command to hostd's "parked
			// guests restored", same boot_ids: 43 s (16:14:26Z to 16:15:09Z,
			// d2rt-cite; e2e-j1 could not park and came back fresh under
			// --force; d2-runtime 16:15Z) and 50 s (17:03:23Z to 17:04:13Z,
			// 3 of 3 machines parked in 5.4 s and restored; d2-arch,
			// /mnt/nixstore/sharing/d2-arch/j2hr.log). An exec through the
			// gateway answered 1.37 s after a restore (16:15Z). Earlier, with
			// 19.5 GB machines, `hostd park` + shutdown -r: back 58 s after
			// the command (15:37:43Z to 15:38:41Z), before the 6 GiB default.
			own: { lo: 43, hi: 50, n: 2 },
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
	// there in design 2 too. I-1666: parked sandboxes are restored once the
	// user's session is unlocked; the restarts above ran with autologin.
	note: 'A restart from the Apple menu, a macOS update or a power cut still loses memory. Timed to the sandboxes restored, and for the Linux VM to ssh answering.'
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
