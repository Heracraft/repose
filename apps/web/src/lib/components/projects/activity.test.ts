import { describe, expect, it } from 'vitest';
import { foldActivity } from './activity';

const ev = (kind: string, project: string, ts: string, summary = kind) => ({
	kind,
	ts,
	summary,
	project: { id: project }
});

describe('foldActivity', () => {
	it('folds a run of one kind on one project into its newest row', () => {
		const got = foldActivity([
			ev('snapshot_done', 'a', '2026-10-20T23:06:00Z', 'newest'),
			ev('snapshot_done', 'a', '2026-10-20T22:06:00Z'),
			ev('snapshot_done', 'a', '2026-10-20T21:06:00Z'),
			ev('agent_message', 'a', '2026-10-20T20:06:00Z')
		]);
		expect(got.map((r) => [r.kind, r.more, r.summary])).toEqual([
			['snapshot_done', 2, 'newest'],
			['agent_message', 0, 'agent_message']
		]);
		expect(got[0].ts).toBe('2026-10-20T23:06:00Z');
	});

	it('keeps runs apart across projects and across an event between them', () => {
		const got = foldActivity([
			ev('completed', 'a', '5'),
			ev('completed', 'b', '4'),
			ev('completed', 'a', '3'),
			ev('error', 'a', '2'),
			ev('completed', 'a', '1')
		]);
		expect(got.map((r) => `${r.project.id}:${r.kind}:${r.more}`)).toEqual([
			'a:completed:0',
			'b:completed:0',
			'a:completed:0',
			'a:error:0',
			'a:completed:0'
		]);
	});

	it('keeps every question and failure on its own row', () => {
		const got = foldActivity([
			ev('needs_input', 'a', '6', 'Which branch should the review PR target?'),
			ev('needs_input', 'a', '5', 'Run the migration on staging now?'),
			ev('error', 'a', '4'),
			ev('error', 'a', '3'),
			ev('snapshot_failed', 'a', '2'),
			ev('snapshot_failed', 'a', '1')
		]);
		expect(got.map((r) => `${r.kind}:${r.more}`)).toEqual([
			'needs_input:0',
			'needs_input:0',
			'error:0',
			'error:0',
			'snapshot_failed:0',
			'snapshot_failed:0'
		]);
	});

	it('does not change the rows it was given', () => {
		const rows = [ev('x', 'a', '2'), ev('x', 'a', '1')];
		foldActivity(rows);
		expect(rows).toEqual([ev('x', 'a', '2'), ev('x', 'a', '1')]);
	});
});
