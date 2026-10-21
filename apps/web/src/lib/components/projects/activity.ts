// Recent activity on /projects (I-837): a run of consecutive events of
// one kind on one project is one row, the newest standing for the run with
// a count of the rest, so five rows say five different things.

export interface ActivityEvent {
	kind: string;
	ts: string;
	project: { id: string };
}

export type Folded<T> = T & { more: number };

/** Folds runs of the same kind on the same project, newest first, keeping the newest. */
export function foldActivity<T extends ActivityEvent>(rows: T[]): Folded<T>[] {
	const out: Folded<T>[] = [];
	for (const r of rows) {
		const last = out[out.length - 1];
		if (last && last.kind === r.kind && last.project.id === r.project.id) {
			last.more += 1;
		} else {
			out.push({ ...r, more: 0 });
		}
	}
	return out;
}
