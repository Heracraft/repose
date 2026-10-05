import { describe, expect, it } from 'vitest';
import {
	DOCS,
	NOBREAK_MAX,
	SECTIONS,
	docBySlug,
	highlightNix,
	movedDoc,
	renderShell,
	search,
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

	it('finds pages by words in them', () => {
		expect(search('ntfy topic')[0]?.doc.slug).toBe('notifications');
		expect(search('')).toEqual([]);
		expect(search('zzzz-not-a-word')).toEqual([]);
		// A line commented out in a page is not searchable.
		expect(search('live product for other people')).toEqual([]);
	});

	it('sends a moved page on to its new slug', () => {
		expect(movedDoc('tutorial-your-chrome')).toBe('your-chrome');
		expect(docBySlug(movedDoc('tutorial-your-chrome') ?? '')).toBeDefined();
		expect(movedDoc('your-chrome')).toBeUndefined();
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
