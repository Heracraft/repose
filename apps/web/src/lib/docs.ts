// The user docs: src/content/docs/*.md, each with a small frontmatter block
// (title, description, section, order, and status: experimental). Bundled at build time with
// import.meta.glob, so /docs needs no server and no fetch.
import { Marked, type Tokens } from 'marked';
import { StreamLanguage, type Language } from '@codemirror/language';
import { json } from '@codemirror/legacy-modes/mode/javascript';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { toml } from '@codemirror/legacy-modes/mode/toml';
import { classHighlighter, highlightCode, tags } from '@lezer/highlight';
import { nixLanguage } from '@replit/codemirror-lang-nix';
import { codespan } from './codespan';

export interface DocHeading {
	depth: number;
	text: string;
	id: string;
	/**
	 * The heading as the page's "On this page" rail shows it: the text, or
	 * for a command heading (### `repose run [PROJECT]`) the command's words
	 * without its arguments, "repose run", so a 180px rail lists one per row.
	 */
	label: string;
}

export interface Doc {
	slug: string;
	title: string;
	description: string;
	section: string;
	order: number;
	/** frontmatter `status: experimental`: the page says the feature may change or break. */
	experimental: boolean;
	body: string;
	html: string;
	headings: DocHeading[];
	/** Plain text of the page, for search snippets. */
	plain: string;
	/** The same, lowercased, for matching. */
	text: string;
}

/** The sections in the order the sidebar shows them. */
export const SECTIONS = ['Start here', 'Using repose', 'Tutorials', 'Account', 'Reference'];

const files = import.meta.glob('../content/docs/*.md', {
	query: '?raw',
	import: 'default',
	eager: true
}) as Record<string, string>;

function parseFrontmatter(text: string): { meta: Record<string, string>; body: string } {
	const match = /^---\n([\s\S]*?)\n---\n([\s\S]*)$/.exec(text);
	if (!match) return { meta: {}, body: text };
	const meta: Record<string, string> = {};
	for (const line of match[1].split('\n')) {
		const i = line.indexOf(':');
		if (i === -1) continue;
		meta[line.slice(0, i).trim()] = line.slice(i + 1).trim();
	}
	return { meta, body: match[2] };
}

