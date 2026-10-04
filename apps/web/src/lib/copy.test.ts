import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';
import { unaskedIn } from './unasked';

const web = join(__dirname, '..', '..');
const repo = join(web, '..', '..');

// Legal pages are legal text, held to their own review; the docs pages are
// markdown, checked in docs.test.ts.
const skip = [/\/routes\/(privacy|terms|refunds|docs)\//];

function files(dir: string, ext: RegExp): string[] {
	return readdirSync(dir).flatMap((name) => {
		const p = join(dir, name);
		if (statSync(p).isDirectory()) return files(p, ext);
		return ext.test(name) && !skip.some((s) => s.test(p)) ? [p] : [];
	});
}

// Every screen, component and email gives only help the reader asked for
// (DECISIONS I-485): no reassurance, no narrating the screen, no telling
// the reader what comes next.
describe('site copy and emails', () => {
	it('ask nothing of the reader they did not ask for', () => {
		const all = [
			...files(join(web, 'src', 'routes'), /\.svelte$/),
			...files(join(web, 'src', 'lib', 'components'), /\.svelte$/),
			...files(join(repo, 'internal', 'api', 'notify', 'templates'), /\.(html|txt|tmpl)$/)
		];
		expect(all.length).toBeGreaterThan(20);
		const hits = all.flatMap((p) => unaskedIn(relative(repo, p), readFileSync(p, 'utf8')));
		expect(hits).toEqual([]);
	});
});
