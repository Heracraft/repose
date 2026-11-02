// DECISIONS I-837: the feature grid is four cells, each a claim, one
// paragraph and the command that does it. A visitor without the CLI cannot
// run a command, so the cells carry no copy button, and no "$": the mono
// face marks a command, as in the steps. Two columns from 1024px, one
// below, where a cell keeps its 34rem measure (LANDING.md, I-961). I-843
// puts a picture over each cell's claim.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';

const DOCS = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

const cells = '#features .cells > li';

const CELLS: [string, string][] = [
	['Let it break the whole machine', 'repose snapshots create'],
	['Use its ports on your localhost', 'repose open 5173'],
	["Watch the agent's browser, and take over", 'repose browser'],
	['Open it in your editor', 'repose code your-project']
];

async function grid(page: Page, width: number) {
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/');
	await page.locator('#features').scrollIntoViewIfNeeded();
}

test('four cells, each a claim and its command, no copy button', async ({ page }) => {
	await grid(page, 1440);
	await expect(page.locator(`${cells} h3`)).toHaveText(CELLS.map(([h]) => h));
	await expect(page.locator(`${cells} .cmd`)).toHaveText(CELLS.map(([, c]) => c));
	// The pictures' pause buttons are the only buttons in the cells.
	await expect(page.locator(`${cells} button:not(.pic-pause)`)).toHaveCount(0);
});

// Every command a cell shows is one cli.md documents.
test("each cell's command is in cli.md", async () => {
	const cli = readFileSync(join(DOCS, 'cli.md'), 'utf8');
	for (const [, cmd] of CELLS) {
		const words = cmd.split(' ').slice(0, 2).join(' ');
		expect(cli, words).toContain(words);
	}
});

for (const [width, cols] of [
	[1440, 2],
	[1024, 2],
	[768, 1],
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
		if (cols === 1 && width >= 640) {
			// One column, a cell's picture keeps the 2x2's size.
			const stage = await page.locator(`${cells} > .shot`).first().boundingBox();
			expect(Math.round(stage!.width)).toBe(544);
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
	// In the head's right column, on the headline's first line.
	const h = (await heading.boundingBox())!;
	const l = (await all.boundingBox())!;
	expect(l.x).toBeGreaterThan(h.x + h.width);
	expect(Math.abs(l.y - h.y)).toBeLessThan(24);
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

// I-961: the cells are ruled like the plans, with no gap between them: a
// hairline over each, and from 1024px a vertical one between the columns,
// with the right cells starting on the page's column line (the head's
// right column). The band ends on its last row, on the next band's rule.
test('the cells are ruled and gapless, on the column line', async ({ page }) => {
	await grid(page, 1440);
	const css = await page.locator(cells).evaluateAll((els) =>
		els.map((e) => {
			const s = getComputedStyle(e);
			const r = e.getBoundingClientRect();
			return {
				top: s.borderTopWidth,
				left: s.borderLeftWidth,
				x: r.x,
				y: r.y,
				b: r.bottom,
				w: r.width
			};
		})
	);
	for (const c of css) expect(c.top).toBe('1px');
	expect(css[0].left).toBe('0px');
	expect(css[1].left).toBe('1px');
	// Cells meet: no gap across or down.
	expect(Math.round(css[0].x + css[0].w)).toBe(Math.round(css[1].x));
	expect(Math.round(css[0].b)).toBe(Math.round(css[2].y));
	const band = await page.locator('#features').boundingBox();
	const next = await page.locator('#features + section').boundingBox();
	expect(Math.round(css[3].b)).toBe(Math.round(band!.y + band!.height));
	expect(Math.round(band!.y + band!.height)).toBe(Math.round(next!.y));
	// The right cells' stages start on the page's column line.
	const stage = await page.locator(`${cells}:nth-child(2) > .shot`).boundingBox();
	const links = await page
		.locator('section', { has: page.getByRole('link', { name: 'All agents' }) })
		.getByRole('link', { name: 'All agents' })
		.boundingBox();
	expect(Math.abs(stage!.x - links!.x)).toBeLessThanOrEqual(1);
});

// The Editor's statusline keeps its right segment (position, line and
// column, clock) whole inside the screen at every width.
for (const width of [1440, 768, 390]) {
	test(`the Editor's statusline stays whole at ${width}`, async ({ page }) => {
		await grid(page, width);
		const [r, screen] = await Promise.all([
			page.locator('#features .ed .sl-r').boundingBox(),
			page.locator('#features .ed .screen').boundingBox()
		]);
		expect(r!.x + r!.width).toBeLessThanOrEqual(screen!.x + screen!.width + 0.5);
		expect(r!.x).toBeGreaterThan(screen!.x);
	});
}
