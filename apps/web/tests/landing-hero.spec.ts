// DECISIONS I-667: the hero is never an empty frame. Server-rendered, the
// machine has its services and the agent and the rail has the nightly
// snapshot; across a loop the machine always shows its services and the
// rail a tile; the laptop's rows are never drawn in the wreck's red; and
// the whole machine, agent row included, is in the first 900px at 1440.
import { test, expect, type Page } from '@playwright/test';
import { holdClock } from './helpers';

test.describe('without scripts', () => {
	test.use({ javaScriptEnabled: false });

	test('the server-rendered hero has the services, the agent and the nightly snapshot', async ({
		page
	}) => {
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto('/');
		const machine = page.locator('.hero-pic .mwin > .win');
		await expect(machine.locator('.sys').first()).toContainText('job listings');
		await expect(machine.locator('.sys').first()).toContainText('3,532 rows');
		await expect(machine.locator('.sys').nth(1)).toContainText('v24.20.0');
		await expect(machine.locator('.mode')).toBeVisible();
		await expect(page.locator('.railbody .t-n-stub')).toBeVisible();
		await expect(page.locator('.railbody .t-n-stub')).toContainText('03:00');
	});
});

async function loop(page: Page, width: number, height: number) {
	await page.setViewportSize({ width, height });
	// The clock is held from the first script, so the loop starts at 0
	// however long the load took and moves only as the test runs it.
	await holdClock(page);
	await page.goto('/');
	await page.locator('.hero-pic.mounted').waitFor();
	await page.locator('.hero-frame').evaluate((e) => e.scrollIntoView({ block: 'start' }));
	await expect(page.locator('.hero-frame')).toHaveAttribute('data-motion', 'play');
}

for (const [width, height] of [
	[1440, 900],
	[390, 844]
]) {
	test(`no empty machine or rail and no red laptop row over a loop at ${width}`, async ({
		page
	}) => {
		// 95 samples of every laptop element's colours: slow on a loaded box.
		test.setTimeout(90_000);
		await loop(page, width, height);
		// The wreck's red as the browser computes it, in the format the
		// laptop's colours come back in.
		const stop = await page.evaluate(() => {
			const i = document.createElement('i');
			i.style.color = 'var(--pic-stop)';
			document.querySelector('.hero-pic')!.appendChild(i);
			const c = getComputedStyle(i).color;
			i.remove();
			return c;
		});
		expect(stop).not.toBe('');
		for (let t = 0; t < 19000; t += 200) {
			await page.clock.runFor(200);
			const s = await page.evaluate(() => {
				const op = (e: Element | null) => {
					let o = 1;
					for (let x = e; x && !x.classList.contains('hero-pic'); x = x.parentElement)
						o *= +getComputedStyle(x).opacity;
					return o;
				};
				const tiles = [...document.querySelectorAll('.railbody .tile')].map(op);
				const rows = [...document.querySelectorAll('.lw .rows > li')].filter((e) => op(e) > 0.9);
				const laptop = [...document.querySelectorAll('.laptop *')].flatMap((e) => {
					const c = getComputedStyle(e);
					return [c.color, c.backgroundColor];
				});
				return { tile: Math.max(...tiles), rows: rows.length, laptop };
			});
			expect(s.tile, `rail at ${t}ms`).toBeGreaterThan(0.5);
			expect(s.rows, `machine rows at ${t}ms`).toBeGreaterThanOrEqual(2);
			expect(s.laptop, `laptop colours at ${t}ms`).not.toContain(stop);
		}
	});
}

test('the whole machine is in the first 900px at 1440, mid-wreck', async ({ page }) => {
	await loop(page, 1440, 900);
	await page.evaluate(() => window.scrollTo(0, 0));
	await page.clock.runFor(9000);
	const box = await page.locator('.hero-pic .mwin').boundingBox();
	const agent = await page.locator('.hero-pic .mwin > .lw .agentrow').boundingBox();
	expect(box!.y + box!.height).toBeLessThanOrEqual(900);
	expect(agent!.y + agent!.height).toBeLessThanOrEqual(900);
});

test('the agent row is in the first screen at 1280x800', async ({ page }) => {
	await loop(page, 1280, 800);
	await page.evaluate(() => window.scrollTo(0, 0));
	const agent = await page.locator('.hero-pic .mwin > .lw .agentrow').boundingBox();
	expect(agent!.y + agent!.height).toBeLessThanOrEqual(800);
});

test('the rail ends on the rule over the agent row and the laptop on the floor at 1440', async ({
	page
}) => {
	await loop(page, 1440, 900);
	const m = await page.evaluate(() => {
		const r = (s: string) => document.querySelector(s)!.getBoundingClientRect();
		return {
			rule: r('.lw .agentrow').top - 4,
			rail: r('.hero-pic .rail').bottom,
			laptop: r('.hero-pic .laptop .win').bottom,
			machine: r('.hero-pic .mwin > .lw').bottom
		};
	});
	expect(Math.abs(m.rail - 1 - m.rule)).toBeLessThanOrEqual(0.5);
	expect(Math.abs(m.laptop - m.machine)).toBeLessThanOrEqual(0.5);
});

test('at 390 the globe hangs under the machine and the snapshots take the full width', async ({
	page
}) => {
	await loop(page, 390, 844);
	const m = await page.evaluate(() => {
		const r = (s: string) => document.querySelector(s)!.getBoundingClientRect();
		return { machine: r('.hero-pic .mwin > .lw'), rail: r('.hero-pic .rail'), globe: r('.globe') };
	});
	expect(m.rail.width).toBeCloseTo(m.machine.width, 0);
	expect(m.globe.right).toBeCloseTo(m.machine.right, 0);
	expect(m.globe.bottom).toBeLessThanOrEqual(m.rail.top);
});

test('the postgres count falls through numbers in one value, never two drawn at once', async ({
	page
}) => {
	await loop(page, 1440, 900);
	await page.clock.runFor(7600);
	const seen = new Set<string>();
	for (let i = 0; i < 40; i++) {
		await page.clock.runFor(50);
		const vals = await page
			.locator('.hero-pic .mwin > .lw .sys')
			.first()
			.locator('.val > span')
			.allTextContents();
		expect(vals).toHaveLength(1);
		seen.add(vals[0]);
	}
	expect(seen.size).toBeGreaterThan(3);
});
