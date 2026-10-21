import { describe, expect, it } from 'vitest';
import { legacyTarget } from './legacyUrls';

// One case per row of ia-final.md section 4 (DECISIONS I-841).
const at = (s: string) => legacyTarget(new URL(s, 'https://repose.herakraft.co'));

describe('legacyTarget', () => {
	it('moves the Settings sections that became tabs', () => {
		expect(at('/settings#machine-nix')).toBe('/settings/machine-nix');
		expect(at('/settings#secrets')).toBe('/settings/secrets');
		expect(at('/settings#notifications')).toBe('/settings/notifications');
	});

	it('leaves the sections that stayed on General', () => {
		for (const h of ['#account', '#delete-account', '#timezone', '#install', '']) {
			expect(at(`/settings${h}`)).toBeNull();
		}
	});

	it('moves the project page fragments that became tabs', () => {
		expect(at('/projects/p1#events')).toBe('/projects/p1/events');
		expect(at('/projects/p1#events-card')).toBe('/projects/p1/events');
		expect(at('/projects/p1#usage')).toBe('/projects/p1/usage');
		expect(at('/projects/p1#usage-card')).toBe('/projects/p1/usage');
		expect(at('/projects/p1#snapshots')).toBe('/projects/p1/snapshots');
		expect(at('/projects/p1#snapshots-card')).toBe('/projects/p1/snapshots');
		expect(at('/projects/p1#secrets')).toBe('/projects/p1/config#secrets');
	});

	it('moves the events pager keys to Events with every key kept (I-662)', () => {
		expect(at('/projects/p1?events_page=2')).toBe('/projects/p1/events?events_page=2');
		expect(at('/projects/p1?events_before=2026-10-20T10%3A00%3A00Z&events_kind=errors')).toBe(
			'/projects/p1/events?events_before=2026-10-20T10%3A00%3A00Z&events_kind=errors'
		);
		expect(at('/projects/p1?events_after=x')).toBe('/projects/p1/events?events_after=x');
		expect(at('/projects/p1?events_page=2&filter=x')).toBe(
			'/projects/p1/events?events_page=2&filter=x'
		);
		expect(at('/projects/p1?events_page=2#events')).toBe('/projects/p1/events?events_page=2');
	});

	it('leaves the Overview sections and other project pages', () => {
		for (const h of [
			'#questions',
			'#details-card',
			'#agents-card',
			'#machine-card',
			'#destroy-card',
			''
		]) {
			expect(at(`/projects/p1${h}`)).toBeNull();
		}
		expect(at('/projects/p1/config')).toBeNull();
		expect(at('/projects/p1/config#secrets')).toBeNull();
		expect(at('/projects/p1/events?events_page=2')).toBeNull();
	});

	it('leaves the account-level pages', () => {
		expect(at('/projects#questions')).toBeNull();
		expect(at('/projects?filter=up&q=job')).toBeNull();
		expect(at('/billing')).toBeNull();
		expect(at('/billing?checkout=done')).toBeNull();
		expect(at('/billing?plan=plus')).toBeNull();
		expect(at('/questions')).toBeNull();
	});

	it('answers null for an unknown fragment', () => {
		expect(at('/settings#nope')).toBeNull();
		expect(at('/projects/p1#nope')).toBeNull();
	});
});
