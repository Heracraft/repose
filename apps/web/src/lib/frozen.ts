import type { Project } from './api/types';

/** The time a running machine was frozen while idle, or undefined: never
 * for a machine that is not running, since `resting` is a marker on
 * `running` and not a state of its own (I-683, I-826). */
export function frozenSince(p: Pick<Project, 'state' | 'resting'>): string | undefined {
	if (p.state !== 'running' || !p.resting) return undefined;
	return p.resting.since;
}
