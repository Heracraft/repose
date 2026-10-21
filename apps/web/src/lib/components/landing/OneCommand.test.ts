import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { READY } from './OneCommand.svelte';

const index = join(__dirname, '..', '..', '..', 'content', 'docs', 'index.md');

// The machine's "Ready in Ns" is the docs quickstart's time for a run that
// creates the machine (DECISIONS I-668, F39): when index.md's figure
// changes, the picture changes with it.
describe('OneCommand', () => {
	it("shows the quickstart's ready time", () => {
		const m = readFileSync(index, 'utf8').match(/^Ready in (\d+)s\.$/m);
		expect(m, 'index.md has a "Ready in Ns." line').not.toBeNull();
		expect(READY).toBe(`Ready in ${m![1]}s`);

		// The machine's title shows it, and the story read aloud says it.
		const src = readFileSync(join(__dirname, 'OneCommand.svelte'), 'utf8');
		expect(src).toMatch(/class="ready[^"]*"\s*>\s*\{READY\}/);
		expect(src).toContain("ready in ${READY.replace(/\\D/g, '')} seconds");
	});
});
