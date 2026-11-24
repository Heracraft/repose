import { describe, expect, it } from 'vitest';
import { checkoutFolder, claudeLink } from './claudeLink';

describe('claudeLink (I-1606)', () => {
	it('opens the checkout the CLI named', () => {
		expect(claudeLink('todo-app', 'todo-app')).toBe(
			'claude://code/new?ssh_host=todo-app.repose&ssh_folder=/home/dev/todo-app'
		);
		expect(claudeLink('api', 'job_search.v2')).toBe(
			'claude://code/new?ssh_host=api.repose&ssh_folder=/home/dev/job_search.v2'
		);
	});
	it('opens the home when the machine has no checkout', () => {
		expect(checkoutFolder('')).toBe('/home/dev');
	});
	it('opens ~ while the checkout is unknown, or is not a plain folder name', () => {
		for (const c of [null, undefined, '..', '.', 'a/b', 'a&q=x', 'x y']) {
			expect(claudeLink('api', c)).toBe('claude://code/new?ssh_host=api.repose&ssh_folder=~');
		}
	});
	it('carries nothing else', () => {
		const u = new URL(claudeLink('todo-app', 'todo-app'));
		expect([...u.searchParams.keys()]).toEqual(['ssh_host', 'ssh_folder']);
		expect(u.href).not.toContain('@');
	});
});
