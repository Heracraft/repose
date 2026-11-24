import { describe, expect, it } from 'vitest';
import { repoName, wordName } from './machineName';

describe('wordName', () => {
	it('is two words the api name rule takes', () => {
		expect(wordName([])).toMatch(/^[a-z]+-[a-z]+$/);
	});
	it('never offers a name the account has', () => {
		let i = 0;
		// The first pick is taken; the second is not.
		const seq = [0, 0, 0.5, 0.5];
		const n = wordName(['Amber-Alder'], () => seq[i++ % seq.length]);
		expect(n).not.toBe('amber-alder');
	});
});

describe('repoName', () => {
	it('takes the last segment of any form a remote is typed in', () => {
		expect(repoName('https://GitHub.com/heracraft/Todo-App.git')).toBe('todo-app');
		expect(repoName('git@github.com:heracraft/todo-app.git')).toBe('todo-app');
		expect(repoName('github.com/heracraft/todo-app/')).toBe('todo-app');
		expect(repoName('github.com/you/my app!')).toBe('my-app');
		expect(repoName('')).toBe('');
	});
});
