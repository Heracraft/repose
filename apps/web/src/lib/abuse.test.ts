import { describe, expect, it } from 'vitest';
import { abuseStopReason } from './abuse';

const le =
	'abuse_stopped: stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.run/terms';

describe('abuseStopReason', () => {
	it('is the sentence for a project stopped for mining', () => {
		expect(abuseStopReason({ state: 'stopped', last_error: le })).toBe(
			'stopped: a cryptocurrency miner (xmrig) was running; mining is not allowed on repose, see the terms at https://repose.run/terms'
		);
	});
	it('is empty for another reason, another state, or none', () => {
		expect(
			abuseStopReason({ state: 'stopped', last_error: 'snapshot_failed: upload failed' })
		).toBe('');
		expect(abuseStopReason({ state: 'running', last_error: le })).toBe('');
		expect(abuseStopReason({ state: 'stopped', last_error: null })).toBe('');
	});
});
