import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { FEEDBACK_URL, SUPPORT_EMAIL, SUPPORT_MAILTO } from './links';

describe('links', () => {
	it('builds the support mailto from the one address', () => {
		expect(SUPPORT_MAILTO).toBe(`mailto:${SUPPORT_EMAIL}`);
		expect(SUPPORT_EMAIL).toBe('support@herakraft.co');
	});

	it('keeps the feedback board separate from the support door', () => {
		expect(FEEDBACK_URL).not.toContain(SUPPORT_EMAIL);
	});
});

// CLAUDE.md, "Names are fixed": a second support address is exactly the
// failure that rule exists for, and a grep would then find half the uses.
// Polar's own support@polar.sh in the refunds page is theirs, not ours.
describe('the product has one support address', () => {
	const root = join(__dirname, '..');

	function* files(dir: string): Generator<string> {
		for (const e of readdirSync(dir, { withFileTypes: true })) {
			const p = join(dir, e.name);
			if (e.isDirectory()) yield* files(p);
			else if (/\.(svelte|ts|md)$/.test(e.name)) yield p;
		}
	}

	it('offers one mailto, and no second support address', () => {
		const mailtos = new Set<string>();
		const supports = new Set<string>();
		for (const f of files(root)) {
			if (f.endsWith('links.test.ts')) continue;
			const text = readFileSync(f, 'utf8');
			for (const m of text.matchAll(/mailto:([\w.+-]+@[\w.-]+)/g)) mailtos.add(m[1]);
			for (const m of text.matchAll(/\b(support|help|contact)@[\w-]+(?:\.[\w-]+)+/g))
				supports.add(m[0].replace(/\.$/, ''));
		}
		// Every door the product offers is this one.
		expect([...mailtos].sort()).toEqual([SUPPORT_EMAIL]);
		// Polar answers for the payment itself; that address is theirs.
		expect([...supports].sort()).toEqual([SUPPORT_EMAIL, 'support@polar.sh']);
	});
});
