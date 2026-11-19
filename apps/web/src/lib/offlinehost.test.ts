import { describe, expect, it } from 'vitest';
import { hostStateWord, offlineHostLine } from './offlinehost';
import type { Host } from './api/types';

const host = (over: Partial<Host> = {}): Host => ({
	id: 'h1',
	name: 'mac-mini',
	state: 'unreachable',
	mem_bytes: 24 * 1024 ** 3,
	vcpus: 8,
	cores: 8,
	projects: 2,
	running_projects: 1,
	last_heartbeat_at: new Date(Date.UTC(2026, 10, 19, 12, 0, 0)).toISOString(),
	created_at: new Date(Date.UTC(2026, 10, 1)).toISOString(),
	...over
});

const now = Date.UTC(2026, 10, 19, 12, 14, 0);

describe('a host that has stopped answering', () => {
	it('says nothing while the host is answering', () => {
		expect(offlineHostLine({ host_id: 'h1', host_unreachable: false }, new Map(), now)).toBe('');
	});

	// The user's own Mac is named: they are the one who turns it back on
	// (DECISIONS I-1370), and the words are hostUnreachableLine's in
	// internal/cli/hosts.go.
	it('names a host the user owns, with how long it has been quiet', () => {
		const hosts = new Map([['h1', host()]]);
		expect(offlineHostLine({ host_id: 'h1', host_unreachable: true }, hosts, now)).toBe(
			'your host mac-mini has not answered for 14m'
		);
	});

	it('leaves out a time it does not have', () => {
		const hosts = new Map([['h1', host({ last_heartbeat_at: null })]]);
		expect(offlineHostLine({ host_id: 'h1', host_unreachable: true }, hosts, now)).toBe(
			'your host mac-mini has not answered'
		);
	});

	// A host of repose's is not the user's to fix, and is not theirs to
	// name either.
	it("says whose it is for a host of repose's", () => {
		const hosts = new Map([['h1', host()]]);
		expect(offlineHostLine({ host_id: 'host-01', host_unreachable: true }, hosts, now)).toBe(
			'server not answering'
		);
		expect(offlineHostLine({ host_unreachable: true }, hosts, now)).toBe('server not answering');
		// And so does a page whose own /hosts read failed.
		expect(offlineHostLine({ host_id: 'h1', host_unreachable: true }, new Map(), now)).toBe(
			'server not answering'
		);
	});
});

describe('host states in the words a user reads', () => {
	it('matches what `repose host ls` prints', () => {
		expect(
			['registering', 'ready', 'unreachable', 'draining', 'retired', 'lost'].map(hostStateWord)
		).toEqual(['waiting', 'online', 'offline', 'removing', 'removed', 'removed']);
	});

	it('passes a state it does not know through', () => {
		expect(hostStateWord('something-new')).toBe('something-new');
	});
});
