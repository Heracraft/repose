// Hero loop (2026-11-12): the candidate hero pictures, drawn in place of
// the Orbit when the page's URL carries ?hero=<name>. Each candidate is its
// own component so the directions can be built and judged apart. The
// landing is prerendered (+page.ts), so the URL is read in the browser
// only, after mount: reading it at render made the build answer 500 on /
// (2026-11-15). The alternates load on request, so the page ships one hero.
import type { Component } from 'svelte';

const found = import.meta.glob<{ default: Component }>('./Hero*.svelte');

export async function loadHero(name: string): Promise<Component | undefined> {
	for (const [path, load] of Object.entries(found)) {
		const n = path.match(/Hero([A-Z][A-Za-z0-9]+)\.svelte$/)?.[1]?.toLowerCase();
		if (n === name) return (await load()).default;
	}
	return undefined;
}
