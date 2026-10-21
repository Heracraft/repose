import { describe, it, expect } from 'vitest';
import {
	closedLine,
	askedLine,
	expiryLine,
	layoutQuestions,
	EXPIRY_SHOWN_MS
} from './question-view';
import type { Question } from './api/types';

const NOW = new Date(2026, 9, 12, 12, 0, 0).getTime();

function q(over: Partial<Question> & { id: string }): Question {
	return {
		project_id: 'p1',
		project: 'mailflare',
		agent: 'claude',
		window: 'shell',
		text: 'Ship it?',
		options: [],
		state: 'pending',
		answer: null,
		answered_via: null,
		created_at: new Date(NOW - 60_000).toISOString(),
		expires_at: new Date(NOW + 30 * 60_000).toISOString(),
		answered_at: null,
		...over
	};
}

const ago = (min: number) => new Date(NOW - min * 60_000).toISOString();

describe('closedLine', () => {
	it('names the answer and where it came from', () => {
		const a = (answered_via: Question['answered_via'], answer = 'yes') =>
			closedLine(q({ id: 'a', state: 'answered', answer, answered_via }));
		expect(a('dashboard')).toEqual({
			answer: 'yes',
			text: 'Answered yes',
			tone: 'answered'
		});
		expect(a('cli').text).toBe('Answered yes from the CLI');
		expect(a('email').text).toBe('Answered yes by email');
		expect(a('ntfy').text).toBe('Answered yes in ntfy');
		expect(a('gchat').text).toBe('Answered yes in Google Chat');
	});
	it('says answered in the terminal, with no answer to name', () => {
		expect(closedLine(q({ id: 'a', state: 'answered', answered_via: 'terminal' }))).toEqual({
			text: 'Answered in the terminal',
			tone: 'answered'
		});
	});
	it('says a withdrawal is the agent’s and a dismissal is yours', () => {
		expect(closedLine(q({ id: 'a', state: 'cancelled', answered_via: 'agent' })).text).toBe(
			'Withdrawn by the agent'
		);
		expect(closedLine(q({ id: 'a', state: 'cancelled', answered_via: 'dashboard' })).text).toBe(
			'Dismissed'
		);
		expect(closedLine(q({ id: 'a', state: 'cancelled', answered_via: 'cli' })).text).toBe(
			'Dismissed'
		);
	});
	it('does not say you dismissed a question the api closed with no source', () => {
		// asker_gone, interrupted, a machine stop and an older hostd's
		// reasonless close are stored as cancelled with answered_via null.
		expect(closedLine(q({ id: 'a', state: 'cancelled', answered_via: null })).text).toBe(
			'The agent stopped waiting'
		);
		expect(closedLine(q({ id: 'a', state: 'cancelled' })).text).toBe('The agent stopped waiting');
		expect(closedLine(q({ id: 'a', state: 'expired' }))).toEqual({
			text: 'Expired',
			tone: 'closed'
		});
	});
	it('does not crash on a source it has not heard of', () => {
		const line = closedLine(
			q({
				id: 'a',
				state: 'answered',
				answer: 'no',
				answered_via: 'pager' as Question['answered_via']
			})
		);
		expect(line.text).toBe('Answered no');
	});
});

describe('askedLine and expiryLine', () => {
	it('gives the age on the shared formatter', () => {
		expect(askedLine(q({ id: 'a', created_at: ago(3) }), NOW)).toBe('asked 3m ago');
		expect(askedLine(q({ id: 'a', created_at: ago(0) }), NOW)).toBe('asked just now');
	});
	it('shows the expiry only in its last 10 minutes', () => {
		const at = (ms: number) => q({ id: 'a', expires_at: new Date(NOW + ms).toISOString() });
		expect(expiryLine(at(EXPIRY_SHOWN_MS + 60_000), NOW)).toBe('');
		expect(expiryLine(at(EXPIRY_SHOWN_MS), NOW)).toBe('expires in 10m');
		expect(expiryLine(at(4 * 60_000), NOW)).toBe('expires in 4m');
		expect(expiryLine(at(-1), NOW)).toBe('');
	});
});

describe('layoutQuestions', () => {
	it('keeps one order for questions asked in the same millisecond, whatever order they come in', () => {
		const at = ago(5);
		const a = q({ id: 'q-a', agent: 'claude', created_at: at });
		const b = q({ id: 'q-b', agent: 'codex', created_at: at });
		const ids = (qs: Question[]) => layoutQuestions(qs)[0].rows.map((r) => r.question.id);
		expect(ids([a, b])).toEqual(ids([b, a]));
	});
	it('puts the newest first and folds older ones from the same agent window', () => {
		const groups = layoutQuestions([
			q({ id: 'old', created_at: ago(40) }),
			q({ id: 'new', created_at: ago(3) }),
			q({ id: 'other', agent: 'codex', window: 'api', created_at: ago(10) })
		]);
		expect(groups).toHaveLength(1);
		expect(groups[0].rows.map((r) => [r.question.id, r.folded.map((f) => f.id)])).toEqual([
			['new', ['old']],
			['other', []]
		]);
	});
	it('keeps a closed row in its place and folds nothing under it', () => {
		const groups = layoutQuestions([
			q({ id: 'new', created_at: ago(3), state: 'answered', answer: 'yes', answered_via: 'cli' }),
			q({ id: 'old', created_at: ago(40) })
		]);
		expect(groups[0].rows.map((r) => [r.question.id, r.folded.length])).toEqual([
			['new', 0],
			['old', 0]
		]);
	});
	it('groups by project, the one that has waited longest first', () => {
		const groups = layoutQuestions([
			q({ id: 'm1', created_at: ago(3) }),
			q({ id: 'k1', project_id: 'p2', project: 'kanali', agent: 'codex', created_at: ago(120) }),
			q({ id: 'm2', agent: 'pi', window: 'pi', created_at: ago(41) })
		]);
		expect(groups.map((g) => [g.project, g.pending])).toEqual([
			['kanali', 1],
			['mailflare', 2]
		]);
		expect(groups[1].rows.map((r) => r.question.id)).toEqual(['m1', 'm2']);
	});
	it('separates windows when an agent runs in two', () => {
		const groups = layoutQuestions([
			q({ id: 'a', window: 'one', created_at: ago(3) }),
			q({ id: 'b', window: 'two', created_at: ago(5) })
		]);
		expect(groups[0].rows.map((r) => r.folded.length)).toEqual([0, 0]);
	});
});
