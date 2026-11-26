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

describe('eventKind of a memory stop (pass2-dash N6)', () => {
	it('says what the CLI says, amber, under Errors', async () => {
		const { eventKind } = await import('./events');
		expect(eventKind('oom_stopped')).toEqual({
			label: 'stopped, host out of memory',
			tone: 'attention',
			group: 'error'
		});
	});
});

describe('dashboardWords (pass2-dash N2)', () => {
	it("names New project, not the CLI's flag, as the second way out", async () => {
		const { dashboardWords } = await import('./refusal');
		expect(
			dashboardWords(
				"your host mac-mini runs as many machines at once as its cores allow, and a and b are running there. Stopping one of them frees room, or `repose run --host repose` makes this one on repose's servers"
			)
		).toBe(
			"your host mac-mini runs as many machines at once as its cores allow, and a and b are running there. Stopping one of them frees room, or New project can make it on repose's servers"
		);
	});
});
