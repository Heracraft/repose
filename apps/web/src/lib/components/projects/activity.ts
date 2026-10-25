// Recent activity on /projects (I-837): a run of consecutive events of
// one kind on one project is one row, the newest standing for the run with
// a count of the rest, so five rows say five different things. A question
// or a failure is never folded: each asks something of the reader that the
// row before it does not.

export interface ActivityEvent {
	kind: string;
	ts: string;
	project: { id: string };
}

export type Folded<T> = T & { more: number };

const OWN_ROW = new Set(['needs_input', 'agent_question', 'error', 'billing_stopped']);

/** A kind whose every event keeps its own row. */
function ownRow(kind: string): boolean {
	return OWN_ROW.has(kind) || kind.endsWith('_failed');
}

/** Folds runs of the same kind on the same project, newest first, keeping the newest; questions and failures stay apart. */
export function foldActivity<T extends ActivityEvent>(rows: T[]): Folded<T>[] {
	const out: Folded<T>[] = [];
	for (const r of rows) {
		const last = out[out.length - 1];
		if (last && last.kind === r.kind && last.project.id === r.project.id && !ownRow(r.kind)) {
			last.more += 1;
		} else {
			out.push({ ...r, more: 0 });
		}
	}
	return out;
}
