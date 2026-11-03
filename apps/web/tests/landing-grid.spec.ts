// DECISIONS I-837: the feature grid is four cells, each a claim, one
// paragraph and the command that does it. A visitor without the CLI cannot
// run a command, so the cells carry no copy button, and no "$": the mono
// face marks a command, as in the steps. Two columns from 1024px, one
// below, where a cell runs the frame's width and its picture keeps the
// 2x2's 34rem, starting on the text line (LANDING.md, I-961). I-843 puts a
// picture over each cell's claim.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';

const DOCS = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

const cells = '#features .cells > li';

const CELLS: [string, string][] = [
	['Restore the whole disk', 'repose snapshots restore'],
	['Use its ports on your localhost', 'repose attach'],
	["Watch the agent's browser, and take over", 'repose browser'],
	['Open it in your editor', 'ssh your-project.repose']
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

// Every command a cell shows is one the docs give: a repose command is
// in cli.md, and the SSH host is the one machine.md names.
test("each cell's command is in the docs", async () => {
	const cli = readFileSync(join(DOCS, 'cli.md'), 'utf8');
	const machine = readFileSync(join(DOCS, 'machine.md'), 'utf8');
	for (const [, cmd] of CELLS) {
		if (cmd.startsWith('ssh ')) {
			expect(machine).toContain('`ssh todo-app.repose`');
			continue;
		}
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
			// One column, a cell's stage runs the cell's width, as the
			// other bands' do, and its windows run from the text line to
			// the right one, the same gutter at both sides.
			const text = await page
				.locator('#features h2')
				.evaluate((e) => Math.round(e.getBoundingClientRect().left));
			const m = await page.locator(`${cells} > .shot`).evaluateAll((els) =>
				els.map((e) => {
					const r = e.getBoundingClientRect();
					const cell = e.parentElement!.getBoundingClientRect();
					const wins = [...e.querySelectorAll('.shot-win')].map((w) => w.getBoundingClientRect());
					return {
						stage: Math.round(r.width),
						cell: Math.round(cell.width),
						win: Math.round(Math.min(...wins.map((w) => w.left))),
						gutter: Math.round(r.right - Math.max(...wins.map((w) => w.right)))
					};
				})
			);
			for (const c of m) {
				expect(c.stage).toBe(c.cell);
				expect(c.win).toBe(text);
				expect(Math.abs(c.gutter - text)).toBeLessThanOrEqual(1);
			}
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
	// In the head's right half, on the headline's last line (I-961,
	// option D: a head's side item stands on the h2's last baseline), the
	// pair ending on the right text line, 16px apart (the owner's pick).
	const h = (await heading.boundingBox())!;
	const l = (await all.boundingBox())!;
	expect(l.x).toBeGreaterThan(h.x + h.width);
	expect(Math.abs(l.y + l.height - (h.y + h.height))).toBeLessThan(16);
});

// I-961, the owner's pick: every head whose side item is a pair of links
// ends the pair on the right text line (the frame's right content edge,
// where the cells' and the steps' text ends), 16px apart, with the link
// text on the h2's last baseline, and every head stands as far over what
// it heads as a head with a paragraph does. (The agents test above holds
// the links on the h2's last line.)
for (const width of [1440, 1280, 1024]) {
	test(`the head links end on the right text line at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const m = await page.evaluate(() => {
			const line = (e: Element) => {
				const r = document.createRange();
				r.selectNodeContents(e);
				const rects = [...r.getClientRects()];
				return rects[rects.length - 1];
			};
			// An element's last baseline: where an empty inline block at its
			// end stands.
			const baseline = (e: Element) => {
				const s = document.createElement('span');
				s.style.cssText = 'display:inline-block;width:0;height:0;vertical-align:baseline';
				e.append(s);
				const y = s.getBoundingClientRect().top;
				s.remove();
				return y;
			};
			const cell = document.querySelector('#features .cells > li:nth-child(2) .cell-copy')!;
			const cr = cell.getBoundingClientRect();
			const right = cr.right - parseFloat(getComputedStyle(cell).paddingRight);
			const button = document.querySelector('#pricing .head .hk-btn')!;
			// The steps' Copy labels, where they show (from 1200px).
			const copies = [...document.querySelectorAll('.steps .hk-copy .layer:not(.off)')].map(
				(l) => line(l).right
			);
			return {
				right,
				copies,
				button: {
					right: button.getBoundingClientRect().right,
					base: baseline(button.querySelector('.swap > span')!),
					h2: baseline(document.querySelector('#pricing-h')!)
				},
				heads: [...document.querySelectorAll('main .band > .head')].map((head) => {
					const h2 = head.querySelector('h2')!;
					const links = [...head.querySelectorAll('.links a')];
					const last = line(h2);
					const next = head.nextElementSibling!.getBoundingClientRect().top;
					return {
						id: h2.id,
						toNext: next - last.bottom,
						base: baseline(h2),
						links: links.map((a) => ({ ...a.getBoundingClientRect().toJSON(), base: baseline(a) }))
					};
				})
			};
		});
		// Copy's label ends there too, not its button's padding.
		if (width >= 1200) expect(m.copies).toHaveLength(3);
		for (const c of m.copies) expect(Math.abs(c - m.right)).toBeLessThanOrEqual(1);
		const paired = m.heads.filter((h) => h.links.length === 2);
		expect(paired.map((h) => h.id)).toEqual(['agents-h', 'install-h', 'faq-h']);
		for (const h of paired) {
			const [a, b] = h.links;
			expect(Math.abs(b.right - m.right), h.id).toBeLessThanOrEqual(1);
			expect(Math.round(b.left - a.right), h.id).toBe(16);
			expect(Math.abs(a.base - h.base), h.id).toBeLessThanOrEqual(1);
			expect(Math.abs(b.base - h.base), h.id).toBeLessThanOrEqual(1);
		}
		// Pricing's button ends on the same right edge, its label on the
		// h2's last baseline (owner, 2026-11-02: it had started on the
		// centre column).
		expect(Math.abs(m.button.right - m.right)).toBeLessThanOrEqual(1);
		expect(Math.abs(m.button.base - m.button.h2)).toBeLessThanOrEqual(1);
		// One head-to-content distance for the link heads and the paragraph
		// heads alike (the pricing button is taller, and stands apart).
		const gaps = m.heads.filter((h) => h.id !== 'pricing-h').map((h) => Math.round(h.toNext));
		expect(new Set(gaps).size, gaps.join(',')).toBe(1);
	});
}

// The owner, 2026-11-02: the hero's buttons never stand beside the lead
// (a version had Get started and Copy install command to its right).
// They share one row under the headline and the lead at every width.
for (const width of [1024, 1100, 1280, 1440, 1920]) {
	test(`the hero's buttons stand under the lead at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const lead = (await page.locator('.hero .hk-lead').boundingBox())!;
		const h1 = (await page.locator('#hero-h').boundingBox())!;
		const start = (await page.getByRole('link', { name: 'Get started' }).boundingBox())!;
		const copy = (await page.getByRole('button', { name: 'Copy install command' }).boundingBox())!;
		expect(start.y).toBeGreaterThan(Math.max(lead.y + lead.height, h1.y + h1.height));
		expect(Math.abs(copy.y - start.y)).toBeLessThanOrEqual(1);
		expect(Math.abs(start.x - h1.x)).toBeLessThanOrEqual(1);
	});
}

test('the questions open, one answer at a time as asked', async ({ page }) => {
	// The first answer is secrets.md's "Not your laptop", in its words.
	const secrets = readFileSync(join(DOCS, 'secrets.md'), 'utf8');
	expect(secrets).toContain('Nothing on the machine can open a connection to it.');
	// The snapshot answer is lifecycle.md's "Snapshots", in its numbers.
	const lifecycle = readFileSync(join(DOCS, 'lifecycle.md'), 'utf8');
	expect(lifecycle).toContain(
		'Snapshots from the last 7 days are kept, and the newest one always.'
	);
	expect(lifecycle).toContain(
		'The disk is snapshotted every night while running, and whenever you stop.'
	);
	await page.goto('/');
	const q = page.locator('section', {
		has: page.getByRole('heading', { name: 'Before you try it' })
	});
	await expect(q.locator('summary')).toHaveText([
		'Why not skip permissions on my laptop?',
		'Can I close my laptop while the agent works?',
		'How long are snapshots kept?',
		'Can three agents try three approaches at once?',
		'Why not rent a server and set it up myself?'
	]);
	const first = q.locator('details').first();
	await expect(first.locator('p')).toBeHidden();
	await first.locator('summary').click();
	await expect(first.locator('p')).toContainText("can't open a connection to your laptop");
});

// I-961 (option D): the cells are flush on the sheet, split by inner
// rules with no gap and no stage edge or radius: the centre hairline half
// a gap left of the column line, so the right cells' text starts on it
// (the head's right half). The band ends on its last row, on the next
// band's rule.
test('the cells are flush, ruled and gapless, on the column line', async ({ page }) => {
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
	expect(css.map((c) => c.top)).toEqual(['0px', '0px', '1px', '1px']);
	expect(css[0].left).toBe('0px');
	expect(css[1].left).toBe('1px');
	// Cells meet: no gap across or down.
	expect(Math.round(css[0].x + css[0].w)).toBe(Math.round(css[1].x));
	expect(Math.round(css[0].b)).toBe(Math.round(css[2].y));
	const band = await page.locator('#features').boundingBox();
	const next = await page.locator('#features + section').boundingBox();
	expect(Math.round(css[3].b)).toBe(Math.round(band!.y + band!.height));
	expect(Math.round(band!.y + band!.height)).toBe(Math.round(next!.y));
	// A stage is the cell: flush to it, square, with no edge.
	const shots = await page.locator(`${cells} > .shot`).evaluateAll((els) =>
		els.map((e) => {
			const s = getComputedStyle(e);
			const r = e.getBoundingClientRect();
			const c = e.parentElement!.getBoundingClientRect();
			return {
				radius: s.borderTopLeftRadius,
				edge: s.borderTopWidth,
				dl: Math.round(r.left - c.left),
				dr: Math.round(c.right - r.right)
			};
		})
	);
	for (const sh of shots) {
		expect(sh.radius).toBe('0px');
		expect(sh.edge).toBe('0px');
		expect(sh.dr).toBe(0);
		expect(sh.dl).toBeLessThanOrEqual(1);
	}
	// The hairline stands 16px left of the column line, where the right
	// cells' text and the heads' paragraphs start.
	const title = await page.locator(`${cells}:nth-child(2) h3`).boundingBox();
	const side = await page.locator('#setup-h + .side').boundingBox();
	expect(Math.abs(title!.x - side!.x)).toBeLessThanOrEqual(1);
	expect(Math.round(title!.x - css[1].x)).toBe(16);
});

// I-961 (option D): the thirds lines. Ready's shell and the second and
// third plans start on them, 16px left of a column's text.
test('the plans and the Ready shell stand on the thirds at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const m = await page.evaluate(() => {
		const x = (s: string, i = 0) => document.querySelectorAll(s)[i].getBoundingClientRect().x;
		return {
			plans: [0, 1, 2].map((i) => x('.plans--public .plan', i)),
			names: [0, 1, 2].map((i) => x('.plans--public .p-name', i)),
			shell: x('.ready .tcell'),
			term: x('.ready .term'),
			content: x('#setup-h')
		};
	});
	const third = (m.names[2] - m.names[0]) / 2;
	expect(Math.round(m.names[1] - m.names[0])).toBe(Math.round(third));
	expect(Math.round(m.names[0])).toBe(Math.round(m.content));
	for (const i of [1, 2]) expect(Math.round(m.names[i] - m.plans[i])).toBe(16);
	expect(Math.abs(m.shell - m.plans[1])).toBeLessThanOrEqual(1);
	// The shell's window stands on the cell's text line, as a plan's name.
	expect(Math.abs(m.term - m.names[1])).toBeLessThanOrEqual(1);
});

