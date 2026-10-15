import { describe, it, expect } from 'vitest';
import { EVENT_FILTERS, eventKind } from './events';

describe('eventKind', () => {
	it('names the agent kinds', () => {
		// A turn's end is not the work's end (I-688): only agent_done says done.
		expect(eventKind('agent_done', 'claude')).toEqual({
			label: 'done',
			tone: 'ok',
			group: 'agent'
		});
		expect(eventKind('completed', 'claude')).toEqual({
			label: 'turn ended',
			tone: 'neutral',
			group: 'agent'
		});
		expect(eventKind('needs_input', 'codex')).toEqual({
			label: 'needs input',
			tone: 'attention',
			group: 'agent'
		});
		expect(eventKind('agent_question', 'claude').label).toBe('question');
		expect(eventKind('agent_message', 'claude')).toEqual({
			label: 'message',
			tone: 'neutral',
			group: 'agent'
		});
	});
	it('puts an agent error in the error group', () => {
		expect(eventKind('error', 'claude')).toEqual({ label: 'error', tone: 'error', group: 'error' });
	});
	it('marks every failure as failed', () => {
		for (const kind of [
			'boot_failed',
			'snapshot_failed',
			'destroy_failed',
			'stop_failed',
			'base_update_failed',
			'personal_failed',
			'abuse_stopped'
		]) {
			expect(eventKind(kind), kind).toEqual({ label: 'failed', tone: 'error', group: 'error' });
		}
	});
	it('names the machine kinds', () => {
		expect(eventKind('guest_state_changed')).toEqual({
			label: 'machine',
			tone: 'neutral',
			group: 'machine'
		});
		expect(eventKind('snapshot_done').label).toBe('snapshot');
		expect(eventKind('base_updated').label).toBe('base');
		expect(eventKind('idle_running')).toEqual({
			label: 'idle',
			tone: 'attention',
			group: 'machine'
		});
		expect(eventKind('temp_expiring').label).toBe('expiring');
		// The dotted kinds the api writes, as the CLI reads them.
		expect(eventKind('snapshot.created').label).toBe('snapshot');
		expect(eventKind('project.created').label).toBe('created');
		expect(eventKind('host_restarted')).toEqual({
			label: 'server restart',
			tone: 'attention',
			group: 'machine'
		});
	});
	it('shows an unknown kind by its own name, as words', () => {
		expect(eventKind('pool_high')).toEqual({
			label: 'pool high',
			tone: 'neutral',
			group: 'machine'
		});
		expect(eventKind('herdr_down', 'herdr').group).toBe('agent');
	});
});

describe('EVENT_FILTERS', () => {
	it('lists every failure under errors', () => {
		for (const kind of EVENT_FILTERS.errors) {
			expect(eventKind(kind, 'claude').group, kind).toBe('error');
		}
	});
	it('keeps machine kinds out of the agents filter', () => {
		for (const kind of EVENT_FILTERS.machine) {
			expect(EVENT_FILTERS.agents as readonly string[], kind).not.toContain(kind);
			expect(eventKind(kind).group, kind).toBe('machine');
		}
	});
	it('lists the agent kinds', () => {
		expect([...EVENT_FILTERS.agents].sort()).toEqual(
			['agent_done', 'agent_message', 'agent_question', 'completed', 'error', 'needs_input'].sort()
		);
	});
});
