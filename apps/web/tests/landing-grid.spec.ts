// DECISIONS I-1452: grid, alignment, labeled agents, questions, and pricing
// geometry on the overhauled 6-band landing page.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect, type Page } from '@playwright/test';

const DOCS = fileURLToPath(new URL('../src/content/docs/', import.meta.url));

const cells = '#features .cells > li';

const CELLS: [string, string][] = [
	['Use its ports on your localhost', 'repose attach'],
	["Watch the agent's browser, and take over", 'repose browser'],
	['Open it in your editor', 'ssh your-project.repose'],
	['Close your laptop and get notified', 'repose ls']
];

async function grid(page: Page, width: number) {
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/');
	await page.locator('#features').scrollIntoViewIfNeeded();
}

test('01 How it works: three flush columns on thirds with unique commands', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const cols = page.locator('#how .cols > li');
	await expect(cols).toHaveCount(3);
	await expect(cols.locator('h3')).toHaveText([
		'Sync your state in one command',
		'Run unattended off your laptop',
		'Roll back the whole disk, or fork it'
	]);
	await expect(cols.locator('.cmd')).toHaveText([
		'repose run',
		'repose run -p "..."',
		'repose snapshots restore'
	]);
	const boxes = await cols.evaluateAll((els) =>
		els.map((e) => {
			const r = e.getBoundingClientRect();
			return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width) };
		})
	);
	expect(new Set(boxes.map((b) => b.y)).size).toBe(1);
	expect(new Set(boxes.map((b) => b.x)).size).toBe(3);
});

test('02 Workflow: four cells, each a claim and its command, no copy button', async ({ page }) => {
	await grid(page, 1440);
	await expect(page.locator(`${cells} h3`)).toHaveText(CELLS.map(([h]) => h));
	await expect(page.locator(`${cells} .cmd`)).toHaveText(CELLS.map(([, c]) => c));
	await expect(page.locator(`${cells} button`)).toHaveCount(0);
});

