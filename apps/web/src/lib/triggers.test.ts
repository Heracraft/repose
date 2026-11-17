import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { TRIGGER_REASONS, reasonSentence } from './triggers';

// The reasons are one table (I-1256). The Go copy in
// internal/triggerrun is what the api writes, hostd bounds and the CLI
// prints; this holds the browser's copy to it, word for word. The first
// draft had four copies and two of them were already missing
// `clock_late`, so the most common skip on a machine that was off
// printed a raw token on two surfaces.
describe('the trigger reasons', () => {
	// vitest runs with apps/web as the root, and the Go table is four
	// directories up: a repo-relative path, not an import URL, because
	// this file is served to the browser bundle too.
	const go = readFileSync(resolve('../../internal/triggerrun/triggerrun.go'), 'utf8');
	const block = go.match(/var Sentences = map\[string\]string\{([\s\S]*?)\n\}/);
	const consts = Object.fromEntries(
		[...go.matchAll(/(Reason\w+)\s+= "([^"]+)"/g)].map((m) => [m[1], m[2]])
	);
	const fromGo = Object.fromEntries(
		[...(block?.[1] ?? '').matchAll(/(Reason\w+):\s+"([^"]+)",/g)].map((m) => [consts[m[1]], m[2]])
	);

	it('reads the Go table', () => {
		expect(Object.keys(fromGo).length).toBeGreaterThan(20);
	});

	it('says the same words as the Go table, for every reason', () => {
		expect(TRIGGER_REASONS).toEqual(fromGo);
	});

	it('keeps a gate’s own line after the colon', () => {
		expect(reasonSentence('gated: no signature header')).toBe(
			'the gate refused it: no signature header'
		);
	});

	it('prints a reason a newer api knows and this build does not', () => {
		expect(reasonSentence('something_new')).toBe('something_new');
		expect(reasonSentence(undefined)).toBe('');
	});
});
