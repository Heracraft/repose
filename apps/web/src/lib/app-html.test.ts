import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// app.html paints the page before routes/layout.css arrives (DECISIONS
// I-331), with the page and ink colours written out by hand. If the two
// files drift, every page flashes the old colour on load, in one scheme or
// both. This reads the colours out of both files and holds them equal.
const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');
const appHtml = read('../app.html');
const css = read('../routes/layout.css');
const tokens = read('./hk/tokens.css');
const config = read('../../svelte.config.js');

/** The value of a custom property in a block of CSS, with var() resolved. */
function resolve(block: string, name: string): string {
	const m = new RegExp(`${name}:\\s*([^;]+);`).exec(block);
	if (!m) throw new Error(`${name} not found`);
	const value = m[1].trim();
	const ref = /^var\((--[\w-]+)\)$/.exec(value);
	return ref ? resolve(css, ref[1]) : value.toLowerCase();
}

/** The block of CSS from the first `{` after `start` to its matching `}`. */
function blockAt(text: string, start: number): string {
	const open = text.indexOf('{', start);
	let depth = 0;
	for (let i = open; i < text.length; i++) {
		if (text[i] === '{') depth++;
		else if (text[i] === '}' && --depth === 0) return text.slice(open + 1, i);
	}
	throw new Error('unbalanced block');
}

function schemeBlocks(): { light: string; dark: string } {
	const base = css.slice(css.indexOf('@layer base'));
	const light = base.slice(
		base.indexOf(':root {'),
		base.indexOf('@media (prefers-color-scheme: dark)')
	);
	const darkStart = base.indexOf('@media (prefers-color-scheme: dark)');
	const dark = base.slice(darkStart, base.indexOf('body {', darkStart));
	return { light, dark };
}

describe('app.html', () => {
	const { light, dark } = schemeBlocks();
	const [lightHtml, darkHtml] = appHtml.split('@media (prefers-color-scheme: dark)');

	it('paints the page and the text in layout.css colours before it loads', () => {
		const bg = (s: string) => /background:\s*(#[0-9a-f]{6})/i.exec(s)?.[1].toLowerCase();
		const fg = (s: string) => /\bcolor:\s*(#[0-9a-f]{6})/i.exec(s)?.[1].toLowerCase();
		expect(bg(lightHtml)).toBe(resolve(light, '--page'));
		expect(fg(lightHtml)).toBe(resolve(light, '--ink'));
		expect(bg(darkHtml)).toBe(resolve(dark, '--page'));
		expect(fg(darkHtml)).toBe(resolve(dark, '--ink'));
	});

	it('gives the browser chrome the page colour in each scheme', () => {
		const theme = (scheme: string) =>
			new RegExp(
				`theme-color" content="(#[0-9a-f]{6})" media="\\(prefers-color-scheme: ${scheme}\\)`,
				'i'
			)
				.exec(appHtml)?.[1]
				.toLowerCase();
		expect(theme('light')).toBe(resolve(light, '--page'));
		expect(theme('dark')).toBe(resolve(dark, '--page'));
	});

	// The footer's theme switch: a stored Dark on a light computer repaints
	// before layout.css arrives, and paints native controls dark too.
	it('paints a stored theme the same as the dark scheme', () => {
		const base = css.slice(css.indexOf('@layer base'));
		const manual = blockAt(base, base.indexOf(":root[data-theme='dark']"));
		expect(resolve(manual, '--page')).toBe(resolve(dark, '--page'));
		expect(resolve(manual, '--ink')).toBe(resolve(dark, '--ink'));
		const paint = blockAt(appHtml, appHtml.indexOf("html[data-theme='dark']"));
		expect(/background:\s*(#[0-9a-f]{6})/i.exec(paint)?.[1].toLowerCase()).toBe(
			resolve(dark, '--page')
		);
		expect(/\bcolor:\s*(#[0-9a-f]{6})/i.exec(paint)?.[1].toLowerCase()).toBe(
			resolve(dark, '--ink')
		);
		expect(paint).toMatch(/color-scheme:\s*dark/);
		const light = blockAt(appHtml, appHtml.indexOf("html[data-theme='light']"));
		expect(light).toMatch(/color-scheme:\s*light/);
	});

	it('allows its one inline script by hash in the CSP', () => {
		const scripts = [...appHtml.matchAll(/<script>([\s\S]*?)<\/script>/g)];
		expect(scripts).toHaveLength(1);
		const hash = 'sha256-' + createHash('sha256').update(scripts[0][1]).digest('base64');
		expect(config).toContain(`'${hash}'`);
		expect(appHtml).toContain(`'${hash}'`);
	});

	it('paints the kit tokens: --page and --ink are --hk-bg and --hk-text', () => {
		const kitLight = blockAt(tokens, tokens.indexOf('.hk {'));
		const kitDark = blockAt(
			tokens,
			tokens.indexOf(":root:not(.light):not([data-theme='light']) .hk")
		);
		const hex = (block: string, name: string) =>
			new RegExp(`${name}:\\s*(#[0-9a-f]{6})`, 'i').exec(block)?.[1].toLowerCase();
		expect(resolve(light, '--page')).toBe(hex(kitLight, '--hk-bg'));
		expect(resolve(light, '--ink')).toBe(hex(kitLight, '--hk-text'));
		expect(resolve(dark, '--page')).toBe(hex(kitDark, '--hk-bg'));
		expect(resolve(dark, '--ink')).toBe(hex(kitDark, '--hk-text'));
	});

	// Inside any layer the rule loses to app.html's unlayered
	// `body { visibility: hidden }` and every page stays blank.
	it('shows the body from layout.css outside any layer', () => {
		const last = css.lastIndexOf('body {');
		const rule = blockAt(css, last);
		expect(rule).toMatch(/visibility:\s*visible/);
		let depth = 0;
		for (const ch of css.slice(0, last)) {
			if (ch === '{') depth++;
			else if (ch === '}') depth--;
		}
		expect(depth).toBe(0);
	});
});
