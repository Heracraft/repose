import { describe, expect, it } from 'vitest';
import type { ProjectEvent, Say } from '$lib/api/types';
import { agentVerb, mergeThread, pickWindow, sayLine } from './thread';

const say = (id: string, at: string, s: Partial<Say> = {}): Say => ({
	id,
	project_id: 'p',
	window: 'claude',
	text: `said ${id}`,
	state: 'delivered',
	created_at: at,
	...s
});
const ev = (id: string, ts: string, e: Partial<ProjectEvent> = {}): ProjectEvent =>
	({
		id,
		ts,
		kind: 'completed',
		agent: 'claude',
		window: 'claude',
		summary: `reply ${id}`,
		...e
	}) as ProjectEvent;

describe('mergeThread', () => {
	it('puts both halves in one list, oldest first', () => {
		const t = mergeThread(
			[say('s2', '2026-11-24T10:02:00Z'), say('s1', '2026-11-24T10:00:00Z')],
			[ev('e1', '2026-11-24T10:01:00Z'), ev('e2', '2026-11-24T10:03:00Z')]
		);
		expect(t.map((i) => i.id)).toEqual(['s1', 'e1', 's2', 'e2']);
		expect(t[0]).toMatchObject({ from: 'you', text: 'said s1', state: 'delivered' });
		expect(t[1]).toMatchObject({ from: 'agent', who: 'claude', text: 'reply e1' });
	});

	it("leaves out the machine's own events and kinds that are not an agent speaking", () => {
		const t = mergeThread(
			[],
			[
				ev('e1', '2026-11-24T10:00:00Z', { kind: 'started', agent: undefined, window: undefined }),
				ev('e2', '2026-11-24T10:01:00Z', { kind: 'agent_prompted' }),
				ev('e3', '2026-11-24T10:02:00Z', { kind: 'error', agent: undefined, window: undefined }),
				ev('e4', '2026-11-24T10:03:00Z', { kind: 'agent_question' })
			]
		);
		expect(t.map((i) => i.id)).toEqual(['e4']);
	});

	it('keeps the newest of a long history', () => {
		const says = Array.from({ length: 10 }, (_, i) => say(`s${i}`, `2026-11-24T10:0${i}:00Z`));
		expect(mergeThread(says, [], 3).map((i) => i.id)).toEqual(['s7', 's8', 's9']);
	});
});

describe('sayLine', () => {
	it('says what became of the message, in every state', () => {
		expect(sayLine({ state: 'delivered', window: 'claude' })).toBe('Delivered to claude');
		expect(sayLine({ state: 'waiting', window: 'codex-2' })).toBe(
			'Waiting for codex-2 to finish its turn'
		);
		expect(sayLine({ state: 'replaced', window: 'claude' })).toBe('Replaced by a later message');
		expect(sayLine({ state: 'failed', window: 'claude' })).toBe('Not delivered');
		expect(sayLine({ state: 'sending', window: 'claude' })).toBe('Sending…');
		expect(sayLine({ state: 'delivered', window: '' })).toBe('Delivered to the agent');
	});
});

describe('agentVerb', () => {
	it('marks the kinds that ask for the owner, and nothing on a plain reply', () => {
		expect(agentVerb('completed')).toBe('');
		expect(agentVerb('agent_message')).toBe('');
		expect(agentVerb('agent_question')).toBe('asks');
		expect(agentVerb('needs_input')).toBe('needs input');
	});
});

describe('pickWindow', () => {
	const agents = [
		{ agent: 'claude', window: 'claude', state: 'idle' },
		{ agent: 'codex', window: 'codex-2', state: 'working' }
	];
	it('is undefined with no agent running', () => {
		expect(pickWindow([], [])).toBeUndefined();
	});
	it("takes the owner's pick while that agent still runs", () => {
		expect(pickWindow(agents, [], 'codex-2')).toBe('codex-2');
		expect(pickWindow(agents, [], 'gone')).toBe('claude');
	});
	it('else the agent that spoke last', () => {
		const t = mergeThread(
			[],
			[ev('e1', '2026-11-24T10:00:00Z', { agent: 'codex', window: 'codex-2' })]
		);
		expect(pickWindow(agents, t)).toBe('codex-2');
	});
});
