import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { AGENTS_LIST, RESTART, SESSION, parseAgents, parseRestart, parseSession } from './session';

const docs = join(__dirname, '..', '..', '..', 'content', 'docs');
const index = readFileSync(join(docs, 'index.md'), 'utf8');
const agents = readFileSync(join(docs, 'agents.md'), 'utf8');

// DECISIONS I-837: the hero terminal replays the Quickstart's `repose run`
// session, read from index.md at build time; the FAQ's restart time and
// the agents list come from the docs the same way. When the docs change
// shape, this fails before the build ships a landing that says otherwise.
describe('the hero terminal', () => {
	it("replays index.md's two commands, then its output, ending at the ready time", () => {
		expect(SESSION.commands).toEqual([
			'cd ~/code/your-project',
			expect.stringMatching(/^repose run -p ".+"$/)
		]);
		const ready = index.match(/^Ready in \d+s\.$/m);
		expect(ready, 'index.md has a "Ready in Ns." line').not.toBeNull();
		expect(SESSION.output.at(-1)).toBe(ready![0]);
		expect(SESSION.output.length).toBeGreaterThan(1);
		for (const line of SESSION.output) expect(index).toContain(line);
	});

	it('parses the same session from the file on disk', () => {
		expect(parseSession(index)).toEqual(SESSION);
	});

	it('fails loudly when the section loses its blocks', () => {
		expect(() => parseSession('## 4. Let it loose\n\nNo code here.\n')).toThrow();
		expect(() => parseSession('# Nothing\n')).toThrow();
	});
});

describe('the FAQ', () => {
	it("gives index.md's restart time", () => {
		expect(RESTART).toMatch(/^about \d+ seconds$/);
		expect(index).toContain(`starts it again in ${RESTART} with your files where you left them`);
		expect(parseRestart(index)).toBe(RESTART);
		expect(() => parseRestart('starts again soon')).toThrow();
	});
});

describe('the agents list', () => {
	it("is agents.md's table, name and --agent value", () => {
		expect(AGENTS_LIST).toEqual(parseAgents(agents));
		expect(AGENTS_LIST[0]).toEqual({ name: 'Claude Code', flag: 'claude' });
		expect(AGENTS_LIST.length).toBeGreaterThanOrEqual(5);
		for (const a of AGENTS_LIST)
			expect(agents).toMatch(new RegExp(`\\| ${a.name}\\s+\\| \`${a.flag}\``));
	});
});
