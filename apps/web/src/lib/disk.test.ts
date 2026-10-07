import { describe, it, expect } from 'vitest';
import { diskPercent, diskFullPercent } from './disk';

const GiB = 2 ** 30;

describe('diskPercent', () => {
	it('is the root filesystem used over its size, rounded down', () => {
		expect(diskPercent({ root_used_bytes: 33 * GiB, root_size_bytes: 39 * GiB })).toBe(84);
	});
	it('is null without a figure or with one that cannot be true', () => {
		expect(diskPercent({})).toBeNull();
		expect(diskPercent({ root_used_bytes: 1, root_size_bytes: 0 })).toBeNull();
		expect(diskPercent({ root_used_bytes: 2 * GiB, root_size_bytes: GiB })).toBeNull();
	});
});

describe('diskFullPercent', () => {
	it('is set from 90 percent', () => {
		expect(diskFullPercent({ root_used_bytes: 37 * GiB, root_size_bytes: 39 * GiB })).toBe(94);
		expect(diskFullPercent({ root_used_bytes: 90, root_size_bytes: 100 })).toBe(90);
		expect(diskFullPercent({ root_used_bytes: 89, root_size_bytes: 100 })).toBeNull();
		expect(diskFullPercent({})).toBeNull();
	});
});
