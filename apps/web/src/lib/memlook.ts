// Three looks for the "Memory running" figure (MemoryBlocks.svelte), for
// the owner's pick (owner feedback 2026-10-21: the coloured squares). Dev
// only: `?memlook=a|b|c` on any URL picks one for this tab, kept in
// sessionStorage so it survives a click away and back. A production build
// draws MEM_LOOK_DEFAULT; /design shows all three by passing `look`.

import { dev } from '$app/environment';

export const MEM_LOOKS = ['a', 'b', 'c'] as const;
export type MemLook = (typeof MEM_LOOKS)[number];
export const MEM_LOOK_DEFAULT: MemLook = 'a';

/** One line per look, for the /design gallery. */
export const MEM_LOOK_NOTES: Record<MemLook, string> = {
	a: 'One segmented bar in zinc steps, the plan a tick, over past it in amber.',
	b: 'One square per GB in ink, frozen in ice, free as outlines.',
	c: 'One row per machine, each bar on the plan’s scale.'
};

const KEY = 'repose-memlook';

function isLook(v: string | null): v is MemLook {
	return !!v && (MEM_LOOKS as readonly string[]).includes(v);
}

/** The look ?memlook= asks for, or this tab's last pick, in dev only. */
export function memLook(url: URL): MemLook {
	if (!dev) return MEM_LOOK_DEFAULT;
	const asked = url.searchParams.get('memlook');
	if (isLook(asked)) {
		try {
			sessionStorage.setItem(KEY, asked);
		} catch {
			/* no storage: the asked look holds for this page only */
		}
		return asked;
	}
	try {
		const kept = sessionStorage.getItem(KEY);
		if (isLook(kept)) return kept;
	} catch {
		/* no storage, or the server: the default */
	}
	return MEM_LOOK_DEFAULT;
}
