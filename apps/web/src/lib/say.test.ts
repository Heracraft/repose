import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { ApiError } from '$lib/api/errors';
import type { Say } from '$lib/api/types';
import { TEXT_CAP, sayRefusal, sayState } from './say';

const repo = join(__dirname, '..', '..', '..', '..');
const guestSource = readFileSync(join(repo, 'internal', 'guestd', 'sample', 'say.go'), 'utf8');
const apiSource = readFileSync(join(repo, 'internal', 'api', 'http', 'say.go'), 'utf8');
const serviceSource = readFileSync(join(repo, 'internal', 'api', 'say', 'say.go'), 'utf8');

/** The five refusal reasons internal/api/http/say.go's sayErr can send. */
const REASONS = [
	'text_too_long',
	'not_running',
	'machine_unreachable',
	'no_agent',
	'several_agents'
] as const;

function refusal(reason: string, code = 'conflict'): ApiError {
	return new ApiError(
		{ code: code as ApiError['code'], message: 'the api said something else', detail: { reason } },
		code === 'invalid' ? 400 : 409,
		null
	);
}

const say = (s: Partial<Say>): Pick<Say, 'state' | 'window' | 'replaced'> =>
	({ state: 'delivered', window: 'claude', ...s }) as Say;

describe('sayRefusal', () => {
	it('owns a sentence for every reason the api can send', () => {
		for (const reason of REASONS) {
			const text = sayRefusal(
				refusal(reason, reason === 'several_agents' ? 'invalid' : 'conflict')
			);
			expect(text, reason).toBeTruthy();
			expect(text, reason).not.toContain('the api said something else');
		}
	});

	// The api passes the machine's own sentence through where it names the
	// windows, and that sentence names a CLI flag: no refusal shown in a
	// browser may carry one.
	it('never shows a command-line flag', () => {
		for (const reason of REASONS) {
			expect(sayRefusal(refusal(reason, 'invalid'))).not.toContain('--');
		}
	});

	it('leaves anything it does not know to the caller', () => {
		expect(sayRefusal(refusal('something_new'))).toBeUndefined();
		expect(
			sayRefusal(new ApiError({ code: 'internal', message: 'internal error' }, 500, null))
		).toBeUndefined();
		expect(sayRefusal(new Error('offline'))).toBeUndefined();
		expect(sayRefusal(undefined)).toBeUndefined();
	});

	// The reasons are the api's; this fails the day sayErr grows or renames
	// one, which is the day this module needs another sentence.
	it('covers the reasons internal/api/http/say.go sends, and no others', () => {
		// Both shapes sayErr writes a reason in: the map literal, and the
		// `reason` the no_agent / several_agents branch picks.
		const found = new Set(
			[...apiSource.matchAll(/"reason":\s*"(\w+)"|\breason(?:,\s*code)?\s*:?=\s*"(\w+)"/g)].map(
				(m) => m[1] ?? m[2]
			)
		);
		expect([...found].sort()).toEqual([...REASONS].sort());
	});

	// internal/api/http/say.go:sayErr keeps the machine's wording on
	// purpose ("The machine's own sentence is kept where it names the agent
	// windows"), and the machine's wording names --agent. While both are
	// true, every sentence here has to be the dashboard's own.
	it('is load-bearing: the api still passes a sentence naming --agent through', () => {
		expect(guestSource).toContain('Name one with --agent');
		expect(apiSource).toContain('agentErr.Detail');
	});
});

describe('sayState', () => {
	it('says what the machine did, for every state', () => {
		expect(sayState(say({}))).toBe('Delivered to claude.');
		expect(sayState(say({ replaced: true }))).toBe(
			'Delivered to claude, in place of the message that was waiting.'
		);
		expect(sayState(say({ state: 'waiting' }))).toBe(
			'claude is mid-turn. It gets the message when the turn ends.'
		);
		expect(sayState(say({ state: 'waiting', replaced: true }))).toBe(
			'claude is mid-turn. It gets this message at the end of the turn, in place of the one that was waiting.'
		);
		expect(sayState(say({ state: 'replaced', window: 'codex-2' }))).toBe(
			'It replaced the message waiting for codex-2.'
		);
		expect(sayState(say({ state: 'failed' }))).toBe('The machine did not type it into claude.');
	});

	it('names the agent plainly when the answer carries no window', () => {
		expect(sayState(say({ window: '' }))).toBe('Delivered to the agent.');
	});
});

describe('TEXT_CAP', () => {
	it('is internal/api/say/say.go TextCap', () => {
		const m = serviceSource.match(/const TextCap = (\d+) << (\d+)/);
		expect(m).toBeTruthy();
		expect(TEXT_CAP).toBe(Number(m![1]) << Number(m![2]));
		expect(TEXT_CAP).toBe(4096);
	});
});
