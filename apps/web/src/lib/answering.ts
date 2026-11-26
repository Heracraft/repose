import type { Project } from './api/types';
import { uptime } from './format';

/** When a running machine stopped answering, or undefined (I-1652): its
 * guestd has been silent for about three minutes. Never for a machine
 * that is not running or is frozen, whose guestd is paused on purpose. */
export function notAnsweringSince(
	p: Pick<Project, 'state' | 'resting' | 'not_answering'>
): string | undefined {
	if (p.state !== 'running' || p.resting || !p.not_answering) return undefined;
	return p.not_answering.since;
}

/** "Not answering for 4m", the words a row and the page lead with. */
export function notAnsweringText(
	p: Pick<Project, 'state' | 'resting' | 'not_answering'>,
	now = Date.now()
): string {
	const since = notAnsweringSince(p);
	if (!since) return '';
	return `Not answering for ${uptime(since, now) === '0m' ? '1m' : uptime(since, now)}`;
}
