// @vitest-environment node
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The layout's "Skip to content" link is rendered on every page and points
// at #main. A page whose markup has no <main id="main">, and wraps itself in
// none of the shells that draw one, leaves that link pointing at nothing
// (/callback did, I-400). This reads every +page.svelte and +error.svelte
// and holds each to it.
const routes = fileURLToPath(new URL('../routes', import.meta.url));
const SHELLS = ['PageShell', 'DocPage', 'LegalPage', 'ErrorView'];

function pages(dir: string): string[] {
	return readdirSync(dir).flatMap((name) => {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) return pages(p);
		return name === '+page.svelte' || name === '+error.svelte' ? [p] : [];
	});
}

describe('skip link target', () => {
	const files = pages(routes);
	it('finds the routes', () => {
		expect(files.length).toBeGreaterThan(10);
	});
	for (const file of files) {
		it(`${file.slice(routes.length) || '/'} has a <main id="main">`, () => {
			const src = readFileSync(file, 'utf8');
			const own = /<main\b[^>]*\bid="main"/.test(src);
			const shell = SHELLS.some((s) => new RegExp(`<${s}\\b`).test(src));
			expect(own || shell).toBe(true);
		});
	}
});
