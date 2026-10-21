// The context the root layout sets on dashboard pages (DECISIONS I-841):
// their <main> sits in the rail's grid, which is the frame, so PageShell
// and ErrorView drop their own .frame there.
import { getContext } from 'svelte';

export const DASH = Symbol('dash');

export interface DashContext {
	/** Whether the page is drawn in the dashboard's grid. */
	readonly on: boolean;
}

/** Whether the calling component sits in the dashboard's grid. */
export function inDash(): DashContext {
	return getContext<DashContext | undefined>(DASH) ?? { on: false };
}
