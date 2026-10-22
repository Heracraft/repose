// DECISIONS I-837: "On every machine" is four cells, each a claim, one
// paragraph and the command that does it. A visitor without the CLI cannot
// run a command, so the cells carry no copy button. Two columns from 768px,
// one below. I-843 puts a picture over each cell's claim.
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
	// The pictures' pause buttons are the only buttons in the cells.
	await expect(page.locator(`${cells} button:not(.pic-pause)`)).toHaveCount(0);
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

// I-843: the agents section is the Ready picture under its heading, with
// its two links on the headline's row.
test('the agents section links to the agents and everything installed', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const heading = page.getByRole('heading', {
		name: 'The agents you use, and the tools they need, on first boot'
	});
	const section = page.locator('section', { has: heading });
	const all = section.getByRole('link', { name: 'All agents' });
	await expect(all).toHaveAttribute('href', '/docs/agents');
	await expect(section.getByRole('link', { name: 'Everything installed' })).toHaveAttribute(
		'href',
		'/docs/machine#whats-installed'
	);
	// Beside the headline, its bottom on the headline's last line.
	const h = (await heading.boundingBox())!;
	const l = (await all.boundingBox())!;
	expect(l.x).toBeGreaterThan(h.x + h.width);
	expect(Math.abs(l.y + l.height - (h.y + h.height))).toBeLessThan(12);
});

test('the questions open, one answer at a time as asked', async ({ page }) => {
	// The first answer is secrets.md's "Not your laptop", in its words.
	const secrets = readFileSync(join(DOCS, 'secrets.md'), 'utf8');
	expect(secrets).toContain('Nothing on the machine can open a connection to it.');
	await page.goto('/');
	const q = page.locator('section', { has: page.getByRole('heading', { name: 'Questions' }) });
	await expect(q.locator('summary')).toHaveText([
		'Why not skip permissions on my laptop?',
		'Can I close my laptop while the agent works?',
		'What if the agent wrecks the machine?',
		'Can three agents try three approaches at once?',
		'Why not rent a server and set it up myself?'
	]);
	const first = q.locator('details').first();
	await expect(first.locator('p')).toBeHidden();
	await first.locator('summary').click();
	await expect(first.locator('p')).toContainText('can open a connection to your laptop');
});