test("each workflow cell's command is in the docs", async () => {
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
	test(`the workflow cells run in ${cols} column(s) at ${width}`, async ({ page }) => {
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
			expect(boxes[0].y).toBe(boxes[1].y);
			expect(boxes[2].y).toBe(boxes[3].y);
			expect(boxes[0].w).toBe(boxes[1].w);
		}
		if (cols === 1 && width >= 640) {
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
			expect(m).toHaveLength(2);
			for (const c of m) {
				expect(c.stage).toBe(c.cell);
				expect(c.win).toBe(text);
				expect(Math.abs(c.gutter - text)).toBeLessThanOrEqual(1);
			}
		}
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

// I-1452 (amending I-287): all five preinstalled agents carry visible names
// beside their marks, and the section links to both docs pages.
test('the agents section names all five agents visibly and links to the docs', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const section = page.locator('#agents');
	const heading = section.getByRole('heading', { name: 'Preinstalled on first boot' });
	const all = section.getByRole('link', { name: 'All agents' });
	await expect(all).toHaveAttribute('href', '/docs/agents');
	await expect(section.getByRole('link', { name: 'Everything installed' })).toHaveAttribute(
		'href',
		'/docs/machine#whats-installed'
	);
	await expect(section.locator('.agents .aname')).toHaveText([
		'Claude Code',
		'Codex',
		'opencode',
		'Gemini CLI',
		'pi'
	]);
	await expect(section.locator('.agents svg')).toHaveCount(5);
	const h = (await heading.boundingBox())!;
	const l = (await all.boundingBox())!;
	expect(l.x).toBeGreaterThan(h.x + h.width);
	expect(Math.abs(l.y + l.height - (h.y + h.height))).toBeLessThan(16);
});

for (const width of [1440, 1280, 1024]) {
	test(`the head links and CTA buttons end on the right text line at ${width}`, async ({
		page
	}) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const m = await page.evaluate(() => {
			const line = (e: Element) => {
				const r = document.createRange();
				r.selectNodeContents(e);
				const rects = [...r.getClientRects()];
				return rects[rects.length - 1];
			};
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
			const copies = [...document.querySelectorAll('.steps .hk-copy .layer:not(.off)')].map(
				(l) => line(l).right
			);
			const buttons = ['#pricing', '#install'].map((sel) => {
				const head = document.querySelector(`${sel} .head`)!;
				const btn = head.querySelector('.hk-btn')!;
				const h2 = head.querySelector('h2')!;
				return {
					sel,
					right: btn.getBoundingClientRect().right,
					base: baseline(btn.querySelector('.swap > span')!),
					h2: baseline(h2)
				};
			});
			const heads = [...document.querySelectorAll('main .band > .head')].map((head) => {
				const h2 = head.querySelector('h2')!;
				const links = [...head.querySelectorAll('.links a')];
				return {
					id: h2.id,
					base: baseline(h2),
					links: links.map((a) => ({ ...a.getBoundingClientRect().toJSON(), base: baseline(a) }))
				};
			});
			return { right, copies, buttons, heads };
		});
		if (width >= 1200) expect(m.copies).toHaveLength(3);
		for (const c of m.copies) expect(Math.abs(c - m.right)).toBeLessThanOrEqual(1);
		const paired = m.heads.filter((h) => h.links.length === 2);
		expect(paired.map((h) => h.id)).toEqual(['agents-h', 'faq-h']);
		for (const h of paired) {
			const [a, b] = h.links;
			expect(Math.abs(b.right - m.right), h.id).toBeLessThanOrEqual(1);
			expect(Math.round(b.left - a.right), h.id).toBe(16);
			expect(Math.abs(a.base - h.base), h.id).toBeLessThanOrEqual(1);
			expect(Math.abs(b.base - h.base), h.id).toBeLessThanOrEqual(1);
		}
		for (const btn of m.buttons) {
			expect(Math.abs(btn.right - m.right), btn.sel).toBeLessThanOrEqual(1);
			expect(Math.abs(btn.base - btn.h2), btn.sel).toBeLessThanOrEqual(1);
		}
	});
}

for (const width of [1024, 1100, 1280, 1440, 1920]) {
	test(`the hero's copy is centred and its CTA + install bar stand under the lead at ${width}`, async ({
		page
	}) => {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/');
		const lead = (await page.locator('.hero .hk-lead').boundingBox())!;
		const h1 = (await page.locator('#hero-h').boundingBox())!;
		const start = (await page.locator('.hero').getByRole('link', { name: 'Start a free week' }).boundingBox())!;
		const install = (await page.locator('.hero .hero-install .code-copy').boundingBox())!;
		const pic = (await page.locator('.hero .term').boundingBox())!;
		expect(start.y).toBeGreaterThan(Math.max(lead.y + lead.height, h1.y + h1.height));
		expect(Math.abs(install.y - start.y)).toBeLessThanOrEqual(2);
		const middle = (b: { x: number; width: number }) => b.x + b.width / 2;
		const row = (start.x + install.x + install.width) / 2;
		for (const [what, mid] of [
			['the headline', middle(h1)],
			['the lead', middle(lead)],
			['the actions', row]
		] as const)
			expect(Math.abs(mid - middle(pic)), `${what} at ${width}`).toBeLessThanOrEqual(2);
	});
}

test('the questions open one answer at a time and cover BYO accounts, Mac restarts and isolation', async ({
	page
}) => {
	const secrets = readFileSync(join(DOCS, 'secrets.md'), 'utf8');
	expect(secrets).toContain('Nothing on the machine can open a connection to it.');
	await page.goto('/');
	const q = page.locator('#questions');
	await expect(q.locator('summary')).toHaveText([
		'Do I bring my own Claude, Codex or Gemini account?',
		'Can I run it on a Mac mini or x86 box I already own?',
		'What happens when my Mac sleeps or restarts?',
		'What can an agent reach on the machine?',
		'What does an agent cost while it waits?'
	]);
	const byo = q.locator('details').nth(0);
	await expect(byo.locator('p')).toBeHidden();
	await byo.locator('summary').click();
	await expect(byo.locator('p')).toContainText('your own agent subscriptions or API keys');

	const macRestart = q.locator('details').nth(2);
	await macRestart.locator('summary').click();
	await expect(macRestart.locator('p')).toContainText('what they held in memory is gone');

	const reach = q.locator('details').nth(3);
	await reach.locator('summary').click();
	await expect(reach.locator('p')).toContainText(
		'it connects to your laptop only through a port or a Chrome window you lend it'
	);
});

test('the workflow cells are flush, ruled and gapless, on the column line', async ({ page }) => {
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
	expect(Math.round(css[0].x + css[0].w)).toBe(Math.round(css[1].x));
	expect(Math.round(css[0].b)).toBe(Math.round(css[2].y));
	const band = await page.locator('#features').boundingBox();
	const next = await page.locator('#features + section').boundingBox();
	expect(Math.round(css[3].b)).toBe(Math.round(band!.y + band!.height));
	expect(Math.round(band!.y + band!.height)).toBe(Math.round(next!.y));
	const shots = await page.locator(`${cells} > .shot`).evaluateAll((els) =>
		els.map((e) => {
			const s = getComputedStyle(e);
			const r = e.getBoundingClientRect();
			const c = e.parentElement!.getBoundingClientRect();
			return {
				radius: s.borderTopLeftRadius,
				edges: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth],
				dl: Math.round(r.left - c.left),
				dr: Math.round(c.right - r.right),
				db: Math.round(c.bottom - r.bottom)
			};
		})
	);
	expect(shots).toHaveLength(2);
	for (const sh of shots) {
		expect(sh.radius).toBe('0px');
		expect(sh.edges).toEqual(['1px', '0px', '0px', '0px']);
		expect(sh.dr).toBe(0);
		expect(sh.db).toBe(0);
		expect(sh.dl).toBeLessThanOrEqual(1);
	}
	const title = await page.locator(`${cells}:nth-child(2) h3`).boundingBox();
	const side = await page.locator('#how-h + .side').boundingBox();
	expect(Math.abs(title!.x - side!.x)).toBeLessThanOrEqual(1);
	expect(Math.round(title!.x - css[1].x)).toBe(16);
});

