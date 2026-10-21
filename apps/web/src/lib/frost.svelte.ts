// Prototype looks for a frozen machine (I-683, I-841 amendment, owner
// feedback 2026-10-21). Dev only, behind FROZEN_LOOK: `?frost=<look>` on any
// dashboard URL picks one for this tab (kept in sessionStorage, so a click
// from /projects into a project keeps it); `?frost=off` goes back to the
// I-683 look. The look is an attribute on <html>, data-frost, that
// lib/hk/frost.css keys on; the rail reads `frost.bead` to swap its mark.
// No browser API at import: the root layout renders at prerender.

import { FROZEN_LOOK } from './flags';

export const FROST_LOOKS = ['off', 'ink', 'rime', 'pane', 'window', 'glaze'] as const;
export type FrostLook = (typeof FROST_LOOKS)[number];

/** One line per look, for the /design gallery. */
export const FROST_NOTES: Record<FrostLook, string> = {
	off: 'I-683 as built: the flake and the word in the secondary ink.',
	ink: 'The flake and the word in measured ice ink. No texture.',
	rime: 'Rime crust along the bottom edge, densest at the leading corner.',
	pane: 'Cloudy ice: low clouds and grain that thickens toward the edges.',
	window: 'Window frost: grain and a white crust grow in from every edge.',
	glaze: 'Clear ice: a cold glaze with a top rim and one specular streak.'
};

const KEY = 'repose-frost';

class Frost {
	look = $state<FrostLook>('off');
	/** The rail shows the hexagonal ice bead in place of the flake. */
	bead = $derived(this.look !== 'off' && this.look !== 'ink');
}

export const frost = new Frost();

function isLook(v: string | null): v is FrostLook {
	return !!v && (FROST_LOOKS as readonly string[]).includes(v);
}

/** Reads ?frost= (or this tab's last pick) and sets <html data-frost>. */
export function syncFrost(url: URL): void {
	if (!FROZEN_LOOK || typeof document === 'undefined') return;
	// /design shows every look side by side, each in its own data-frost box.
	if (url.pathname.startsWith('/design')) {
		frost.look = 'off';
		delete document.documentElement.dataset.frost;
		return;
	}
	const asked = url.searchParams.get('frost');
	let look: FrostLook = 'off';
	if (isLook(asked)) {
		look = asked;
		try {
			sessionStorage.setItem(KEY, look);
		} catch {
			/* private window: the look holds for this page only */
		}
	} else {
		try {
			const kept = sessionStorage.getItem(KEY);
			if (isLook(kept)) look = kept;
		} catch {
			/* no storage: off */
		}
	}
	frost.look = look;
	if (look === 'off') delete document.documentElement.dataset.frost;
	else document.documentElement.dataset.frost = look;
}
