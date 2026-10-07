import type { Project } from './api/types';

/** Where a disk is nearly full: guestd's disk_high threshold (I-11), past
 * which a build or an agent's next write can fail. `repose status` and
 * `repose ls` use the same figure (I-567). */
export const DISK_FULL_PERCENT = 90;

/** The guest's root filesystem, used over its size, in whole percent (as
 * guestd counts it), or null when the api's newest sample has no figure
 * (a guest older than I-567, or one that did not answer). */
export function diskPercent(
	p: Pick<Project, 'root_used_bytes' | 'root_size_bytes'>
): number | null {
	const used = p.root_used_bytes;
	const size = p.root_size_bytes;
	if (used === undefined || size === undefined || size <= 0 || used < 0 || used > size) return null;
	return Math.floor((used * 100) / size);
}

/** diskPercent when it is DISK_FULL_PERCENT or more, else null. */
export function diskFullPercent(
	p: Pick<Project, 'root_used_bytes' | 'root_size_bytes'>
): number | null {
	const pct = diskPercent(p);
	return pct !== null && pct >= DISK_FULL_PERCENT ? pct : null;
}
