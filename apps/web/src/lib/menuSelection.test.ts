import { describe, expect, it } from 'vitest';
import { menuEntries } from './menuSelection';

describe('menuEntries', () => {
	it('lists entries with their option, then packages, as stored', () => {
		expect(
			menuEntries([
				{ id: 'postgresql', options: { version: '16' } },
				{ id: 'redis' },
				{ package: 'ffmpeg' }
			])
		).toEqual(['postgresql 16', 'redis', 'ffmpeg']);
	});
	it('reads anything else as empty', () => {
		expect(menuEntries(null)).toEqual([]);
		expect(menuEntries(undefined)).toEqual([]);
		expect(menuEntries({ id: 'x' })).toEqual([]);
	});
});