// Every picture's windows are inset on their stage, the Ready shell's
// too: the cell's text lines at the sides, the stage's inset above and
// below. Below 1024px the windows run the cell's width, the same gutter
// at both sides (the 34rem cap left a 164px strip at 768).
for (const width of [1440, 768, 390]) {
	test(`the windows stand inset on their stages at ${width}`, async ({ page }) => {
		await page.emulateMedia({ reducedMotion: 'reduce' });
		await grid(page, width);
		const m = await page.evaluate(() => {
			const r = (e: Element) => e.getBoundingClientRect();
			const cells = [...document.querySelectorAll('#features .cells > li')].map((li) => {
				const shot = r(li.querySelector('.shot')!);
				const wins = [...li.querySelectorAll('.shot .shot-win')].map(r);
				return {
					left: Math.min(...wins.map((w) => w.left)) - shot.left,
					right: shot.right - Math.max(...wins.map((w) => w.right)),
					top: Math.min(...wins.map((w) => w.top)) - shot.top
				};
			});
			const cell = r(document.querySelector('.ready .tcell')!);
			const term = r(document.querySelector('.ready .term')!);
			return {
				cells,
				ready: {
					top: term.top - cell.top,
					bottom: cell.bottom - term.bottom,
					right: cell.right - term.right
				}
			};
		});
		for (const c of m.cells) expect(c.top).toBeGreaterThanOrEqual(12);
		if (width < 1024) {
			for (const c of m.cells) expect(Math.abs(c.left - c.right)).toBeLessThanOrEqual(1);
		}
		// (Below 1024px the cell's top rule is inside it.)
		expect(Math.abs(m.ready.top - m.ready.bottom)).toBeLessThanOrEqual(1);
		expect(m.ready.top).toBeGreaterThanOrEqual(12);
		expect(m.ready.right).toBeGreaterThanOrEqual(15);
	});
}

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

