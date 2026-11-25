import type { Project } from './api/types';

/**
 * The api's sentence for a project the platform stopped because a
 * cryptocurrency miner was running (DECISIONS I-239), from last_error
 * "abuse_stopped: stopped: a cryptocurrency miner (xmrig) was running; ...".
 * Empty for any other project, including a stopped one whose last_error is
 * an older, unrelated failure.
 */
export function abuseStopReason(p: Pick<Project, 'state' | 'last_error'>): string {
	if (p.state !== 'stopped' || !p.last_error) return '';
	const prefix = 'abuse_stopped: ';
	return p.last_error.startsWith(prefix) ? p.last_error.slice(prefix.length) : '';
}

/**
 * The api's sentence for a machine the kernel stopped because a host of
 * the account's own ran out of memory (DECISIONS I-1632), from last_error
 * "oom_stopped: stopped: your host mac-mini ran out of memory, and the
 * kernel stopped api to keep the others running. `repose start api`
 * starts it again". The dashboard has a Start button beside it, so the
 * command is left off. Empty for any other project.
 */
export function oomStopReason(p: Pick<Project, 'state' | 'last_error'>): string {
	if (p.state !== 'stopped' || !p.last_error) return '';
	const prefix = 'oom_stopped: ';
	if (!p.last_error.startsWith(prefix)) return '';
	const text = p.last_error.slice(prefix.length);
	const cut = text.indexOf('. `repose start');
	return cut < 0 ? text : `${text.slice(0, cut)}.`;
}

/** Why the platform stopped a machine: a miner (I-239) or its host out of
 * memory (I-1632); '' for any other project. */
export function platformStopReason(p: Pick<Project, 'state' | 'last_error'>): string {
	return abuseStopReason(p) || oomStopReason(p);
}
