// Inline code in the docs and the legal pages, rendered the same way so a
// command in either breaks the same way (layout.css, ".doc code.nobreak").
// Its own module so LegalPage gets it without the docs' grammars.
import type { Tokens } from 'marked';

/**
 * The longest inline code span kept on one line. 30 characters of Geist
 * Mono at 12.75px (the chip's 0.85em of 15px prose), with its padding, is
 * about 235px: it fits the 320px column at 360 wide even inside a nested
 * list.
 */
export const NOBREAK_MAX = 30;

function escapeHTML(text: string): string {
	return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

/**
 * A marked codespan renderer. A short span with no space in it is a
 * command, a flag or a path, and .doc code.nobreak keeps it on one line;
 * one with spaces (a quoted message) wraps as prose does. So does a span
 * longer than NOBREAK_MAX: kept whole, a 44-character ntfy URL ran 54px
 * past the column at 390 and scrolled the page sideways. Inside a span
 * with spaces, each short word is kept whole, so "--api-url URL" breaks at
 * its space and never after "--" (I-393); a long word in it still wraps
 * anywhere. Returning false hands the span to marked's own renderer.
 */
export function codespan(token: Tokens.Codespan): string | false {
	if (!/\s/.test(token.text)) {
		if (token.text.length > NOBREAK_MAX) return false;
		return `<code class="nobreak">${escapeHTML(token.text)}</code>`;
	}
	const words = token.text
		.split(/(\s+)/)
		.map((w) =>
			w === '' || /^\s+$/.test(w) || w.length > NOBREAK_MAX
				? escapeHTML(w)
				: `<span class="nobreak">${escapeHTML(w)}</span>`
		);
	return `<code>${words.join('')}</code>`;
}
