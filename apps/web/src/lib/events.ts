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

/** The agent hooks' kinds and the three an agent sends itself (I-244, I-688). */
const AGENT_KINDS = [
	'completed',
	'needs_input',
	'error',
	'agent_message',
	'agent_question',
	'agent_done'
];

/** Kinds that say something failed, beyond the *_failed ones. */
const FAILED_KINDS = ['abuse_stopped'];

/**
 * The pager's filters (I-662), as explicit kind lists so the api can
 * filter by them. An agent's error is in both Agents and Errors.
 */
export const EVENT_FILTERS = {
	agents: [...AGENT_KINDS],
	errors: [
		'error',
		'base_update_failed',
		'snapshot_failed',
		'destroy_failed',
		'stop_failed',
		'personal_failed',
		'boot_failed',
		'abuse_stopped',
		'billing_stopped'
	],
	machine: [
		'guest_state_changed',
		'base_updated',
		'snapshot_done',
		'idle_running',
		'temp_expiring',
		'temp_destroyed',
		'host_moved',
		'host_restarted',
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
	guest_state_changed: { label: 'machine', tone: 'neutral' },
	base_updated: { label: 'base', tone: 'neutral' },
	// "unused" is the fixed word for a machine nobody has touched for a
	// day (I-617): eventverbs.go, `repose ls` and the projects list all
	// say it, and this card said "idle", which is an agent's state
	// (I-826).
	idle_running: { label: 'unused', tone: 'attention' },
	temp_expiring: { label: 'expiring', tone: 'attention' },
	temp_destroyed: { label: 'destroyed', tone: 'neutral' },
	host_moved: { label: 'moved', tone: 'neutral' },
	billing_stopped: { label: 'stopped', tone: 'error' },
	'project.created': { label: 'created', tone: 'neutral' },
	// A server restart stopped the machine (I-645); the summary says when
	// the server was back.
	host_restarted: { label: 'server restart', tone: 'attention' },
	notifications_paused: { label: 'paused', tone: 'neutral' }
};

/** The label, tone and group of an event's kind. */
export function eventKind(kind: string, agent?: string): EventKindInfo {
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
