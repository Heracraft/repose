import { platformStopReason } from '$lib/abuse';
import { ApiError } from '$lib/api/errors';
import type { GuestState, OwnedHost, Project } from '$lib/api/types';
import { bootFallbackReason } from '$lib/bootfail';
import { uptime } from '$lib/format';

/**
 * The name of the host a machine is on while that host is one the account
 * owns, and "" while it is on repose's servers or unplaced (DECISIONS I-1392):
 * reads the project's own `host_name` first so the common listing costs no
 * `GET /hosts`, falling back to `hosts` only for an older api while a host
 * is quiet. Matches `ownHostName` in internal/cli/hosts.go.
 */
export function ownHostName(
	p: Pick<Project, 'host_id' | 'host_name'>,
	hosts?: Map<string, OwnedHost>
): string {
	if (p.host_name) return p.host_name;
	if (!p.host_id || !hosts) return '';
	return hosts.get(p.host_id)?.name ?? '';
}

/**
 * Where a machine runs, in the CLI's words (DECISIONS I-1392, `hostCell` in
 * internal/cli/hosts.go): the owned host's name, `repose` for a machine on
 * repose's servers, or `-` for one not placed yet.
 */
export function hostCell(
	p: Pick<Project, 'host_id' | 'host_name'>,
	hosts?: Map<string, OwnedHost>
): string {
	const name = ownHostName(p, hosts);
	if (name) return name;
	if (!p.host_id) return '-';
	return 'repose';
}

/**
 * Where a single project runs on its detail page: shown when this project
 * runs on a host the account owns, or when the already-loaded project list
 * holds one that does, so accounts with no owned-host machines see nothing
 * extra and no extra `GET /hosts` is issued (DECISIONS I-1392).
 */
export function projectHostLabel(
	p: Pick<Project, 'host_id' | 'host_name'>,
	list?: readonly Pick<Project, 'host_id' | 'host_name'>[],
	hosts?: Map<string, OwnedHost>
): string | undefined {
	if (ownHostName(p, hosts)) return hostCell(p, hosts);
	if (list?.some((q) => !!ownHostName(q, hosts))) return hostCell(p, hosts);
	return undefined;
}

/**
 * What a project says while its host has stopped answering (DECISIONS I-1370).
 * Uses `p.host_name` when available so the owned host is named even before
 * `/hosts` answers (or if `/hosts` fails), adding the quiet duration once
 * `hosts` carries `last_heartbeat_at`.
 */
export function offlineHostText(
	p: Pick<Project, 'host_id' | 'host_name' | 'host_unreachable'>,
	hosts: Map<string, OwnedHost>,
	now = Date.now()
): string {
	if (!p.host_unreachable) return '';
	const h = p.host_id ? hosts.get(p.host_id) : undefined;
	const name = h?.name || p.host_name;
	if (!name) return 'server not answering';
	if (!h?.last_heartbeat_at) return `your host ${name} has not answered`;
	return `your host ${name} has not answered for ${uptime(h.last_heartbeat_at, now)}`;
}

/**
 * Strips the leading "code: " prefix from `last_error` when `state === 'error'`.
 * Shared by `/projects` and the project frame so the detail page never tells
 * the user less than the list.
 */
export function errorDetailText(p: Pick<Project, 'state' | 'last_error'>): string {
	if (p.state !== 'error' || !p.last_error) return '';
	const i = p.last_error.indexOf(': ');
	return i > 0 ? p.last_error.slice(i + 2) : p.last_error;
}

/**
 * What a project says while its create waits for a host of the account's own
 * to finish installing (DECISIONS I-1700), in the words `repose run` prints;
 * "" otherwise. Matches `waitingLine` in internal/cli/hostwait.go.
 */
export function waitingForHostText(p: Pick<Project, 'state' | 'waiting_for_host'>): string {
	const w = p.waiting_for_host;
	if (!w || p.state !== 'creating') return '';
	const who = w.provider === 'mac' ? `your Mac (${w.name})` : `your host ${w.name}`;
	return `Waiting for ${who} to finish installing. This project starts there when it's ready.`;
}

/**
 * The reason line shown on a `/projects` row: a wait for the account's host,
 * an abuse stop, a boot fallback, or an error's sentence.
 */
export function projectRowReason(
	p: Pick<Project, 'state' | 'last_error'> & Partial<Pick<Project, 'waiting_for_host'>>
): string {
	const waiting = waitingForHostText(p);
	if (waiting) return waiting;
	const abuse = platformStopReason(p);
	if (abuse) return abuse;
	const boot = bootFallbackReason(p);
	if (boot) return boot;
	return errorDetailText(p);
}

/** Ensures a sentence starts with a capital letter and ends with punctuation. */
export function sentence(text: string): string {
	const trimmed = text.trim();
	if (!trimmed) return '';
	const raised = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
	return /[.!?]$/.test(raised) ? raised : `${raised}.`;
}

/**
 * The error banner sentence for a project in error: the sentence after
 * "code: " in `last_error`, capitalised and punctuated.
 */
export function errorBannerText(p: Pick<Project, 'state' | 'last_error'>): string {
	const r = errorDetailText(p);
	return r ? sentence(r) : '';
}

/**
 * The banner text for a `503 capacity` refusal on Start/Restart. Own-host
 * refusals (`detail.reason === 'no_own_host'`) carry the real cause (such as
 * "your host stalehost has not registered yet") and must not be replaced with
 * the generic fleet line.
 */
export function capacityRefusalText(err: ApiError): string {
	if (err.detail?.reason === 'no_own_host' && err.message.trim()) {
		return sentence(err.message);
	}
	return 'No capacity right now.';
}

const ACTIVE_DESTROY_STATE: Partial<Record<GuestState, string>> = {
	creating: 'This machine is still being created. Wait for it to finish, then try again.',
	building: 'This machine is still building. Wait for it to finish, then try again.',
	starting: 'This machine is still starting. Wait for it to finish, then try again.',
	stopping: 'This machine is still stopping. Wait for it to finish, then try again.',
	restoring: 'This machine is still restoring. Wait for it to finish, then try again.',
	destroying: 'This machine is already being destroyed.'
};

/**
 * Plain-English inline refusal when `DELETE /projects/:id` returns 409
 * conflict (most commonly "an operation is in progress" while the machine is
 * still starting or stopping). Returns undefined for non-conflict errors.
 */
export function destroyRefusalMessage(err: unknown, state: GuestState): string | undefined {
	if (!(err instanceof ApiError) || err.code !== 'conflict') return undefined;
	const inProgress = err.message.toLowerCase().includes('operation is in progress');
	if (inProgress) {
		return (
			ACTIVE_DESTROY_STATE[state] ??
			'Another operation is running on this machine. Wait for it to finish, then try again.'
		);
	}
	return sentence(err.message) || 'This machine cannot be destroyed right now. Try again.';
}
