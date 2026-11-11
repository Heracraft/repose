// The docs as plain markdown for agents (DECISIONS I-412): /llms.txt lists
// every page, /docs/<slug>.md is one page's source with its links made
// absolute. The machine guide points an agent on a guest here, so a command
// it suggests matches the docs instead of its memory.
import { DOCS, docsBySection, type Doc } from './docs';

export const ORIGIN = 'https://repose.run';

/** A docs link, /docs/sync#where, as the .md page an agent can fetch. */
export function absoluteLinks(body: string): string {
	return body.replace(
		/\]\(\/docs(?:\/([a-z0-9-]+))?(#[^)\s]*)?\)/g,
		(_, slug: string | undefined, anchor = '') => `](${ORIGIN}/docs/${slug ?? 'index'}.md${anchor})`
	);
}

export function docMarkdown(doc: Doc): string {
	const status = doc.experimental
		? '\n\nExperimental: this setup is new and can change or stop working.'
		: '';
	return `# ${doc.title}\n\n> ${doc.description}${status}\n\n${absoluteLinks(doc.body.trim())}\n`;
}

export function llmsTxt(): string {
	const lines = [
		'# repose',
		'',
		"> A persistent NixOS machine per project where coding agents keep working after your laptop closes. These pages describe the latest release of the `repose` CLI; `repose --version` prints the one installed, and on a repose machine `~/.repose/cli-version` holds the version on the user's laptop.",
		''
	];
	for (const { section, docs } of docsBySection()) {
		lines.push(`## ${section}`, '');
		for (const d of docs)
			lines.push(`- [${d.title}](${ORIGIN}/docs/${d.slug}.md): ${d.description}`);
		lines.push('');
	}
	return lines.join('\n');
}

export const SLUGS = DOCS.map((d) => d.slug);
