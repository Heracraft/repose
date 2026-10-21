// DECISIONS I-837: "On every machine" is four cells, each a claim, one
// paragraph and the command that does it. A visitor without the CLI cannot
// run a command, so the cells carry no copy button. Two columns from 768px,
// one below; "Agents on first boot" lists agents.md's table.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';

const DOCS = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

const cells = '#features .cells > li';

const CELLS: [string, string][] = [
	['Let it break the whole machine', '$ repose snapshots create'],
	["The machine's ports on your localhost", '$ repose open 3000'],
	["Watch the agent's browser, and take over", '$ repose browser'],
	['Open it in your editor', '$ repose code your-project']
];

async function grid(page: Page, width: number) {
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/');
	await page.locator('#features').scrollIntoViewIfNeeded();
}

test('four cells, each a claim and its command, no copy button', async ({ page }) => {
	await grid(page, 1440);
	await expect(page.locator(`${cells} h3`)).toHaveText(CELLS.map(([h]) => h));
	await expect(page.locator(`${cells} .cell-cmd`)).toHaveText(CELLS.map(([, c]) => c));
	await expect(page.locator(`${cells} button`)).toHaveCount(0);
});

// Every command a cell shows is one cli.md documents.
test("each cell's command is in cli.md", async () => {
	const cli = readFileSync(join(DOCS, 'cli.md'), 'utf8');
	for (const [, cmd] of CELLS) {
		const words = cmd.replace(/^\$ /, '').split(' ').slice(0, 2).join(' ');
		expect(cli, words).toContain(words);
	}
});

for (const [width, cols] of [
	[1440, 2],
	[1024, 2],
	[768, 2],
	[390, 1]
] as const) {
	test(`the cells run in ${cols} column(s) at ${width}`, async ({ page }) => {
		await grid(page, width);
		const boxes = await page.locator(cells).evaluateAll((els) =>
			els.map((e) => {
				const r = e.getBoundingClientRect();
				return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width) };
			})
		);
		expect(boxes).toHaveLength(4);
		const xs = new Set(boxes.map((b) => b.x));
		expect(xs.size).toBe(cols);
		if (cols === 2) {
			// Rows line up: cells 1 and 2 share a top, so do 3 and 4.
			expect(boxes[0].y).toBe(boxes[1].y);
			expect(boxes[2].y).toBe(boxes[3].y);
			expect(boxes[0].w).toBe(boxes[1].w);
		}
		// A 24px claim runs to three lines at most.
		const lines = await page
			.locator(`${cells} h3`)
			.evaluateAll((els) =>
				els.map((e) =>
					Math.round(e.getBoundingClientRect().height / parseFloat(getComputedStyle(e).lineHeight))
				)
			);
		for (const n of lines) expect(n).toBeLessThanOrEqual(3);
	});
}

test("the agents list is agents.md's table", async ({ page }) => {
	const md = readFileSync(join(DOCS, 'agents.md'), 'utf8');
	// The first table, from its "| Agent | `--agent` |" header to its end.
	const table = md.slice(md.indexOf('| Agent ')).split('\n\n')[0];
	const rows = [...table.matchAll(/^\| ([^|`]+?)\s+\| `([^`]+)`/gm)]
		.map((m) => [m[1], m[2]])
		.filter(([name]) => name !== 'Agent');
	expect(rows.length).toBeGreaterThanOrEqual(5);
	await page.goto('/');
	const list = page.getByRole('list', { name: 'Agents' });
	await expect(list.locator('.name')).toHaveText(rows.map(([n]) => n));
	// Each value carries its flag, so the column says what it is.
	await expect(list.locator('.flag')).toHaveText(rows.map(([, f]) => `--agent ${f}`));
	const section = page.locator('section', {
		has: page.getByRole('heading', { name: 'Agents on first boot' })
	});
	await expect(section.getByRole('link', { name: 'All agents' })).toHaveAttribute(
		'href',
		'/docs/agents'
	);
	await expect(section.getByRole('link', { name: 'Everything installed' })).toHaveAttribute(
		'href',
		'/docs/machine#whats-installed'
	);
});

test('the questions open, one answer at a time as asked', async ({ page }) => {
	const index = readFileSync(join(DOCS, 'index.md'), 'utf8');
	const restart = index.match(/starts it again in (about \d+ seconds)/)![1];
	await page.goto('/');
	const q = page.locator('section', { has: page.getByRole('heading', { name: 'Questions' }) });
	await expect(q.locator('summary')).toHaveText([
		'Do my files survive a stop?',
		'Is my Claude Code login copied from my laptop?',
		'Does the CLI run on Windows?'
	]);
	const first = q.locator('details').first();
	await expect(first.locator('p')).toBeHidden();
	await first.locator('summary').click();
	await expect(first.locator('p')).toContainText(`starts the machine again in ${restart}`);
});
