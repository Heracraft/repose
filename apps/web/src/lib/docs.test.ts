import { describe, expect, it } from 'vitest';
import {
	DOCS,
	FIGURE,
	MOVED,
	NOBREAK_MAX,
	SECTIONS,
	closestDocs,
	docBySlug,
	highlightNix,
	movedDoc,
	renderShell,
	SEARCH,
	searchDocs,
	nearDocs,
	slugify
} from './docs';
import { unaskedIn } from './unasked';

// Every /docs link in the docs names a page that exists and, when it has a
// #fragment, a heading on that page: a renamed heading otherwise breaks
// links silently.
describe('user docs', () => {
	it('has an overview and puts every page in a known section', () => {
		expect(docBySlug('index')).toBeDefined();
		for (const d of DOCS) {
			expect(SECTIONS, d.slug).toContain(d.section);
			expect(d.title, d.slug).not.toBe(d.slug);
			expect(d.description, d.slug).not.toBe('');
		}
	});

	it('links only to pages and headings that exist', () => {
		const broken: string[] = [];
		for (const d of DOCS) {
			for (const [, slug, anchor] of d.body.matchAll(
				/\]\(\/docs(?:\/([a-z0-9-]+))?(?:#([a-z0-9-]+))?\)/g
			)) {
				const target = docBySlug(slug ?? 'index');
				if (!target) {
					broken.push(`${d.slug} -> /docs/${slug}`);
					continue;
				}
				if (anchor && !target.headings.some((h) => h.id === anchor)) {
					broken.push(`${d.slug} -> /docs/${slug ?? ''}#${anchor}`);
				}
			}
		}
		expect(broken).toEqual([]);
	});

	it('uses no em dashes', () => {
		for (const d of DOCS) expect(d.body.includes('—'), d.slug).toBe(false);
	});

	// Help the reader asked for is welcome; help nobody asked for is cut
	// (DECISIONS I-485). The phrasings a grep can catch are in unasked.ts.
	it('asks nothing of the reader it did not ask for', () => {
		expect(DOCS.flatMap((d) => unaskedIn(d.slug, d.body))).toEqual([]);
	});

	// Plans, not a card and not an hourly meter (DECISIONS I-289): the docs
	// send nobody to add a card and quote no price per hour. billing, cli
	// and limits are checked once their owners' rewrite lands (their pages
	// are still the hourly ones on this branch); the rest must be clean now.
	it('promises no card step, no hourly price, and has no stale release notes', () => {
		const stillHourly = new Set(['billing', 'cli', 'limits']);
		for (const d of DOCS) {
			expect(d.text.includes('add a card'), d.slug).toBe(false);
			expect(d.text.includes('not in a release yet'), d.slug).toBe(false);
			if (stillHourly.has(d.slug)) continue;
			expect(d.text.toLowerCase().includes('per hour'), d.slug).toBe(false);
			expect(/\$\d+(\.\d+)?\/h\b/.test(d.text), d.slug).toBe(false);
		}
	});

	it('gives every page its own title and a place in its section', () => {
		const titles = new Set(DOCS.map((d) => d.title));
		expect(titles.size).toBe(DOCS.length);
		const places = new Set(DOCS.map((d) => `${d.section}/${d.order}`));
		expect(places.size).toBe(DOCS.length);
	});

	it('slugs headings the way links spell them', () => {
		expect(slugify("What you'll get")).toBe('what-youll-get');
		expect(slugify('`repose open`')).toBe('repose-open');
		expect(slugify('Things that don’t work yet')).toBe('things-that-dont-work-yet');
	});

	it('finds pages, headings, flags and keys by words in them', () => {
		const top = (q: string) => searchDocs(q)[0]?.entry;
		expect(top('ntfy topic')?.slug).toBe('notifications');
		expect(searchDocs('zzzz-not-a-word')).toEqual([]);
		// A line commented out in a page is not searchable.
		expect(searchDocs('live product for other people')).toEqual([]);
		// CLI commands and flags are entries of their own (the palette's ask).
		expect(top('repose run')).toMatchObject({
			kind: 'heading',
			slug: 'cli',
			id: 'repose-run-project'
		});
		expect(top('--temp')).toMatchObject({ kind: 'item', slug: 'cli', context: 'repose run' });
		expect(top('default_size')).toMatchObject({ kind: 'item', slug: 'cli', id: 'config-toml' });
		expect(top('REPOSE_NO_FORWARD')?.label).toBe('REPOSE_NO_FORWARD=1');
		// A title beats a heading, which beats the text.
		const secret = searchDocs('secret');
		expect(secret[0].entry).toMatchObject({ kind: 'page', slug: 'secrets' });
		const tiers = secret.map((r) => (r.snippet ? 1 : 0));
		expect(tiers).toEqual([...tiers].sort());
	});

	it('ranks a label that starts with the query over one that has it inside', () => {
		const top = (q: string) => searchDocs(q)[0]?.entry;
		expect(top('snapshot')).toMatchObject({ kind: 'heading', label: 'Snapshots' });
		expect(top('port')).toMatchObject({ kind: 'heading', label: 'Ports' });
	});

	it('ranks a section about the term over an intro with it inside a word', () => {
		const rows = searchDocs('port', 100).map((r) => r.entry.label);
		const ports = rows.indexOf('Several repositories on one machine');
		const supported = rows.indexOf('Quickstart');
		expect(ports).toBeGreaterThanOrEqual(0);
		expect(supported).toBeGreaterThan(ports);
	});

	it('offers labels a typo away when nothing matches', () => {
		expect(searchDocs('snapshto')).toEqual([]);
		expect(nearDocs('snapshto')[0]?.entry.label).toBe('Snapshots');
		expect(nearDocs('memry').map((r) => r.entry.label)).toContain('Memory and disk');
		expect(nearDocs('zzzz-not-a-word')).toEqual([]);
		expect(nearDocs('ab')).toEqual([]);
	});

	it('lists every page, with its section, for an empty query', () => {
		const all = searchDocs('  ');
		expect(all.map((r) => r.entry.slug)).toEqual(DOCS.map((d) => d.slug));
		expect(all.map((r) => r.entry.context)).toEqual(DOCS.map((d) => d.section));
		expect(all.every((r) => !r.snippet && !r.label.some((p) => p.hit))).toBe(true);
	});

	it('lands every entry on a heading the page has', () => {
		for (const e of SEARCH) {
			if (e.kind === 'page') continue;
			// A table above a page's first heading lands on the page.
			if (e.id === undefined && e.kind === 'item') continue;
			const doc = docBySlug(e.slug)!;
			expect(
				doc.headings.map((h) => h.id),
				`${e.label} in ${e.slug}`
			).toContain(e.id);
		}
	});

	it('marks the query in a label, and snippets only a match in the text', () => {
		for (const q of ['snap', 'cores memory', 'secret', '--temp']) {
			for (const r of searchDocs(q)) {
				expect(r.label.map((p) => p.text).join('')).toBe(r.entry.label);
				if (r.snippet) {
					expect(
						r.snippet.some((p) => p.hit),
						`${q}: ${r.entry.label}`
					).toBe(true);
					const text = r.snippet
						.map((p) => p.text)
						.join('')
						.replace(/^…/, '');
					expect(r.entry.body).toContain(text);
					expect(text).not.toMatch(/-{3,}/);
				} else {
					const marked = [...r.label, ...r.context]
						.filter((p) => p.hit)
						.map((p) => p.text.toLowerCase());
					expect(
						q.split(' ').every((t) => marked.includes(t)),
						`${q}: ${r.entry.label}`
					).toBe(true);
				}
			}
		}
	});

	it('sends a moved page on to its new slug', () => {
		expect(movedDoc('tutorial-your-chrome')).toBe('your-chrome');
		expect(docBySlug(movedDoc('tutorial-your-chrome') ?? '')).toBeDefined();
		expect(movedDoc('your-chrome')).toBeUndefined();
		// /docs/quickstart is the overview (slug 'index', served at /docs)
		// and /docs/pricing the plans page.
		expect(movedDoc('quickstart')).toBe('index');
		expect(docBySlug('index')!.title).toBe('Quickstart');
		expect(movedDoc('pricing')).toBe('billing');
		// An old slug is never a live page's, or the redirect would hide it.
		for (const [from, to] of Object.entries(MOVED)) {
			expect(docBySlug(from), from).toBeUndefined();
			expect(docBySlug(to), to).toBeDefined();
		}
	});

	// "100 GB" never breaks between the number and its unit.
	it('marks the figures in a table', () => {
		for (const f of ['100 GB', '1 TB', '$29*', '$0.05', '4 vCPUs', '12%', '8']) {
			expect(FIGURE.test(f), f).toBe(true);
		}
		for (const f of ['Solo', 'day 3', '30 minutes', '8 GB: one large']) {
			expect(FIGURE.test(f), f).toBe(false);
		}
		const billing = docBySlug('billing')!.html;
		expect(billing).toContain('<td class="num">100 GB</td>');
		expect(billing).toContain('<td class="num">$89</td>');
		expect(billing).not.toContain('<td class="num">Solo</td>');
		// A heading over a column of figures sits right with them.
		const machine = docBySlug('machine')!.html;
		expect(machine).toContain('<th class="num">Cores</th>');
		expect(machine).toContain('<th class="num">Disk</th>');
		expect(machine).toContain('<th>Size</th>');
	});

	// The rail lists h2s and h3s; a command heading shows as its command.
	it('gives each section a label for the rail', () => {
		const cli = docBySlug('cli')!.headings;
		expect(cli.filter((h) => h.depth === 3).length).toBeGreaterThanOrEqual(30);
		const label = (id: string) => cli.find((h) => h.id === id)?.label;
		expect(label('repose-run-project')).toBe('repose run');
		expect(label('repose-mcp-forward-name')).toBe('repose mcp forward');
		expect(label('repose-cp-r-src-dst')).toBe('repose cp');
		expect(label('which-project')).toBe('Which project');
		expect(cli.find((h) => h.text === 'config.toml')?.label).toBe('config.toml');
	});

	it('puts the first line of a code block in its own span', () => {
		const html = docBySlug('lifecycle')!.html;
		expect(html).toContain('<code class="language-shell"><span class="l1">');
		// Plain output has no Copy button and no span.
		expect(docBySlug('index')!.html).not.toContain('language-text"><span class="l1">');
	});

	// The grammar's bare `parser` export has no highlight tags (they live on
	// nixLanguage), which rendered every block as plain text.
	it('highlights nix blocks', () => {
		const out = highlightNix('{ pkgs, ... }:\n{\n  # a\n  x = with pkgs; [ "s" true 1 ];\n}\n');
		for (const tok of ['tok-comment', 'tok-keyword', 'tok-string', 'tok-bool', 'tok-number']) {
			expect(out, tok).toContain(`class="${tok}"`);
		}
		expect(highlightNix('x = "<b>";')).toContain('&lt;b&gt;');
		const config = docBySlug('config')!;
		expect(config.html).toContain('<code class="language-nix"><span');
	});

	it('highlights shell, toml and json blocks and gives each a copy button', () => {
		const cli = docBySlug('cli')!.html;
		expect(cli).toContain('<code class="language-shell">');
		expect(cli).toContain('<code class="language-toml"><span');
		expect(docBySlug('agents')!.html).toContain('<code class="language-json">');
		expect(cli).toContain('<button type="button" class="copy"');
		// Program output is ```text: plain, nothing to copy.
		const index = docBySlug('index')!.html;
		expect(index).toContain('<code class="language-text">');
		expect(index.match(/class="copy"/g)?.length).toBe(
			index.match(/<code class="language-(?!text)/g)?.length
		);
	});

	it('copies only the commands of a block with prompts', () => {
		const { html, copy } = renderShell(
			'$ repose ls\nPROJECT  STATE\n\n$ repose stop "a"\nStopped a.'
		);
		expect(copy).toBe('repose ls\nrepose stop "a"');
		expect(html).toContain('<span class="prompt">$ </span>');
		expect(html).toContain('<span class="output">Stopped a.</span>');
		expect(renderShell('repose run # go').copy).toBe('repose run # go');
	});

	it('fits every code block in the reading column, clear of its Copy button', () => {
		// Code blocks scroll sideways rather than wrap (DECISIONS I-345), so the
		// docs are written to fit. From 1280 wide up, where the column is the
		// narrowest above a phone, a block holds 70 columns of 13px monospace,
		// and the Copy button covers the last 8 of the first line. On a phone a
		// long line scrolls, as it would in a terminal.
		const COLUMNS = 70;
		const UNDER_COPY = 8;
		const long: string[] = [];
		let blocks = 0;
		for (const d of DOCS) {
			for (const [, lang, code] of d.body.matchAll(/^```(\w*)\n([\s\S]*?)^```$/gm)) {
				blocks++;
				code
					.replace(/\n$/, '')
					.split('\n')
					.forEach((line, i) => {
						const limit = i === 0 && lang !== 'text' ? COLUMNS - UNDER_COPY : COLUMNS;
						const width = [...line.replace(/\t/g, '  ')].length;
						if (width > limit) long.push(`${d.slug}: ${width} > ${limit}: ${line}`);
					});
			}
		}
		expect(blocks).toBeGreaterThan(100);
		expect(long).toEqual([]);
	});

	// A span that never breaks is only safe while it fits a phone's
	// column; a longer one must be allowed to wrap, or the page scrolls
	// sideways (a 44-character URL on notifications did).
	it('keeps only short inline code spans on one line', () => {
		const long: string[] = [];
		let kept = 0;
		for (const d of DOCS) {
			for (const [, text] of d.html.matchAll(/<code class="nobreak">([^<]*)<\/code>/g)) {
				kept++;
				if (text.length > NOBREAK_MAX) long.push(`${d.slug}: ${text}`);
			}
		}
		expect(kept).toBeGreaterThan(100);
		expect(long).toEqual([]);
		expect(docBySlug('notifications')!.html).toContain(
			'<code>https://user:password@ntfy.example.com/topic</code>'
		);
	});

	it("breaks code with spaces at its spaces, not at a flag's hyphens", () => {
		const html = docBySlug('cli')!.html;
		expect(html).toContain(
			'<code><span class="nobreak">--api-url</span> <span class="nobreak">URL</span></code>'
		);
	});

	it('scrolls a code block instead of wrapping it', () => {
		const html = docBySlug('tutorial-git')!.html;
		expect(html).toContain('<code class="language-text">');
		expect(html).not.toContain('class="line"');
	});
});

// The docs 404 lists the pages closest to the slug asked for (spec 2.2).
describe('closestDocs', () => {
	const slugs = (s: string) => closestDocs(s).map((d) => d.slug);

	it('ranks the longest shared prefix first', () => {
		expect(slugs('secret')[0]).toBe('secrets');
		expect(slugs('cli-ref')[0]).toBe('cli');
	});

	it('matches a word of a title or slug', () => {
		expect(slugs('chrome')).toContain('your-chrome');
		expect(slugs('lifecycl')).toContain('lifecycle');
	});

	it('puts a whole slug before a word of a title, and skips short words', () => {
		expect(slugs('agent')[0]).toBe('agents');
		expect(slugs('and')).toEqual(['index']);
	});

	it('holds to six pages, in sidebar order among equals', () => {
		const tutorials = DOCS.filter((d) => d.slug.startsWith('tutorial-')).map((d) => d.slug);
		expect(slugs('tutorial')).toEqual(tutorials.slice(0, 6));
		expect(closestDocs('tutorial').length).toBeLessThanOrEqual(6);
	});

	it('falls back to the Quickstart when nothing shares four letters', () => {
		expect(slugs('zzzz')).toEqual(['index']);
		expect(slugs('')).toEqual(['index']);
	});
});
