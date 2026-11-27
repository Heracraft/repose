import type { Project } from './api/types';
import { bytes, uptime } from './format';

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

/** The line for a running machine whose VM got less than it gets with
 * the host to itself (I-1680), in the CLI's words (`repose status`). */
export function sizedDownText(p: Pick<Project, 'state' | 'vm'>): string {
	const v = p.vm;
	if (p.state !== 'running' || !v || !v.sized_down) return '';
	const cpus = v.vcpus === 1 ? '1 vCPU' : `${v.vcpus} vCPUs`;
	return `Runs with ${cpus} and ${bytes(v.mem_bytes)} of its ${v.full_vcpus} and ${bytes(v.full_mem_bytes)}: the machines running beside it held the rest when it started. A start after one of them stops gives it all.`;
}

/** The words for a machine parked on a Mac that waits for its user to
 * log in (I-1672): its memory is kept, so nothing here offers a restart. */
export function parkedText(p: Pick<Project, 'state' | 'parked' | 'host_name'>): string {
	if (p.state !== 'running' || !p.parked) return '';
	return `Parked: waits for you to log in to ${p.host_name || 'its Mac'}; its memory is kept`;
}
