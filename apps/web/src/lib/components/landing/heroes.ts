// Hero loop (2026-11-12): the candidate hero pictures, drawn in place of
// Compose when the page's URL carries ?hero=<name>. Each candidate is its
// own component so the four directions can be built and judged apart.
import type { Component } from 'svelte';

const found = import.meta.glob<{ default: Component }>('./Hero*.svelte', { eager: true });

export const heroes: Record<string, Component> = {};
for (const [path, mod] of Object.entries(found)) {
	const name = path.match(/Hero([A-Z][a-z]+)\.svelte$/)?.[1]?.toLowerCase();
	if (name) heroes[name] = mod.default;
}
