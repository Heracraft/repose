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

/** A short form of the sized-down fact for a row or a header (I-1680,
 * review-dash P3 #8): "2 of 5 vCPUs · 3.9 of 8 GB". */
export function sizedDownShort(p: Pick<Project, 'state' | 'vm'>): string {
	const v = p.vm;
	if (p.state !== 'running' || !v || !v.sized_down) return '';
	return `${v.vcpus} of ${v.full_vcpus} vCPUs · ${bytes(v.mem_bytes)} of ${bytes(v.full_mem_bytes)}`;
}

/** The words for a machine parked on a Mac that waits for its user to
 * log in (I-1672): its memory is kept, so nothing here offers a restart. */
export function parkedText(p: Pick<Project, 'state' | 'parked' | 'host_name'>): string {
	if (p.state !== 'running' || !p.parked) return '';
	const mac = p.host_name || 'its Mac';
	// Parked by a restart through repose while the Mac reboots (I-1690
	// amendment, review-dash P2 #2).
	if (p.parked.waits_for === 'restart') return `Parked: ${mac} is restarting; its memory is kept`;
	// A Mac that is logged in but locked waits for the unlock (I-1682).
	if (p.parked.waits_for === 'unlock')
		return `Parked: waits for you to unlock ${mac}; its memory is kept`;
	return `Parked: waits for you to log in to ${mac}; its memory is kept`;
}

/** The words for a machine resting as a save that its locked Mac cannot
 * read (I-1682): it wakes at the unlock, not at a connection. */
export function restingWaitText(p: Pick<Project, 'state' | 'resting' | 'host_name'>): string {
	if (p.state !== 'running' || !p.resting?.waits_for) return '';
	const mac = p.host_name || 'its Mac';
	if (p.resting.waits_for === 'login')
		return `Resting: wakes once you log in to ${mac}; its memory is kept`;
	return `Resting: wakes once you unlock ${mac}; its memory is kept`;
}
