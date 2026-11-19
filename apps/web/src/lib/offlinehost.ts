import type { OwnedHost, Project } from '$lib/api/types';
import { uptime } from '$lib/format';

/**
 * What a machine's row says while its host has stopped answering
 * (DECISIONS I-1370). A host the user owns is named, with how long it
 * has been quiet, because they are the one who turns it back on; a host
 * of repose's is not theirs to fix, so the line says whose it is.
 *
 * `hosts` is the account's own hosts by id, which the page reads only
 * when a machine says its host is quiet. The CLI's twin is
 * hostUnreachableLine in internal/cli/hosts.go, and the two say the same
 * words.
 */
export function offlineHostLine(
	p: Pick<Project, 'host_id' | 'host_unreachable'>,
	hosts: Map<string, OwnedHost>,
	now = Date.now()
): string {
	if (!p.host_unreachable) return '';
	const h = p.host_id ? hosts.get(p.host_id) : undefined;
	if (!h) return 'server not answering';
	// "your host <name>" is the api's own wording for a placement it
	// refused (ops.ownHostRefusal), so the two say the same thing.
	if (!h.last_heartbeat_at) return `your host ${h.name} has not answered`;
	return `your host ${h.name} has not answered for ${uptime(h.last_heartbeat_at, now)}`;
}

/** The host states in the words a user reads, as `repose host ls` prints them. */
export function hostStateWord(state: string): string {
	switch (state) {
		case 'ready':
			return 'online';
		case 'unreachable':
			return 'offline';
		case 'registering':
			return 'waiting';
		case 'draining':
			return 'removing';
		case 'retired':
		case 'lost':
			return 'removed';
	}
	return state;
}
