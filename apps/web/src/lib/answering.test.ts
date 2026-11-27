import { describe, it, expect } from 'vitest';
import {
	notAnsweringSince,
	notAnsweringText,
	parkedText,
	restingWaitText,
	sizedDownText
} from './answering';
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

describe('parked (I-1672)', () => {
	const parked = { since: '2026-11-26T03:32:00Z', waits_for: 'login' };
	it('names the host and that its memory is kept', () => {
		expect(parkedText({ state: 'running', parked, host_name: 'mac-mini' })).toBe(
			'Parked: waits for you to log in to mac-mini; its memory is kept'
		);
	});
	it('says nothing of a machine that is not running or not parked', () => {
		expect(parkedText({ state: 'stopped', parked })).toBe('');
		expect(parkedText({ state: 'running' })).toBe('');
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

describe('sizedDownText (I-1680)', () => {
	const G = 1024 * 1024 * 1024;
	it('says what the VM got and what it gets alone, in the CLI words', () => {
		expect(
			sizedDownText({
				state: 'running',
				vm: {
					vcpus: 2,
					mem_bytes: 3968 * 1024 * 1024,
					full_vcpus: 5,
					full_mem_bytes: 8 * G,
					sized_down: true
				}
			})
		).toBe(
			'Runs with 2 vCPUs and 3.9 GB of its 5 and 8 GB: the machines running beside it held the rest when it started. A start after one of them stops gives it all.'
		);
	});
	it('says nothing for a full-size, stopped or unsized machine', () => {
		const vm = {
			vcpus: 5,
			mem_bytes: 8 * G,
			full_vcpus: 5,
			full_mem_bytes: 8 * G,
			sized_down: false
		};
		expect(sizedDownText({ state: 'running', vm })).toBe('');
		expect(sizedDownText({ state: 'stopped', vm: { ...vm, sized_down: true } })).toBe('');
		expect(sizedDownText({ state: 'running' })).toBe('');
	});
});

describe('waits for the unlock (I-1682)', () => {
	it('a parked machine on a locked Mac waits for the unlock', () => {
		expect(
			parkedText({
				state: 'running',
				host_name: 'mac-mini',
				parked: { since: '2026-11-27T03:00:00Z', waits_for: 'unlock' }
			})
		).toBe('Parked: waits for you to unlock mac-mini; its memory is kept');
	});
	it('a machine resting as a save says what wakes it, and nothing otherwise', () => {
		expect(
			restingWaitText({
				state: 'running',
				host_name: 'mac-mini',
				resting: { since: '2026-11-27T03:00:00Z', waits_for: 'unlock' }
			})
		).toBe('Resting: wakes once you unlock mac-mini; its memory is kept');
		expect(restingWaitText({ state: 'running', resting: { since: '2026-11-27T03:00:00Z' } })).toBe(
			''
		);
	});
});