// The plans run the sheet's width as the steps do, stacked one per row
// below 1024px, each name on the steps' text line; PlanCells' own
// 640-899px card padding stays off the landing.
for (const width of [768, 390]) {
	test(`the plans stack on the steps' line at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const plans = await page
			.locator('.plans--public .plan')
			.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().toJSON()));
		const step = (await page.locator('.steps > li').first().boundingBox())!;
		expect(plans).toHaveLength(3);
		for (const p of plans) {
			expect(Math.round(p.x)).toBe(Math.round(step.x));
			expect(Math.round(p.width)).toBe(Math.round(step.width));
		}
		expect(plans[1].y).toBeGreaterThan(plans[0].y);
		expect(plans[2].y).toBeGreaterThan(plans[1].y);
		const [name, num] = await Promise.all([
			page.locator('.plans--public .plan .p-name').first().boundingBox(),
			page.locator('.steps .step-title .label').first().boundingBox()
		]);
		expect(Math.round(name!.x)).toBe(Math.round(num!.x));
	});
}

// The snapshots window runs the machine's height, so both end on the
// stage's inset; the older tile, folded to its head, stands 4px under
// the newest one at the window's top (review of the owner's pick).
test("the snapshots window runs the machine's height at 1440", async ({ page }) => {
	// The still frame: the loop moves the older tile.
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await grid(page, 1440);
	const m = await page.evaluate(() => {
		const r = (s: string) => document.querySelector(s)!.getBoundingClientRect();
		return {
			stack: r('#features .stack'),
			old: r('#features .stack .tile.old'),
			machine: r('#features .machine')
		};
	});
	expect(Math.abs(m.stack.top - m.machine.top)).toBeLessThanOrEqual(1);
	expect(Math.abs(m.stack.bottom - m.machine.bottom)).toBeLessThanOrEqual(1);
	expect(m.old.top + 29).toBeLessThan(m.stack.bottom - 8);
});
