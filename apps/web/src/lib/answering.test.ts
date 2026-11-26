import { describe, it, expect } from 'vitest';
import { notAnsweringSince, notAnsweringText } from './answering';
import { bytes } from './format';

describe('notAnswering (I-1652)', () => {
	const since = '2026-11-26T03:32:00Z';
	const now = Date.parse('2026-11-26T03:36:30Z');
	it('says how long a running machine has been silent', () => {
		expect(notAnsweringText({ state: 'running', not_answering: { since } }, now)).toBe(
			'Not answering for 4m'
		);
	});
	it('is never said of a frozen or a stopped machine', () => {
		expect(
			notAnsweringSince({ state: 'running', resting: { since }, not_answering: { since } })
		).toBeUndefined();
		expect(notAnsweringSince({ state: 'stopped', not_answering: { since } })).toBeUndefined();
		expect(notAnsweringText({ state: 'running' }, now)).toBe('');
	});
});

describe('bytes', () => {
	// review-dash 9: "1006 MB used" beside "1.6 GB used".
	it('says a gigabyte once a figure rounds to 1000 MB', () => {
		expect(bytes(1006 * 1024 * 1024)).toBe('1 GB');
		expect(bytes(999 * 1024 * 1024)).toBe('999 MB');
		expect(bytes(1.6 * 1024 * 1024 * 1024)).toBe('1.6 GB');
	});
});
