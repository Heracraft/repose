// How the dashboard lays out and words the questions agents wait on
// (I-693): the "Waiting on you" card on /projects and on a project page
// draws them with one QuestionRow, from these functions, so the two pages
// cannot word the same fact two ways again.
import { ageShort, minutesLeft } from './format';
import type { Question, QuestionVia } from './api/types';

/** The expiry shows only once this little time is left. */
export const EXPIRY_SHOWN_MS = 10 * 60_000;

/** How long an answered row stays, as one line, before it leaves. */
export const CLOSED_LINE_MS = 8_000;

/** Where an answer came from, as the end of "Answered yes …". An answer
 *  from the dashboard says nothing more: the reader is on it. */
const FROM: Partial<Record<QuestionVia, string>> = {
	cli: 'from the CLI',
	email: 'by email',
	ntfy: 'in ntfy',
	gchat: 'in Google Chat'
};

export interface ClosedLine {
	/** The answer, which the row sets in ink; absent when there is none to name. */
	answer?: string;
	/** The whole line, answer included, for the row and the live region. */
	text: string;
	/** answered draws the emerald square; closed the hollow one. */
	tone: 'answered' | 'closed';
}

/** The one line a closed question collapses to: what ended it, and where. */
export function closedLine(q: Question): ClosedLine {
	const via = q.answered_via ?? undefined;
	if (q.state === 'answered') {
		if (via === 'terminal' || q.answer === null) {
			return { text: 'Answered in the terminal', tone: 'answered' };
		}
		const from = via ? FROM[via] : undefined;
		return {
			answer: q.answer,
			text: from ? `Answered ${q.answer} ${from}` : `Answered ${q.answer}`,
			tone: 'answered'
		};
	}
	if (q.state === 'cancelled') {
		if (via === 'agent') return { text: 'Withdrawn by the agent', tone: 'closed' };
		// The api's Cancel stores dashboard (Dismiss) or cli (`repose reply
		// --dismiss`); a close with no source (asker_gone,
		// interrupted, a machine stop) is not the owner's doing.
		if (via === 'dashboard' || via === 'cli') return { text: 'Dismissed', tone: 'closed' };
		return { text: 'The agent stopped waiting', tone: 'closed' };
	}
	if (q.state === 'expired') return { text: 'Expired', tone: 'closed' };
	return { text: 'Closed', tone: 'closed' };
}

/** "asked 3m ago", on the shared formatter. */
export function askedLine(q: Question, now: number): string {
	return `asked ${ageShort(q.created_at, now)}`;
}

/** "expires in 4m" in the question's last 10 minutes; "" before that and after. */
export function expiryLine(q: Question, now: number): string {
	const ms = new Date(q.expires_at).getTime() - now;
	if (ms <= 0 || ms > EXPIRY_SHOWN_MS) return '';
	return `expires in ${minutesLeft(q.expires_at, now)}m`;
}

export interface QuestionRowLayout {
	question: Question;
	/** Older open questions from the same agent window, newest first, folded under this one. */
	folded: Question[];
}

export interface ProjectQuestions {
	project_id: string;
	project: string;
	/** Open questions in the group, folded ones included. */
	pending: number;
	rows: QuestionRowLayout[];
}

const windowKey = (q: Question) => `${q.project_id}\u0000${q.agent}\u0000${q.window ?? ''}`;
const time = (iso: string) => new Date(iso).getTime();

/**
 * Groups questions by project, the project that has waited longest
 * first, each group's rows newest first. An open question is folded under
 * a newer open one from the same agent and window: the agent asked again,
 * so the older one is most likely stale. A closed question keeps its own
 * row, in its place, while its line shows.
 */
export function layoutQuestions(questions: Question[]): ProjectQuestions[] {
	// Ties go by id, so a row held while it closes (WaitingCard) keeps its
	// place, and its focus, when it reaches the layout in another order.
	const sorted = [...questions].sort(
		(a, b) => time(b.created_at) - time(a.created_at) || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0)
	);
	const groups = new Map<string, ProjectQuestions & { oldest: number }>();
	const lead = new Map<string, QuestionRowLayout>();
	for (const q of sorted) {
		let g = groups.get(q.project_id);
		if (!g) {
			g = { project_id: q.project_id, project: q.project, pending: 0, rows: [], oldest: Infinity };
			groups.set(q.project_id, g);
		}
		if (q.state !== 'pending') {
			g.rows.push({ question: q, folded: [] });
			continue;
		}
		g.pending++;
		g.oldest = Math.min(g.oldest, time(q.created_at));
		const first = lead.get(windowKey(q));
		if (first) {
			first.folded.push(q);
		} else {
			const row = { question: q, folded: [] };
			lead.set(windowKey(q), row);
			g.rows.push(row);
		}
	}
	return [...groups.values()]
		.sort((a, b) => a.oldest - b.oldest)
		.map((g) => ({
			project_id: g.project_id,
			project: g.project,
			pending: g.pending,
			rows: g.rows
		}));
}
