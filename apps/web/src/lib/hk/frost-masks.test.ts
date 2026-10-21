import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// The frost a frozen machine shows (frost.css) is painted through masks that
// scripts/frost-masks.mjs writes into frost-masks.css. A hand edit there, or
// a generator change not rerun, ships a mask nobody can rebuild; a bad escape
// ships one the browser cannot decode, and the frost silently disappears.
const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(here, 'frost-masks.css'), 'utf8');
const masks = [...css.matchAll(/(--hk-frost-[\w-]+): url\("data:image\/svg\+xml,([^"]+)"\);/g)];

describe('frost masks', () => {
	it('are what the generator writes', () => {
		const script = join(here, '../../../scripts/frost-masks.mjs');
		expect(() =>
			execFileSync(process.execPath, [script, '--check'], { stdio: 'pipe' })
		).not.toThrow();
	});

	it('are every mask frost.css paints through', () => {
		const frost = readFileSync(join(here, 'frost.css'), 'utf8');
		const used = new Set(
			[...frost.matchAll(/var\((--hk-frost-(?:head|meter|row)[\w-]*)\)/g)].map((m) => m[1])
		);
		expect([...used].sort()).toEqual(masks.map((m) => m[1]).sort());
	});

	it.each(masks.map((m) => [m[1], m[2]]))(
		'%s decodes to one SVG with no raw # < >',
		(_name, data) => {
			expect(data).not.toMatch(/[#<>"]/);
			const svg = decodeURIComponent(data);
			expect(svg.startsWith("<svg xmlns='http://www.w3.org/2000/svg'")).toBe(true);
			expect(svg.endsWith('</svg>')).toBe(true);
			// Every reference points at an id the SVG defines.
			const ids = new Set([...svg.matchAll(/ id='([^']+)'/g)].map((m) => m[1]));
			for (const [, ref] of svg.matchAll(/(?:href='#|url\(#)([^')]+)/g)) expect(ids).toContain(ref);
		}
	);
});
