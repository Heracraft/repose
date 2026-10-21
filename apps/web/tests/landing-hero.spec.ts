// DECISIONS I-837: the hero is the eyebrow, the headline, the lead, "Get
// started" with a labelled copy of the install command, and a near-black
// terminal replaying the Quickstart's `repose run` session as index.md
// prints it. The terminal plays once, keeps its height while it does, and
// shows every line at once without scripts or under reduced motion. It
// ends on "Ready in 16s." and a still caret: `repose run -p` attaches you
// to the agent, so no shell prompt comes back. It has no copy button.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';

const DOCS = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

// index.md's "4. Let it loose": its command block and its output block,
// read as session.ts reads them, so the test checks the page against the
// docs rather than against a copy of the page.
const index = readFileSync(join(DOCS, 'index.md'), 'utf8');
const section = index.split('\n## 4. Let it loose\n')[1].split('\n## ')[0];
const [commands, output] = [...section.matchAll(/^```[a-z]*\n([\s\S]*?)^```$/gm)].map((m) =>
	m[1].trimEnd().split('\n')
);
const expected = [...commands.map((c) => `$ ${c}`), ...output];

const lines = (page: Page) => page.locator('.term .ln');

async function texts(page: Page): Promise<string[]> {
	return (await lines(page).allTextContents()).map((t) => t.replace(/\s+$/, ''));
}

test('the session is index.md\'s, in order, ending "Ready in 16s."', async () => {
	expect(commands).toEqual(['cd ~/code/your-project', expect.stringMatching(/^repose run -p "/)]);
	expect(output.at(-1)).toMatch(/^Ready in \d+s\.$/);
});

test.describe('without scripts', () => {
	test.use({ javaScriptEnabled: false });

	test('the prerendered terminal shows every line', async ({ page }) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/');
		const term = page.getByRole('region', { name: 'A repose run session' });
		await expect(term).toBeVisible();
		await expect(term.locator('.title')).toHaveText('~/code/your-project');
		expect(await texts(page)).toEqual(expected);
		for (const line of await lines(page).all()) await expect(line).toHaveCSS('opacity', '1');
		// The caret ends the "Ready" line; no prompt line follows it.
		await expect(lines(page).last().locator('.caret')).toBeVisible();
		await expect(term.locator('.caret')).toHaveCount(1);
	});
});

test('the terminal plays once, ends at a still caret, and keeps its height', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const term = page.locator('.term');
	await expect(term).toHaveClass(/playing/);
	const before = await term.boundingBox();
	// Mid-play: the first command is in, the "Ready" line is not yet.
	await expect(lines(page).first()).toHaveCSS('opacity', '1');
	await expect(lines(page).last()).toHaveCSS('opacity', '0');
	// 8 lines one every 450ms, plus the 240ms entry.
	await expect(lines(page).last()).toHaveCSS('opacity', '1', { timeout: 8000 });
	expect(await texts(page)).toEqual(expected);
	const after = await term.boundingBox();
	expect(after!.height).toBe(before!.height);
	// The caret holds still: no animation left on the pane.
	await page.waitForTimeout(500);
	const running = await term.evaluate((t) =>
		t.getAnimations({ subtree: true }).filter((a) => a.playState === 'running')
	);
	expect(running.length).toBe(0);
});

test('under reduced motion every line shows at once', async ({ page }) => {
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');
	await expect(page.locator('.term')).not.toHaveClass(/playing/);
	for (const line of await lines(page).all()) await expect(line).toHaveCSS('opacity', '1');
	expect(await texts(page)).toEqual(expected);
});

// Its `cd ~/code/your-project` fails on a visitor's laptop, and the
// `repose run -p` after it would then start an agent in whatever folder
// they are in; the hero's install copy is the one the reader acts on.
test('the terminal has no copy button', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	await expect(page.locator('.term')).toBeVisible();
	await expect(page.locator('.term button')).toHaveCount(0);
});

