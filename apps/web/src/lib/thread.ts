// The conversation on a project's page (DECISIONS I-1604): the messages
// the owner sent (`GET /projects/:id/says`) and what the agents said
// back (their events), in one list, oldest first. A leaf module, so the
// merge and the words are tested without a page.
import type { AgentSignal, ProjectEvent, Say } from '$lib/api/types';

/**
 * The event kinds that are an agent speaking: the end of a turn with its
 * last message (`completed`, the agent's own Stop hook), `repose-notify`
 * (`agent_message`, and `agent_done` with --done), a `repose-ask`
 * question, a permission prompt (`needs_input`) and an agent's error.
 * `agent_prompted` is not one: it is the machine saying a message went
 * in, which the message's own state already says.
 */
export const AGENT_KINDS = [
	'completed',
	'agent_message',
	'agent_done',
	'agent_question',
	'needs_input',
	'error'
] as const;

/** How many of each half one read asks for. */
export const THREAD_READ = 30;
/** Items drawn before "Earlier messages". */
export const THREAD_SHOWN = 12;

/** A message's state as the thread shows it: the say's, or `sending`. */
export type SayState = Say['state'] | 'sending';

export type ThreadItem =
	| {
			from: 'you';
			id: string;
			at: string;
			text: string;
			window: string;
			/** The say's state, or `sending` while the POST is out. */
			state: SayState;
	  }
	| {
			from: 'agent';
			id: string;
			at: string;
			/** The agent's name, or its window when the event has no agent. */
			who: string;
			window: string;
			kind: string;
			text: string;
	  };

/**
 * One list from the two reads, oldest first, the newest `cap` of them.
 * An event with no agent and no window is the machine's or the api's
 * own (a stop, a start), not an agent's, and stays out.
 */
export function mergeThread(
	says: Say[],
	events: ProjectEvent[],
	cap = THREAD_READ * 2
): ThreadItem[] {
	const items: ThreadItem[] = [
		...says.map(
			(s): ThreadItem => ({
				from: 'you',
				id: s.id,
				at: s.created_at,
				text: s.text,
				window: s.window,
				state: s.state
			})
		),
		...events
			.filter((e) => (AGENT_KINDS as readonly string[]).includes(e.kind) && (e.agent || e.window))
			.map(
				(e): ThreadItem => ({
					from: 'agent',
					id: e.id,
					at: e.ts,
					who: e.agent || e.window || 'agent',
					window: e.window ?? '',
					kind: e.kind,
					text: e.summary
				})
			)
	];
	items.sort((a, b) => (a.at < b.at ? -1 : a.at > b.at ? 1 : a.id < b.id ? -1 : 1));
	return items.slice(-cap);
}

/** The line under one of the owner's messages: what became of it. */
export function sayLine(item: { state: SayState; window: string }): string {
	const who = item.window || 'the agent';
	switch (item.state) {
		case 'sending':
			return 'Sending…';
		case 'waiting':
			return `Waiting for ${who} to finish its turn`;
		case 'replaced':
			return 'Replaced by a later message';
		case 'failed':
			return 'Not delivered';
		default:
			return `Delivered to ${who}`;
	}
}

/** The few words an agent's item opens with, by kind; '' for a plain reply. */
export function agentVerb(kind: string): string {
	switch (kind) {
		case 'agent_question':
			return 'asks';
		case 'needs_input':
			return 'needs input';
		case 'agent_done':
			return 'done';
		case 'error':
			return 'error';
		default:
			return '';
	}
}

/**
 * The window a message goes to: the one the owner picked while it still
 * runs, else the agent that spoke last, else the first running. undefined
 * when no agent runs.
 */
export function pickWindow(
	agents: AgentSignal[],
	thread: ThreadItem[],
	chosen?: string
): string | undefined {
	const windows = agents.map((a) => a.window || a.agent);
	if (windows.length === 0) return undefined;
	if (chosen && windows.includes(chosen)) return chosen;
	for (let i = thread.length - 1; i >= 0; i--) {
		const it = thread[i];
		const w = it.from === 'agent' ? it.window || it.who : it.window;
		if (w && windows.includes(w)) return w;
	}
	return windows[0];
}
