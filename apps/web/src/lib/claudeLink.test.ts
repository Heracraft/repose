import { describe, expect, it } from 'vitest';
import { checkoutFolder, claudeLink, claudePlatform } from './claudeLink';

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

describe('claudePlatform (ENG-225)', () => {
	it('is desktop on macOS and Linux', () => {
		expect(
			claudePlatform(
				'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15'
			)
		).toBe('desktop');
		expect(
			claudePlatform(
				'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/131.0 Safari/537.36'
			)
		).toBe('desktop');
	});
	it('is windows on Windows', () => {
		expect(
			claudePlatform(
				'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0 Safari/537.36'
			)
		).toBe('windows');
	});
	it('is phone on a phone, a tablet, and an iPad that asks for the desktop site', () => {
		expect(
			claudePlatform(
				'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148'
			)
		).toBe('phone');
		expect(
			claudePlatform(
				'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/131.0 Mobile Safari/537.36'
			)
		).toBe('phone');
		expect(
			claudePlatform(
				'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15',
				5
			)
		).toBe('phone');
		expect(claudePlatform('Mozilla/5.0 (X11; Linux x86_64)', 0, true)).toBe('phone');
	});
});
