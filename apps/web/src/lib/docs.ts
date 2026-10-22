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

/**
 * One row of the docs search palette (Guide B 12.14): a page, one of its
 * h2 or h3 headings, or an item named in code in the first cell of a table
 * row under a heading (a CLI flag, a config.toml key, an environment
 * variable, a state, a notification's title).
 */
export interface SearchEntry {
	kind: 'page' | 'heading' | 'item';
	label: string;
	/** Where it sits, shown on the right: a page's section, a heading's page, an item's command or page. */
	context: string;
	slug: string;
	/** The heading the entry lands on; a page has none. */
	id?: string;
	/** Set in the code face: a command heading or an item. */
	mono: boolean;
	/** The section's own text, plain, for a match in the body and its snippet. */
	body: string;
}

const lexer = new Marked();

/** The page, its sections and its table items, in reading order. */
function entriesFor(doc: Doc): SearchEntry[] {
	const page: SearchEntry = {
		kind: 'page',
		label: doc.title,
		context: doc.section,
		slug: doc.slug,
		mono: false,
		body: ''
	};
	const out: SearchEntry[] = [page];
	const seen = new Map<string, number>();
	let owner = page;
	let words: string[] = [doc.description];
	let under: { id: string; context: string } | undefined;
	const flush = () => {
		owner.body = plain(words.join('\n\n'));
	};
	for (const token of lexer.lexer(doc.body)) {
		if (token.type === 'heading') {
			// The same ids render() gives, counted the same way.
			let id = slugify(token.text);
			const n = seen.get(id) ?? 0;
			seen.set(id, n + 1);
			if (n) id = `${id}-${n}`;
			const heading = doc.headings.find((h) => h.id === id);
			if (heading) {
				flush();
				const command = heading.label !== heading.text;
				owner = {
					kind: 'heading',
					label: heading.text,
					context: doc.title,
					slug: doc.slug,
					id,
					mono: command,
					body: ''
				};
				out.push(owner);
				words = [];
				under = { id, context: command ? heading.label : doc.title };
				continue;
			}
		}
		if (token.type === 'table') {
			for (const row of (token as Tokens.Table).rows) {
				const cell = row[0]?.text.trim() ?? '';
				if (!/^`[^`]+`(\s*,\s*`[^`]+`)*$/.test(cell)) continue;
				const label = cell.replace(/`/g, '');
				if (label.length < 2) continue;
				out.push({
					kind: 'item',
					label,
					context: under?.context ?? doc.title,
					slug: doc.slug,
					id: under?.id,
					mono: true,
					body: ''
				});
			}
		}
		words.push(token.raw);
	}
	flush();
	return out;
}

/** Every entry of every page, in sidebar order. Built once, at build time for the prerendered docs. */
export const SEARCH: SearchEntry[] = DOCS.flatMap(entriesFor);

/** A run of text; `hit` when it is one of the query's words. */
export interface Part {
	text: string;
	hit: boolean;
}

export interface SearchResult {
	entry: SearchEntry;
	/** The label cut into runs, the query's words marked. */
	label: Part[];
	/** The context, marked the same way, so a match there shows why the row is listed. */
	context: Part[];
	/** A line of the section's text around the match, only when the match is in the text. */
	snippet?: Part[];
}

/**
 * A common English ending dropped, so "pagination" finds "paginated" and
 * "secrets" finds "secret". For the body only.
 */
function stem(term: string): string {
	const s = term.replace(/(ation|ing|ed|es|s)$/, '');
	return s.length >= 4 ? s : term;
}

const KIND_BONUS = { page: 3, heading: 1, item: 0 } as const;

const WORD = /[a-z0-9]/;

/** True when `term` (or its stem) starts a word somewhere in `text`. */
function wordStart(text: string, term: string): boolean {
	return [term, stem(term)].some((t) => new RegExp(`(^|[^a-z0-9])${escapeRegExp(t)}`).test(text));
}

/**
 * Title over heading over item, and a match in the label over one in the
 * text: the whole label 100; a label that starts with the query 90 when it
 * ends a word there ("Ports" for "ports"), 85 when it does not ("Ports" for
 * "port"); whole words inside it 80, the start of a word 60, inside a word
 * 50; every word somewhere in it 40; the label and its context together 20.
 * The section's text gives 10 when every word starts a word there and 5
 * when one sits inside another word ("port" in "supported"); a heading's
 * text adds 1 and a page's intro nothing, so the section about the term
 * comes before an intro that mentions it. -1 is no match.
 */
function score(e: SearchEntry, terms: string[]): number {
	const label = e.label.toLowerCase();
	const hay = `${label} ${e.context.toLowerCase()}`;
	const bonus = KIND_BONUS[e.kind];
	if (terms.every((t) => hay.includes(t))) {
		const q = terms.join(' ');
		if (label === q) return 100 + bonus;
		const at = label.indexOf(q);
		if (at >= 0) {
			const starts = at === 0 || !WORD.test(label[at - 1]);
			const ends = !WORD.test(label[at + q.length] ?? ' ');
			if (at === 0) return (ends ? 90 : 85) + bonus;
			return (starts ? (ends ? 80 : 60) : 50) + bonus;
		}
		return (terms.every((t) => label.includes(t)) ? 40 : 20) + bonus;
	}
	const body = `${hay} ${e.body.toLowerCase()}`;
	if (!e.body || !terms.every((t) => body.includes(t) || body.includes(stem(t)))) return -1;
	return (terms.every((t) => wordStart(body, t)) ? 10 : 5) + (e.kind === 'page' ? 0 : 1);
}

function labelHasAll(label: string, terms: string[]): boolean {
	const l = label.toLowerCase();
	return terms.every((t) => l.includes(t));
}

function escapeRegExp(text: string): string {
	return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Text cut into runs, each of `marks` (longest first) marked wherever it occurs. */
export function markParts(text: string, marks: string[]): Part[] {
	const words = [...new Set(marks.filter(Boolean).map((m) => m.toLowerCase()))].sort(
		(a, b) => b.length - a.length
	);
	if (!words.length) return [{ text, hit: false }];
	const re = new RegExp(`(${words.map(escapeRegExp).join('|')})`, 'gi');
	return text
		.split(re)
		.filter(Boolean)
		.map((t) => ({ text: t, hit: words.includes(t.toLowerCase()) }));
}

/** The section's text from a little before the first match, cut at a word edge; the row's CSS ends it. */
function snippetOf(body: string, marks: string[]): Part[] | undefined {
	const lower = body.toLowerCase();
	const at = Math.min(...marks.map((m) => lower.indexOf(m)).filter((i) => i >= 0));
	if (!Number.isFinite(at)) return undefined;
	let start = Math.max(0, at - 28);
	if (start > 0) {
		const space = body.indexOf(' ', start);
		start = space >= 0 && space < at ? space + 1 : at;
	}
	const text = (start > 0 ? '…' : '') + body.slice(start, start + 160);
	return markParts(text, marks);
}

/**
 * The palette's rows for a query, best first, ties in reading order. Every
 * word of the query must be in the entry's label and context, or in its
 * section's text. An empty query lists every page.
 */
export function searchDocs(query: string, limit = 20): SearchResult[] {
	const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
	if (!terms.length) {
		return SEARCH.filter((e) => e.kind === 'page').map((entry) => ({
			entry,
			label: [{ text: entry.label, hit: false }],
			context: [{ text: entry.context, hit: false }]
		}));
	}
	const marks = [...terms, ...terms.map(stem)];
	return SEARCH.map((entry, i) => ({ entry, s: score(entry, terms), i }))
		.filter((r) => r.s >= 0)
		.sort((a, b) => b.s - a.s || a.i - b.i)
		.slice(0, limit)
		.map(({ entry, s }) => ({
			entry,
			label: markParts(entry.label, s >= 20 ? terms : marks),
			// The context is marked only when the label alone does not
			// explain the row, so a page's twelve headings do not each
			// mark its title.
			context: labelHasAll(entry.label, terms)
				? [{ text: entry.context, hit: false }]
				: markParts(entry.context, s >= 20 ? terms : marks),
			snippet: s < 20 ? snippetOf(entry.body, marks) : undefined
		}));
}

/** Edit distance with a swap of two neighbours as one edit, stopping past `max`. */
function editDistance(a: string, b: string, max: number): number {
	if (Math.abs(a.length - b.length) > max) return max + 1;
	const d: number[][] = Array.from({ length: a.length + 1 }, (_, i) =>
		Array.from({ length: b.length + 1 }, (_, j) => (i === 0 ? j : j === 0 ? i : 0))
	);
	for (let i = 1; i <= a.length; i++) {
		let low = Infinity;
		for (let j = 1; j <= b.length; j++) {
			const cost = a[i - 1] === b[j - 1] ? 0 : 1;
			d[i][j] = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + cost);
			if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1])
				d[i][j] = Math.min(d[i][j], d[i - 2][j - 2] + 1);
			low = Math.min(low, d[i][j]);
		}
		if (low > max) return max + 1;
	}
	return d[a.length][b.length];
}

/**
 * The palette's rows when searchDocs finds nothing, so a typo ("snapshto")
 * is not a dead end: labels where each word of the query of four letters or
 * more is one edit from a word (two from a word of seven or more letters),
 * and each shorter word is in the label as typed. Fewest edits first, then
 * title over heading over item, ties in reading order.
 */
export function nearDocs(query: string, limit = 8): SearchResult[] {
	const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
	if (!terms.length || !terms.some((t) => t.length >= 4)) return [];
	const found: { entry: SearchEntry; cost: number; i: number }[] = [];
	SEARCH.forEach((entry, i) => {
		const label = entry.label.toLowerCase();
		const words = label.split(/[^a-z0-9_]+/).filter(Boolean);
		let cost = 0;
		for (const t of terms) {
			if (t.length < 4) {
				if (!label.includes(t)) return;
				continue;
			}
			const max = t.length >= 7 ? 2 : 1;
			// A word is compared whole and cut to the term's length, so
			// "snapshto" finds "Snapshots" as well as "snapshot".
			const best = Math.min(
				...words.map((w) =>
					Math.min(editDistance(t, w, max), editDistance(t, w.slice(0, t.length), max))
				)
			);
			if (best > max) return;
			cost += best;
		}
		found.push({ entry, cost, i });
	});
	return found
		.sort(
			(a, b) => a.cost - b.cost || KIND_BONUS[b.entry.kind] - KIND_BONUS[a.entry.kind] || a.i - b.i
		)
		.slice(0, limit)
		.map(({ entry }) => ({
			entry,
			label: [{ text: entry.label, hit: false }],
			context: [{ text: entry.context, hit: false }]
		}));
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