test('the plans stand on the thirds at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const m = await page.evaluate(() => {
		const x = (s: string, i = 0) => document.querySelectorAll(s)[i].getBoundingClientRect().x;
		return {
			plans: [0, 1, 2].map((i) => x('.plans--public .plan', i)),
			names: [0, 1, 2].map((i) => x('.plans--public .p-name', i)),
			content: x('#how-h')
		};
	});
	const third = (m.names[2] - m.names[0]) / 2;
	expect(Math.round(m.names[1] - m.names[0])).toBe(Math.round(third));
	expect(Math.round(m.names[0])).toBe(Math.round(m.content));
	for (const i of [1, 2]) expect(Math.round(m.names[i] - m.plans[i])).toBe(16);
});

for (const width of [1440, 768, 390]) {
	test(`the workflow captures stand inset on their stages at ${width}`, async ({ page }) => {
		await grid(page, width);
		const m = await page.evaluate(() => {
			const r = (e: Element) => e.getBoundingClientRect();
			return [...document.querySelectorAll('#features .cells > li:has(> .shot)')].map((li) => {
				const shot = r(li.querySelector('.shot')!);
				const wins = [...li.querySelectorAll('.shot .shot-win')].map(r);
				return {
					left: Math.min(...wins.map((w) => w.left)) - shot.left,
					right: shot.right - Math.max(...wins.map((w) => w.right)),
					top: Math.min(...wins.map((w) => w.top)) - shot.top
				};
			});
		});
		expect(m).toHaveLength(2);
		for (const c of m) expect(c.top).toBeGreaterThanOrEqual(12);
		if (width < 1024) {
			for (const c of m) expect(Math.abs(c.left - c.right)).toBeLessThanOrEqual(1);
		}
	});
}

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
