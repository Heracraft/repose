// How the dashboard names and groups a project's events (api.md
// "Events"; the kinds are docs/features/notifications.md's, and the
// guest_state_changed the api writes on every start and stop). A kind the
// dashboard has not heard of shows its own name, so a new one is never
// hidden; it falls in the machine group until it is added here.

export type EventTone = 'error' | 'attention' | 'ok' | 'neutral';
export type EventGroup = 'agent' | 'machine' | 'error';

export interface EventKindInfo {
	/** A word or two for the event's tag: "done", "needs input". */
	label: string;
	/** error is red, attention amber (waiting on you), ok emerald, neutral grey. */
	tone: EventTone;
	group: EventGroup;
}

/**
 * The agent hooks' kinds, the three an agent sends itself (I-244, I-688)
 * and the record that a `repose say` reached the agent (I-1144).
 */
const AGENT_KINDS = [
	'completed',
	'needs_input',
	'error',
	'agent_message',
	'agent_question',
	'agent_done',
	'agent_prompted'
];

/** Kinds that say something failed, beyond the *_failed ones. */
const FAILED_KINDS = ['abuse_stopped'];

/**
 * Why an event notified nobody, in a few words, or undefined when it went
 * out, when it is still going out, or when the kind queues no delivery
 * (I-1280). An event that reached a channel says nothing: the answer is
 * wanted for the ping that never came, not for every row.
 */
export function notSent(delivered?: Record<string, string>): string | undefined {
	const outcomes = Object.values(delivered ?? {});
	if (outcomes.length === 0) return undefined;
	let held = false;
	let dropped = false;
	for (const v of outcomes) {
		if (v === 'held') held = true;
		else if (v.startsWith('dropped')) dropped = true;
		else return undefined; // a send time, or a failure of its own
	}
	if (held) return 'not sent yet: you are at the machine';
	if (dropped) return 'not sent: it was over by then';
	return undefined;
}

/**
 * The pager's filters (I-662), as explicit kind lists so the api can
 * filter by them. An agent's error is in both Agents and Errors.
 */
export const EVENT_FILTERS = {
	agents: [...AGENT_KINDS],
	errors: [
		'error',
		'trigger_failed',
		'base_update_failed',
		'snapshot_failed',
		'destroy_failed',
		'stop_failed',
		'personal_failed',
		'boot_failed',
		'abuse_stopped',
		'oom_stopped',
		'billing_stopped'
	],
	machine: [
		'guest_state_changed',
		// A trigger's run is the machine working without the user, which
		// is what this filter is for; a failed one is under Errors too
		// (I-1256).
		'trigger_run',
		'base_updated',
		'snapshot_done',
		'idle_running',
		'temp_expiring',
		'temp_destroyed',
		'host_moved',
		'host_restarted',
		// Its guestd stopped answering for about three minutes (I-1652).
		'not_answering',
		'snapshot.created',
		'project.created'
	]
} as const satisfies Record<string, readonly string[]>;

export type EventFilter = keyof typeof EVENT_FILTERS;

const LABELS: Record<string, { label: string; tone: EventTone }> = {
	// A turn's end, which notifies nobody; the agent says it is done with
	// repose-notify --done (I-688).
	completed: { label: 'turn ended', tone: 'neutral' },
	agent_done: { label: 'done', tone: 'ok' },
	needs_input: { label: 'needs input', tone: 'attention' },
	error: { label: 'error', tone: 'error' },
	agent_question: { label: 'question', tone: 'attention' },
	agent_message: { label: 'message', tone: 'neutral' },
	// A message the owner sent with `repose say`, typed into the agent's
	// terminal (I-1144). The summary is who sent it, never the message.
	agent_prompted: { label: 'got a message', tone: 'neutral' },
	guest_state_changed: { label: 'machine', tone: 'neutral' },
	base_updated: { label: 'base', tone: 'neutral' },
	// "unused" is the fixed word for a machine nobody has touched for a
	// day (I-617): eventverbs.go, `repose ls` and the projects list all
	// say it, and this card said "idle", which is an agent's state
	// (I-826).
	// A trigger's run. The summary is the agent's own `--done` words
	// where it sent any, else the reason in the words of
	// features/triggers.md §4.1.
	trigger_run: { label: 'trigger', tone: 'neutral' },
	trigger_failed: { label: 'trigger failed', tone: 'error' },
	idle_running: { label: 'unused', tone: 'attention' },
	temp_expiring: { label: 'expiring', tone: 'attention' },
	temp_destroyed: { label: 'destroyed', tone: 'neutral' },
	host_moved: { label: 'moved', tone: 'neutral' },
	billing_stopped: { label: 'stopped', tone: 'error' },
	'project.created': { label: 'created', tone: 'neutral' },
	// A server restart stopped the machine (I-645); the summary says when
	// the server was back.
	host_restarted: { label: 'server restart', tone: 'attention' },
	// A running machine that has not answered for about three minutes
	// (I-1652): the summary says how long and what `repose start` costs.
	not_answering: { label: 'not answering', tone: 'attention' },
	notifications_paused: { label: 'paused', tone: 'neutral' }
};

/** The label, tone and group of an event's kind. */
export function eventKind(kind: string, agent?: string): EventKindInfo {
	if (kind === 'oom_stopped') {
		// repose stopped it so the others keep running (I-1650): the
		// CLI's words (eventverbs), amber, still under Errors. It read
		// "failed" in red (pass2-dash N6, I-1658).
		return { label: 'stopped, host out of memory', tone: 'attention', group: 'error' };
	}
	if (kind.endsWith('_failed') || FAILED_KINDS.includes(kind)) {
		return { label: 'failed', tone: 'error', group: 'error' };
	}
	// The api writes some kinds dotted (snapshot.created, project.created),
	// as the CLI's eventverbs reads them.
	if (kind.startsWith('snapshot_') || kind.startsWith('snapshot.')) {
		return { label: 'snapshot', tone: 'neutral', group: 'machine' };
	}
	const known = LABELS[kind];
	if (known) {
		const group: EventGroup =
			known.tone === 'error' ? 'error' : AGENT_KINDS.includes(kind) ? 'agent' : 'machine';
		return { ...known, group };
	}
	// An unknown kind reads as words, as the CLI's eventverbs.Verb does.
	return { label: kind.replace(/[._]/g, ' '), tone: 'neutral', group: agent ? 'agent' : 'machine' };
}

/** The kit dot class for a kind's 6px cue: amber for a question or input
 * asked, red for an error, grey otherwise. The project's Events list and
 * /projects' Recent activity draw the same cue from it. */
export function toneDot(tone: EventTone): string {
	return tone === 'error' ? 'hk-dot--err' : tone === 'attention' ? 'hk-dot--warn' : '';
}
