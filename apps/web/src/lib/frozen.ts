import { FROZEN_LOOK } from './flags';
import type { Project } from './api/types';

/** The time a running machine was frozen while idle, or undefined: never
 * with the flag off, nor for a machine that is not running (I-683). */
export function frozenSince(p: Pick<Project, 'state' | 'resting'>): string | undefined {
	if (!FROZEN_LOOK || p.state !== 'running' || !p.resting) return undefined;
	return p.resting.since;
}
