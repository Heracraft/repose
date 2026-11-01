import { describe, expect, it } from 'vitest';
import { agentLink } from './agent-link';

describe('agentLink', () => {
	it('names the host beside the label', () => {
		expect(
			agentLink({ url: 'http://localhost:8742/out/a.html', label: 'Compare resumes' })?.text
		).toBe('Compare resumes · localhost:8742');
		expect(agentLink({ url: 'http://127.0.0.1:5173/' })?.text).toBe('Open localhost:5173');
		expect(agentLink({ url: 'http://[::1]:3000/' })?.text).toBe('Open localhost:3000');
		expect(agentLink({ url: 'https://github.com/h/r/pull/12', label: 'PR 12' })?.text).toBe(
			'PR 12 · github.com'
		);
	});
	it('draws nothing that is not the web, or hides its host', () => {
		for (const url of [
			'javascript:alert(1)',
			'data:text/html,x',
			'https://a:b@evil.example/',
			'nonsense',
			''
		])
			expect(agentLink({ url })).toBeUndefined();
		expect(agentLink(undefined)).toBeUndefined();
	});
});