/** A heading's anchor: lowercase words joined by dashes, punctuation dropped. */
export function slugify(text: string): string {
	return text
		.toLowerCase()
		.replace(/<[^>]+>/g, '')
		.replace(/&[a-z#0-9]+;/g, '')
		.replace(/['’]/g, '')
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '');
}

function plain(markdown: string): string {
	return (
		markdown
			.replace(/<!--[\s\S]*?-->/g, ' ')
			.replace(/```[\s\S]*?```/g, ' ')
			// A table's rule row ("| --- | :---: |") is markup, not words.
			.replace(/^\s*\|?(\s*:?-{3,}:?\s*\|)+\s*:?-*:?\s*$/gm, ' ')
			.replace(/`([^`]*)`/g, '$1')
			.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
			.replace(/^\s*[#>|-]+/gm, ' ')
			.replace(/[*|]/g, ' ')
			.replace(/\s+/g, ' ')
			.trim()
	);
}

function escapeHTML(text: string): string {
	return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/**
 * Grammars for fenced blocks. A block with no language is shell; ```text is
 * program output and stays plain, with no copy button.
 */
const PARSERS: Record<string, Language['parser']> = {
	nix: nixLanguage.parser,
	shell: StreamLanguage.define(shell).parser,
	toml: StreamLanguage.define(toml).parser,
	json: StreamLanguage.define({ ...json, tokenTable: { property: tags.propertyName } }).parser
};

/**
 * Code highlighted with the given grammar. Tokens get lezer's `tok-*`
 * classes; layout.css colours them.
 */
export function highlight(code: string, lang: string): string {
	const parser = PARSERS[lang];
	if (!parser) return escapeHTML(code);
	let out = '';
	highlightCode(
		code,
		parser.parse(code),
		classHighlighter,
		(text, classes) => {
			out += classes ? `<span class="${classes}">${escapeHTML(text)}</span>` : escapeHTML(text);
		},
		() => (out += '\n')
	);
	return out;
}

/** A ```nix block, highlighted with the same grammar as the dashboard's Nix editor. */
export function highlightNix(code: string): string {
	return highlight(code, 'nix');
}

/**
 * A shell block. When some lines start with `$ `, those are commands and the
 * rest is their output: the prompt and the output are dimmed and left out of
 * the copy. Otherwise every line is a command.
 */
export function renderShell(code: string): { html: string; copy: string } {
	const lines = code.split('\n');
	if (!lines.some((l) => l.startsWith('$ '))) return { html: highlight(code, 'shell'), copy: code };
	const html = lines
		.map((l) =>
			l.startsWith('$ ')
				? `<span class="prompt">$ </span>${highlight(l.slice(2), 'shell')}`
				: l && `<span class="output">${escapeHTML(l)}</span>`
		)
		.join('\n');
	const copy = lines
		.filter((l) => l.startsWith('$ '))
		.map((l) => l.slice(2))
		.join('\n');
	return { html, copy };
}

/**
 * A code block with a copy button over its top right corner; ```text has
 * none. The first line is its own span: on a phone the button sits over the
 * block's corner, and the span's end padding lets that line scroll clear of
 * it (DocPage). No highlight span crosses a line break, so the first
 * newline is always between spans.
 */
function codeBlock(lang: string, code: string): string {
	if (lang === 'text') {
		return `<pre><code class="language-text">${escapeHTML(code)}</code></pre>\n`;
	}
	const { html, copy } =
		lang === 'shell' ? renderShell(code) : { html: highlight(code, lang), copy: code };
	const nl = html.indexOf('\n');
	const lines =
		nl === -1
			? `<span class="l1">${html}</span>`
			: `<span class="l1">${html.slice(0, nl)}</span>${html.slice(nl)}`;
	return `<div class="code"><pre><code class="language-${lang}">${lines}</code></pre><button type="button" class="copy" data-copy="${escapeHTML(copy).replace(/"/g, '&quot;')}">Copy</button></div>\n`;
}

/**
 * A table cell that is a figure: a number with its unit, a price, a
 * percentage ("100 GB", "$29*", "4 vCPUs"). It gets class `num`, which
 * keeps it on one line in tabular figures, so "100" never ends a line with
 * "GB" on the next.
 */
export const FIGURE = /^\$?[\d.,]+\s?(GB|TB|MB|KB|vCPUs?|%|ms|s|h)?\*?$/;

function markFigures(html: string): string {
	const marked = html.replace(
		/<(td|th)((?: align="[a-z]+")?)>([^<]*)<\/\1>/g,
		(cell, tag: string, attrs: string, text: string) =>
			FIGURE.test(text.trim()) ? `<${tag}${attrs} class="num">${text}</${tag}>` : cell
	);
	return marked.replace(/<table>[\s\S]*?<\/table>/g, alignFigureHeads);
}

/**
 * A column whose every body cell is a figure sits right, so its heading
 * sits right with it: "Cores" over a right-aligned "1" read as a heading
 * for the column before it.
 */
function alignFigureHeads(table: string): string {
	const body = table.split('<tbody>')[1];
	if (!body) return table;
	const rows = body.split('</tr>').filter((r) => r.includes('<td'));
	const cells = rows.map((r) => r.match(/<td[^>]*>/g) ?? []);
	const numeric = (i: number) =>
		cells.length > 0 && cells.every((r) => r[i]?.includes('class="num"'));
	let i = 0;
	return table.replace(/<th((?: align="[a-z]+")?)( class="num")?>/g, (th, attrs: string, num) => {
		const col = i++;
		return num || !numeric(col) ? th : `<th${attrs} class="num">`;
	});
}

/** A command heading's words before its first argument: "repose mcp forward". */
function railLabel(inner: string, text: string): string {
	const html = inner.trim();
	if (!/^<code[ >][\s\S]*<\/code>$/.test(html) || html.split('<code').length !== 2) return text;
	const words: string[] = [];
	for (const w of text.trim().split(/\s+/)) {
		if (!/^[a-z][a-z0-9-]*$/.test(w)) break;
		words.push(w);
	}
	return words.length ? words.join(' ') : text;
}

/** Heading text for the page's own list: tags dropped, entities decoded. */
function headingText(html: string): string {
	return html
		.replace(/<[^>]+>/g, '')
		.replace(/&#39;/g, "'")
		.replace(/&quot;/g, '"')
		.replace(/&lt;/g, '<')
		.replace(/&gt;/g, '>')
		.replace(/&amp;/g, '&');
}

export { NOBREAK_MAX } from './codespan';

function render(body: string): { html: string; headings: DocHeading[] } {
	const headings: DocHeading[] = [];
	const seen = new Map<string, number>();
	const marked = new Marked({
		renderer: {
			heading(
				this: { parser: { parseInline(t: Tokens.Generic[]): string } },
				token: Tokens.Heading
			) {
				const inner = this.parser.parseInline(token.tokens);
				let id = slugify(token.text);
				const n = seen.get(id) ?? 0;
				seen.set(id, n + 1);
				if (n) id = `${id}-${n}`;
				if (token.depth === 2 || token.depth === 3) {
					const text = headingText(inner);
					headings.push({ depth: token.depth, text, id, label: railLabel(inner, text) });
				}
				return `<h${token.depth} id="${id}"><a class="anchor" href="#${id}" aria-hidden="true" tabindex="-1">#</a>${inner}</h${token.depth}>\n`;
			},
			code(token: Tokens.Code) {
				const lang = token.lang || 'shell';
				if (!(lang in PARSERS) && lang !== 'text') return false;
				return codeBlock(lang, token.text);
			},
			codespan,
			blockquote(
				this: { parser: { parse(t: Tokens.Generic[]): string } },
				token: Tokens.Blockquote
			) {
				return `<aside class="note">${this.parser.parse(token.tokens)}</aside>\n`;
			}
		}
	});
	let html = marked.parse(body) as string;
	// Wide tables scroll inside their own box, never the page.
	html = markFigures(
		html
			.replace(/<table>/g, '<div class="table-wrap"><table>')
			.replace(/<\/table>/g, '</table></div>')
	);
	return { html, headings };
}

export const DOCS: Doc[] = Object.entries(files)
	.map(([path, raw]) => {
		const slug = path.split('/').pop()!.replace(/\.md$/, '');
		const { meta, body } = parseFrontmatter(raw);
		const { html, headings } = render(body);
		const plainText = plain(`${meta.title ?? ''}. ${meta.description ?? ''} ${body}`);
		return {
			slug,
			title: meta.title ?? slug,
			description: meta.description ?? '',
			section: meta.section ?? 'Reference',
			order: Number(meta.order ?? 99),
			experimental: meta.status === 'experimental',
			body,
			html,
			headings,
			plain: plainText,
			text: plainText.toLowerCase()
		};
	})
	.sort(
		(a, b) =>
			SECTIONS.indexOf(a.section) - SECTIONS.indexOf(b.section) ||
			a.order - b.order ||
			a.title.localeCompare(b.title)
	);

export function docBySlug(slug: string): Doc | undefined {
	return DOCS.find((d) => d.slug === slug);
}

/**
 * Pages that moved, old slug to new, so links already shared keep working.
 * 'index' is the overview at /docs.
 */
export const MOVED: Record<string, string> = {
	'tutorial-your-chrome': 'your-chrome', // DECISIONS I-316
	quickstart: 'index', // the overview is the Quickstart
	pricing: 'billing' // plans and prices are on Plans and billing
};

export function movedDoc(slug: string): string | undefined {
	return MOVED[slug];
}

/** The docs grouped by section, in sidebar order. */
export function docsBySection(): { section: string; docs: Doc[] }[] {
	return SECTIONS.map((section) => ({
		section,
		docs: DOCS.filter((d) => d.section === section)
	})).filter((g) => g.docs.length);
}

export interface SearchHit {
	doc: Doc;
	heading?: DocHeading;
	snippet: string;
	/** The snippet cut into runs; a run that is one of the query's words has `hit`. */
	parts: { text: string; hit: boolean }[];
}

function escapeRegExp(text: string): string {
	return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Every word of the query must appear on the page. Hits in a title rank
 * first, then in a heading, then in the text; the snippet is the text
 * around the first word's first match, cut at word edges.
 */
export function search(query: string, limit = 8): SearchHit[] {
	const words = query.toLowerCase().split(/\s+/).filter(Boolean);
	if (!words.length) return [];
	const marks = new RegExp(`(${words.map(escapeRegExp).join('|')})`, 'gi');
	const hits: { hit: SearchHit; score: number }[] = [];
	for (const doc of DOCS) {
		if (!words.every((w) => doc.text.includes(w))) continue;
		let score = 0;
		const title = doc.title.toLowerCase();
		for (const w of words) if (title.includes(w)) score += 10;
		const heading = doc.headings.find((h) => words.some((w) => h.text.toLowerCase().includes(w)));
		if (heading) score += 5;
		const at = doc.text.indexOf(words[0]);
		let start = Math.max(0, at - 50);
		if (start > 0) {
			const space = doc.plain.indexOf(' ', start);
			start = space >= 0 && space < at ? space + 1 : at;
		}
		let end = Math.min(doc.plain.length, at + 90);
		if (end < doc.plain.length) {
			const space = doc.plain.lastIndexOf(' ', end);
			if (space > at) end = space;
		}
		const snippet =
			(start > 0 ? '…' : '') +
			doc.plain.slice(start, end).trim() +
			(end < doc.plain.length ? '…' : '');
		const parts = snippet
			.split(marks)
			.filter(Boolean)
			.map((text) => ({ text, hit: words.includes(text.toLowerCase()) }));
		hits.push({ hit: { doc, heading, snippet, parts }, score });
	}
	return hits
		.sort((a, b) => b.score - a.score)
		.slice(0, limit)
		.map((h) => h.hit);
}

/** The length of the prefix two strings share. */
function sharedPrefix(a: string, b: string): number {
	let n = 0;
	while (n < a.length && n < b.length && a[n] === b[n]) n++;
	return n;
}

/**
 * The docs 404's "Closest pages" (Guide B 12.22): up to `limit` pages whose
 * slug, title or one of their words shares the longest prefix with the slug
 * asked for, at least its first four letters (all of it when shorter), in
 * sidebar order among equals. No page close enough gives the Quickstart.
 */
export function closestDocs(slug: string, limit = 6): Doc[] {
	const want = slugify(slug);
	const floor = Math.min(4, want.length);
	const scored: { doc: Doc; score: number; at: number }[] = [];
	DOCS.forEach((doc, at) => {
		const title = slugify(doc.title);
		// The whole slug or title beats one of its words by half a letter,
		// so /docs/agent gives Agents before "Lend the agents your Chrome";
		// words of three letters or fewer ("and", "the") match nothing.
		const whole = Math.max(sharedPrefix(want, doc.slug), sharedPrefix(want, title));
		const words = [...doc.slug.split('-'), ...title.split('-')].filter((w) => w.length > 3);
		const part = Math.max(0, ...words.map((k) => sharedPrefix(want, k)));
		const score = whole >= part ? whole + (whole ? 0.5 : 0) : part;
		if (floor > 0 && score >= floor) scored.push({ doc, score, at });
	});
	if (!scored.length) return DOCS.filter((d) => d.slug === 'index');
	return scored
		.sort((a, b) => b.score - a.score || a.at - b.at)
		.slice(0, limit)
		.map((s) => s.doc);
}
