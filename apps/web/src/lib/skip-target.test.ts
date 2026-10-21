// @vitest-environment node
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The layout's "Skip to content" link is rendered on every page and points
// at #main. A page whose markup has no <main id="main">, and wraps itself in
// none of the shells that draw one, leaves that link pointing at nothing
// (/callback did, I-400). This reads every +page.svelte and +error.svelte
// and holds each to it. A page may get its <main> from a +layout.svelte
// above it that draws PageShell (a project's tabs and Settings' tabs draw
// their header once in their layout, I-841).
const routes = fileURLToPath(new URL('../routes', import.meta.url));
const SHELLS = ['PageShell', 'DocPage', 'LegalPage', 'ErrorView'];

function pages(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) return pages(p);
		return name === '+page.svelte' || name === '+error.svelte' ? [p] : [];
	});
}

const hasMain = (src: string) =>
	/<main\b[^>]*\bid="main"/.test(src) || SHELLS.some((s) => new RegExp(`<${s}\\b`).test(src));

/** Whether a +layout.svelte between the page and the routes root draws the <main>. */
function layoutDrawsMain(file: string): boolean {
	for (let dir = dirname(file); dir.startsWith(routes) && dir !== routes; dir = dirname(dir)) {
		try {
			if (hasMain(readFileSync(join(dir, '+layout.svelte'), 'utf8'))) return true;
		} catch {
			// No layout at this level.
		}
	}
	return false;
}

describe('skip link target', () => {
	const files = pages(routes);
	it('finds the routes', () => {
		expect(files.length).toBeGreaterThan(10);
	});
	for (const file of files) {
		it(`${file.slice(routes.length) || '/'} has a <main id="main">`, () => {
			expect(hasMain(readFileSync(file, 'utf8')) || layoutDrawsMain(file)).toBe(true);
		});
	}
});
