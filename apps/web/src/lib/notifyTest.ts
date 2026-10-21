// What Settings → Notifications says under Send test (DECISIONS I-691,
// I-841): which channels the test reached and which refused it, in one
// line that stays until the next test. Pure, so each case is tested.
import type { NotifyTestResult } from './api/client';

export interface TestReport {
	/** ok: every channel that is on got it; partly: some did; failed: none did. */
	tone: 'ok' | 'partly' | 'failed';
	text: string;
}

const NAMES = { email: 'email', ntfy: 'ntfy', gchat: 'Google Chat' } as const;
type Channel = keyof typeof NAMES;

/** "email", "email and ntfy", "email, ntfy and Google Chat". */
function list(names: string[], conj: 'and' | 'or'): string {
	if (names.length <= 1) return names.join('');
	return `${names.slice(0, -1).join(', ')} ${conj} ${names[names.length - 1]}`;
}

export function testReport(r: NotifyTestResult): TestReport {
	const channels = Object.keys(NAMES) as Channel[];
	const sent = channels.filter((c) => r[c] === 'ok').map((c) => NAMES[c]);
	const why: Partial<Record<Channel, string | undefined>> = {
		ntfy: r.ntfy_error?.replace(/\.+$/, ''),
		gchat: r.gchat_error?.replace(/\.+$/, '')
	};
	const failed = channels
		.filter((c) => r[c] === 'error')
		.map((c) => (why[c] ? `${NAMES[c]} (${why[c]})` : NAMES[c]));
	if (sent.length === 0 && failed.length === 0) {
		return { tone: 'failed', text: 'No channel is on.' };
	}
	if (sent.length === 0) {
		return { tone: 'failed', text: `Test notification not sent by ${list(failed, 'or')}.` };
	}
	const head = `Test notification sent by ${list(sent, 'and')}.`;
	if (failed.length === 0) return { tone: 'ok', text: head };
	return { tone: 'partly', text: `${head} Not sent by ${list(failed, 'or')}.` };
}
