// What the landing shows of the docs, read from the docs at build time so
// the two cannot drift (DECISIONS I-837): the Quickstart's `repose run`
// session for the hero terminal, the restart time for the FAQ, and the
// agents table for "Agents on first boot". A change of shape in either
// file fails Terminal.test.ts and the build, never the page in silence.
const files = import.meta.glob(['/src/content/docs/index.md', '/src/content/docs/agents.md'], {
	query: '?raw',
	import: 'default',
	eager: true
}) as Record<string, string>;

const INDEX = files['/src/content/docs/index.md'];
const AGENTS = files['/src/content/docs/agents.md'];

export interface Session {
	/** The commands, without their `$ `. */
	commands: string[];
	/** The output lines, as the CLI prints them. */
	output: string[];
}

/** The fenced blocks of one `## ` section, in order. */
function blocks(md: string, heading: string): string[][] {
	const start = md.indexOf(`\n## ${heading}\n`);
	if (start < 0) throw new Error(`index.md has no "## ${heading}" section`);
	const rest = md.slice(start + 1);
	const end = rest.indexOf('\n## ', 1);
	const section = end < 0 ? rest : rest.slice(0, end);
	const out: string[][] = [];
	const fence = /^```[a-z]*\n([\s\S]*?)^```$/gm;
	for (let m = fence.exec(section); m; m = fence.exec(section)) {
		out.push(m[1].replace(/\n$/, '').split('\n'));
	}
	return out;
}

/** The Quickstart's "4. Let it loose": its command block, then its output block. */
export function parseSession(md: string): Session {
	const [commands, output] = blocks(md, '4. Let it loose');
	if (!commands?.length || !output?.length) {
		throw new Error('index.md "4. Let it loose" lost its command or output block');
	}
	return { commands, output };
}

/** "about 12 seconds": the restart time index.md gives for a stopped machine. */
export function parseRestart(md: string): string {
	const m = md.match(/starts it again in (about \d+ seconds)/);
	if (!m) throw new Error('index.md lost "starts it again in about N seconds"');
	return m[1];
}

export interface Agent {
	name: string;
	/** Its `--agent` value. */
	flag: string;
}

/** The first table of agents.md: each agent's name and `--agent` value. */
export function parseAgents(md: string): Agent[] {
	const cells = (l: string) =>
		l
			.split('|')
			.slice(1, -1)
			.map((c) => c.trim());
	const lines = md.split('\n');
	const head = lines.findIndex((l) => {
		const c = cells(l);
		return c[0] === 'Agent' && c[1] === '`--agent`';
	});
	if (head < 0) throw new Error('agents.md lost its Agent | `--agent` table');
	const agents: Agent[] = [];
	// The rows under the header and its |---| line, up to the table's end.
	for (const l of lines.slice(head + 2)) {
		if (!l.startsWith('|')) break;
		const c = cells(l);
		const flag = c[1]?.match(/^`([^`]+)`$/)?.[1];
		if (!c[0] || !flag) throw new Error(`agents.md: a row without a name or \`--agent\`: ${l}`);
		agents.push({ name: c[0], flag });
	}
	if (!agents.length) throw new Error('agents.md: the agents table is empty');
	return agents;
}

export const SESSION = parseSession(INDEX);
export const RESTART = parseRestart(INDEX);
export const AGENTS_LIST = parseAgents(AGENTS);
