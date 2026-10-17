import { describe, expect, it } from 'vitest';
import type { Project } from '$lib/api/types';
import { busyAgents, joinNames, stopWarning } from './busyAgents';

function p(state: Project['state'], agents: { agent: string; window: string; state: string }[]) {
	return { slug: 'api', state, signals: { agents } } as unknown as Project;
}

describe('busyAgents', () => {
	it('counts an agent mid-turn and one waiting on an answer, and nothing else', () => {
		expect(busyAgents(p('running', [{ agent: 'claude', window: '', state: 'working' }]))).toEqual([
			'claude (working)'
		]);
		expect(
			busyAgents(p('running', [{ agent: 'claude', window: '', state: 'needs_input' }]))
		).toEqual(['claude (needs input)']);
		// internal/cli/lifecycle.go's busyAgentList skips every other state.
		for (const state of ['idle', 'running', 'unknown', '']) {
			expect(busyAgents(p('running', [{ agent: 'claude', window: '', state }]))).toEqual([]);
		}
	});

	it('names an agent by its window, falling back to the agent', () => {
		expect(
			busyAgents(p('running', [{ agent: 'codex', window: 'codex-2', state: 'needs_input' }]))
		).toEqual(['codex-2 (needs input)']);
	});

	it('has nothing to interrupt on a machine that is not running', () => {
		expect(busyAgents(p('stopped', [{ agent: 'claude', window: '', state: 'working' }]))).toEqual(
			[]
		);
		expect(busyAgents(undefined)).toEqual([]);
	});
});

describe('joinNames', () => {
	it('joins as the CLI does', () => {
		expect(joinNames([])).toBe('');
		expect(joinNames(['a'])).toBe('a');
		expect(joinNames(['a', 'b'])).toBe('a and b');
		expect(joinNames(['a', 'b', 'c'])).toBe('a, b and c');
	});
});

describe('stopWarning', () => {
	// The sentence internal/cli/confirm_test.go:154 pins for the CLI.
	it('is the CLI’s sentence, for one agent and for several', () => {
		expect(stopWarning(p('running', [{ agent: 'claude', window: '', state: 'working' }]))).toBe(
			'api has claude (working). Stopping ends it.'
		);
		expect(
			stopWarning(
				p('running', [
					{ agent: 'claude', window: '', state: 'working' },
					{ agent: 'codex', window: 'codex-2', state: 'needs_input' }
				])
			)
		).toBe('api has claude (working) and codex-2 (needs input). Stopping ends them.');
	});

	it('is empty when a stop would interrupt nothing, which is the signal not to ask', () => {
		expect(stopWarning(p('running', [{ agent: 'claude', window: '', state: 'idle' }]))).toBe('');
		expect(stopWarning(p('running', []))).toBe('');
	});
});
