// The context the root layout sets on dashboard pages (DECISIONS I-841):
// their <main> sits in the rail's grid, which is the frame, so PageShell
// and ErrorView drop their own .frame there.
import { getContext } from 'svelte';

export const DASH = Symbol('dash');

export interface DashContext {
	/** Whether the page is drawn in the dashboard's grid. */
	readonly on: boolean;
	/**
	 * Keeps the footer under the fold until the returned release is called.
	 * From 1024px a short page ends at the window's foot, so the footer is
	 * in view at first paint and a page whose sections arrive one after
	 * another pushed it out as they came (CLS up to 0.04 on /projects at
	 * 1440, I-664, I-841). Held, the grid is the whole window under the
	 * header; on release a short page's footer rises its 71px once.
	 */
	hold(): () => void;
}

/** Whether the calling component sits in the dashboard's grid. */
export function inDash(): DashContext {
	return getContext<DashContext | undefined>(DASH) ?? { on: false, hold: () => () => {} };
}
