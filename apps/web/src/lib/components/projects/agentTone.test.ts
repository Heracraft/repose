import { describe, expect, it } from 'vitest';
import { agentTone } from './agentTone';

describe('agentTone', () => {
	it('maps each agent state to its dot', () => {
		expect(agentTone('working')).toBe('busy');
		expect(agentTone('needs_input')).toBe('warn');
		expect(agentTone('question')).toBe('warn');
		expect(agentTone('running')).toBe('on');
		expect(agentTone('idle')).toBe('idle');
		expect(agentTone('thinking_hard')).toBe('idle');
	});
});