test('the hero: eyebrow, headline, two-tone lead, one primary and the install copy', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const hero = page.locator('main section').first();
	await expect(hero.locator('.hk-eyebrow')).toHaveText('For solo founders and their agents');
	await expect(hero.getByRole('heading', { level: 1 })).toHaveText(
		'Your dev environment, replicated in the cloud'
	);
	// "full permissions" sits in the lead's second sentence, after the claim.
	await expect(hero.locator('.hk-lead strong')).toHaveText(
		'Each project gets a machine of its own, with your code, tools and logins on it.'
	);
	await expect(hero.locator('.hk-lead')).toContainText(
		'Agents there run with full permissions, and nobody approves commands one by one.'
	);
	await expect(hero.getByRole('button', { name: 'Get started' })).toBeVisible();
	await expect(hero.getByRole('button', { name: 'Copy install command' })).toBeVisible();
	await expect(hero.locator('.hk-btn')).toHaveCount(1);
});

// The pane replays a fixed set of lines, so it is as tall as they are:
// no empty black under the last one (review-landing S3).
test('at 1440 the terminal sits beside the copy, sized to its lines', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const left = (await page.locator('.hero-copy').boundingBox())!;
	const right = (await page.locator('.term').boundingBox())!;
	expect(right.x).toBeGreaterThan(left.x + left.width);
	const last = (await lines(page).last().boundingBox())!;
	expect(right.y + right.height - (last.y + last.height)).toBeLessThanOrEqual(64);
	// Centred on the copy column.
	const mid = (b: { y: number; height: number }) => b.y + b.height / 2;
	expect(Math.abs(mid(left) - mid(right))).toBeLessThanOrEqual(2);
	// The whole hero, terminal included, is in the first screen.
	expect(right.y + right.height).toBeLessThanOrEqual(900);
});

// The headline sets in three lines beside the terminal from 1280 and in
// two above it below 1280, and no terminal line wraps (review r4: four
// lines beside a short pane leaned the hero left).
for (const [width, beside, lines_] of [
	[1440, true, 3],
	[1280, true, 3],
	[1024, false, 2],
	[768, false, 2]
] as const) {
	test(`at ${width} the headline takes ${lines_} lines, terminal ${beside ? 'beside' : 'under'} it`, async ({
		page
	}) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const h1 = page.locator('.hero h1');
		const n = await h1.evaluate((e) => {
			const lh = parseFloat(getComputedStyle(e).lineHeight);
			return Math.round(e.getBoundingClientRect().height / lh);
		});
		expect(n).toBe(lines_);
		const left = (await page.locator('.hero-copy').boundingBox())!;
		const term = (await page.locator('.term').boundingBox())!;
		if (beside) expect(term.x).toBeGreaterThan(left.x + left.width);
		else expect(term.y).toBeGreaterThanOrEqual(left.y + left.height);
		// Every line of the replay holds one row.
		const wrapped = await lines(page).evaluateAll(
			(els) =>
				els.filter(
					(e) =>
						e.getBoundingClientRect().height > 2 * parseFloat(getComputedStyle(e).lineHeight) - 1
				).length
		);
		expect(wrapped).toBe(0);
	});
}

test('at 390 the terminal follows the install line, full width, and nothing scrolls sideways', async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');
	const install = (await page.locator('.install').boundingBox())!;
	const term = (await page.locator('.term').boundingBox())!;
	expect(term.y).toBeGreaterThan(install.y + install.height);
	// On the frame's 20px gutter, as wide as the column above it.
	const copy = (await page.locator('.hero-copy').boundingBox())!;
	expect(Math.round(term.x)).toBe(20);
	expect(Math.round(term.width)).toBe(Math.round(copy.width));
	const over = await page.evaluate(
		() => document.documentElement.scrollWidth - document.documentElement.clientWidth
	);
	expect(over).toBeLessThanOrEqual(0);
});
