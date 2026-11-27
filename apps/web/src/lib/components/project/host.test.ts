import { describe, expect, it } from 'vitest';
import { ApiError } from '$lib/api/errors';
import type { OwnedHost } from '$lib/api/types';
import {
	capacityRefusalText,
	destroyRefusalMessage,
	errorBannerText,
	hostCell,
	offlineHostText,
	ownHostName,
	projectHostLabel,
	projectRowReason,
	waitingForHostText
} from './host';

const host = (over: Partial<OwnedHost> = {}): OwnedHost => ({
	id: 'h1',
	name: 'mac-mini',
	state: 'ready',
	mem_bytes: 24 * 1024 ** 3,
	vcpus: 8,
	cores: 8,
	projects: 1,
	running_projects: 1,
	last_heartbeat_at: new Date(Date.UTC(2026, 10, 19, 12, 0, 0)).toISOString(),
	created_at: new Date(Date.UTC(2026, 10, 1)).toISOString(),
	...over
});

const NOW = Date.UTC(2026, 10, 19, 12, 14, 0);

describe('host readout (I-1392)', () => {
	it('names an owned host from project.host_name without reading /hosts', () => {
		expect(ownHostName({ host_id: 'h1', host_name: 'mac-mini' })).toBe('mac-mini');
		expect(hostCell({ host_id: 'h1', host_name: 'mac-mini' })).toBe('mac-mini');
	});

	it('returns repose for fleet machines and - for unplaced machines', () => {
		expect(hostCell({ host_id: 'host-02' })).toBe('repose');
		expect(hostCell({})).toBe('-');
	});

	it('falls back to the offline hosts map when an older api omits host_name', () => {
		const hosts = new Map([['h1', host()]]);
		expect(ownHostName({ host_id: 'h1' }, hosts)).toBe('mac-mini');
		expect(hostCell({ host_id: 'h1' }, hosts)).toBe('mac-mini');
	});

	it('shows the detail host label only when this or another listed machine is on an owned host', () => {
		const fleet = { host_id: 'host-02' };
		const own = { host_id: 'h1', host_name: 'mac-mini' };
		const unplaced = {};

		expect(projectHostLabel(fleet, [fleet])).toBeUndefined();
		expect(projectHostLabel(own, undefined)).toBe('mac-mini');
		expect(projectHostLabel(fleet, [own, fleet])).toBe('repose');
		expect(projectHostLabel(unplaced, [own, unplaced])).toBe('-');
	});
});

describe('offlineHostText', () => {
	it('names an owned host from host_name even before /hosts resolves', () => {
		expect(
			offlineHostText(
				{ host_id: 'h1', host_name: 'mac-mini', host_unreachable: true },
				new Map(),
				NOW
			)
		).toBe('your host mac-mini has not answered');
	});

	it('adds the quiet duration once /hosts carries last_heartbeat_at', () => {
		const hosts = new Map([['h1', host({ state: 'unreachable' })]]);
		expect(
			offlineHostText({ host_id: 'h1', host_name: 'mac-mini', host_unreachable: true }, hosts, NOW)
		).toBe('your host mac-mini has not answered for 14m');
	});

	it('says server not answering for a fleet host', () => {
		expect(offlineHostText({ host_id: 'host-02', host_unreachable: true }, new Map(), NOW)).toBe(
			'server not answering'
		);
	});
});

describe('a create waiting for its host (I-1700)', () => {
	it('names a Mac as your Mac and any other host by its name, on the row too', () => {
		const mac = {
			state: 'creating' as const,
			last_error: null,
			waiting_for_host: { name: 'mac-mini', provider: 'mac' }
		};
		const want =
			"Waiting for your Mac (mac-mini) to finish installing. This project starts there when it's ready.";
		expect(waitingForHostText(mac)).toBe(want);
		expect(projectRowReason(mac)).toBe(want);
		expect(
			waitingForHostText({
				state: 'creating',
				waiting_for_host: { name: 'box-1', provider: 'metal' }
			})
		).toBe(
			"Waiting for your host box-1 to finish installing. This project starts there when it's ready."
		);
	});
	it('says nothing once the machine is past creating', () => {
		expect(
			waitingForHostText({
				state: 'running',
				waiting_for_host: { name: 'mac-mini', provider: 'mac' }
			})
		).toBe('');
		expect(waitingForHostText({ state: 'creating', waiting_for_host: null })).toBe('');
	});
});

describe('error and capacity copy', () => {
	it('formats errorBannerText and projectRowReason from last_error', () => {
		const p = {
			state: 'error' as const,
			last_error: 'capacity: your host stalehost has not registered yet'
		};
		expect(projectRowReason(p)).toBe('your host stalehost has not registered yet');
		expect(errorBannerText(p)).toBe('Your host stalehost has not registered yet.');
	});

	it('preserves existing terminal punctuation in errorBannerText', () => {
		expect(
			errorBannerText({ state: 'error', last_error: 'boot_failed: the machine did not boot.' })
		).toBe('The machine did not boot.');
	});

	it('surfaces own-host capacity refusals instead of the generic fleet message', () => {
		const ownErr = new ApiError(
			{
				code: 'capacity',
				message: 'your host stalehost has not registered yet',
				detail: { reason: 'no_own_host', host: 'stalehost', host_state: 'registering' }
			},
			503,
			null
		);
		expect(capacityRefusalText(ownErr)).toBe('Your host stalehost has not registered yet.');

		const fleetErr = new ApiError(
			{ code: 'capacity', message: 'no host with capacity' },
			503,
			null
		);
		expect(capacityRefusalText(fleetErr)).toBe('No capacity right now.');
	});
});

describe('destroyRefusalMessage', () => {
	const conflict = (message = 'an operation is in progress') =>
		new ApiError({ code: 'conflict', message }, 409, null);

	it('names the active transition state when destroy conflicts with an operation in progress', () => {
		expect(destroyRefusalMessage(conflict(), 'starting')).toBe(
			'This machine is still starting. Wait for it to finish, then try again.'
		);
		expect(destroyRefusalMessage(conflict(), 'creating')).toBe(
			'This machine is still being created. Wait for it to finish, then try again.'
		);
		expect(destroyRefusalMessage(conflict(), 'stopping')).toBe(
			'This machine is still stopping. Wait for it to finish, then try again.'
		);
		expect(destroyRefusalMessage(conflict(), 'destroying')).toBe(
			'This machine is already being destroyed.'
		);
	});

	it('falls back to a clear operation message when state is settled', () => {
		expect(destroyRefusalMessage(conflict(), 'running')).toBe(
			'Another operation is running on this machine. Wait for it to finish, then try again.'
		);
	});

	it('returns undefined for non-conflict errors', () => {
		expect(
			destroyRefusalMessage(
				new ApiError({ code: 'internal', message: 'internal' }, 500, null),
				'starting'
			)
		).toBeUndefined();
	});
});
