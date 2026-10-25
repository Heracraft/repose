import { describe, expect, it } from 'vitest';
import { PROJECT_TABS, tabOf } from './tabs';

describe('tabOf', () => {
	const id = '0190-a';
	it('names each tab by its path', () => {
		expect(tabOf(`/projects/${id}`, id)).toBe('Overview');
		expect(tabOf(`/projects/${id}/`, id)).toBe('Overview');
		expect(tabOf(`/projects/${id}/events`, id)).toBe('Events');
		expect(tabOf(`/projects/${id}/usage`, id)).toBe('Usage');
		expect(tabOf(`/projects/${id}/snapshots`, id)).toBe('Snapshots');
		expect(tabOf(`/projects/${id}/config`, id)).toBe('Config');
	});
	it('does not match a path that only starts like a tab', () => {
		expect(tabOf(`/projects/${id}/eventsx`, id)).toBe('Overview');
	});
	it('is Overview for another project or no project', () => {
		expect(tabOf('/projects/other/config', id)).toBe('Overview');
		expect(tabOf('/projects', id)).toBe('Overview');
	});
	it('keeps the five in order', () => {
		expect(PROJECT_TABS.map(([t]) => t)).toEqual([
			'Overview',
			'Events',
			'Usage',
			'Snapshots',
			'Config'
		]);
	});
});
