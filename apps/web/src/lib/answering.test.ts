import { describe, it, expect } from 'vitest';
import {
	notAnsweringSince,
	notAnsweringText,
	parkedText,
	restingWaitText,
	sizedDownShort,
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
				"your Mac mac-mini is running as many machines as it can. Stop a or b there, or run it on repose's servers: repose run --host repose"
			)
		).toBe(
			"your Mac mac-mini is running as many machines as it can. Stop a or b there, or make it on repose's servers with New project"
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

describe('review-dash fixes (I-1690 amendment)', () => {
	const G = 1024 * 1024 * 1024;
	it('a machine parked by a restart through repose says its host is restarting', () => {
		expect(
			parkedText({
				state: 'running',
				host_name: 'mac-mini',
				parked: { since: '2026-11-27T10:15:30Z', waits_for: 'restart' }
			})
		).toBe('Parked: mac-mini is restarting; its memory is kept');
	});
	it('a sized-down start has a short form for the header and the row', () => {
		expect(
			sizedDownShort({
				state: 'running',
				vm: { vcpus: 2, mem_bytes: 4 * G, full_vcpus: 5, full_mem_bytes: 8 * G, sized_down: true }
			})
		).toBe('2 of 5 vCPUs · 4 GB of 8 GB');
	});
});

describe('held on a Mac (I-1712)', () => {
	it('says what a Mac holds for a machine, 0 B while it rests as a save', async () => {
		const { heldText, sizeText } = await import('./format');
		expect(heldText(3.8 * 1024 ** 3)).toBe('3.8 GB held');
		expect(heldText(0)).toBe('0 B held');
		const vm = { vcpus: 5 };
		expect(
			sizeText({ class: 'large', sized: false, state: 'running', mem_used_bytes: 0, vm })
		).toBe('0 B held');
		expect(
			sizeText({ class: 'large', sized: false, state: 'running', mem_used_bytes: 1 << 30 })
		).toBe('1 GB used');
	});
});
