// Landing geometry & interactive showcase tests: monumental scroll-driven
// ScrollWheel across 4 beats on desktop (and stacked 4-beat layout on phones),
// left-to-right Safety Wreck layout, and flush PlanCells on thirds.
import { test, expect } from '@playwright/test';

test('01 How it works: scroll-driven wheel rotates across four beats on desktop and supports click and keyboard', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');

	const track = page.locator('#agents .sw-track');
	const stage = page.locator('#agents .sw-desktop');
	await expect(track).toHaveClass(/sw-track--scroll/);
	await expect(stage).toBeVisible();
	await expect(page.locator('#agents .sw-mobile')).toBeHidden();

	const navBtns = stage.locator('.sw-nav [role="tab"]');
	await expect(navBtns).toHaveCount(4);
	await expect(navBtns.nth(0)).toHaveAttribute('aria-selected', 'true');
	await expect(stage.locator('#where-h')).toHaveText('Our cloud, or your own hardware');

	// Scroll to the end of the track -> wheel turns toward 270deg and activates Beat 1.4.
	const geom = await track.evaluate((el) => {
		const r = el.getBoundingClientRect();
		return { top: r.top + window.scrollY, height: r.height };
	});
	const vh = 900 - 57;
	const scrollable = geom.height - vh;
	await page.evaluate((y) => window.scrollTo(0, y), geom.top - 57 + scrollable);
	await expect(navBtns.nth(3)).toHaveAttribute('aria-selected', 'true');
	await expect(stage.locator('#where-h')).toHaveText('Close the lid, get pinged when done');

	const dialAngle = await page
		.locator('#agents .sw-rotor')
		.evaluate((el) => parseFloat(el.style.getPropertyValue('--angle')));
	expect(dialAngle).toBeGreaterThan(240);

	// Scroll to Beat 1.2 (Agents) -> all five preinstalled agents carry visible names.
	await page.evaluate((y) => window.scrollTo(0, y), geom.top - 57 + scrollable / 3);
	await expect(navBtns.nth(1)).toHaveAttribute('aria-selected', 'true');
	await expect(stage.locator('.sw-panel-wrap.is-active .sw-ag-icons .sw-mini-chip')).toHaveText([
		'Claude Code',
		'Codex',
		'opencode',
		'Gemini CLI',
		'pi'
	]);
	await expect(stage.locator('.sw-panel-wrap.is-active .sw-ag-icons svg')).toHaveCount(5);

	// Keyboard navigation on the wheel nav works cleanly under reduced motion (direct jump).
	await page.emulateMedia({ reducedMotion: 'reduce' });
	await navBtns.nth(1).focus();
	await page.keyboard.press('ArrowDown');
	await expect(navBtns.nth(2)).toHaveAttribute('aria-selected', 'true');
	await expect(stage.locator('#where-h')).toHaveText('Works with your browser and editor');
	await page.keyboard.press('ArrowUp');
	await expect(navBtns.nth(1)).toHaveAttribute('aria-selected', 'true');
});

test('01 How it works: stacks all four beats vertically on phones without hidden tabs', async ({
	page
}) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');

	await expect(page.locator('#agents .sw-desktop')).toBeHidden();
	const mob = page.locator('#agents .sw-mobile');
	await expect(mob).toBeVisible();
	await expect(mob.locator('.sw-mb-head .label')).toHaveText(['1.1', '1.2', '1.3', '1.4']);
	await expect(mob.locator('.sw-mb-head h3')).toHaveText([
		'Our cloud, or your own hardware',
		'All 5 agents in parallel sandboxes',
		'Works with your browser and editor',
		'Close the lid, get pinged when done'
	]);
	await expect(mob.locator('.sw-ag-icons .sw-mini-chip')).toHaveText([
		'Claude Code',
		'Codex',
		'opencode',
		'Gemini CLI',
		'pi'
	]);
	const tops = await mob
		.locator('.sw-mob-beats > li')
		.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().top));
	expect(tops).toHaveLength(4);
	for (let i = 1; i < tops.length; i++) {
		expect(tops[i]).toBeGreaterThan(tops[i - 1]);
	}
});

test('02 Safety arranges laptop, sandbox, and snapshots left-to-right on desktop and stacked on phones', async ({
	page
}) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const safety = page.locator('#safety');
	const [lap, side] = await Promise.all([
		safety.locator('.side.laptop').boundingBox(),
		safety.locator('.side.right').boundingBox()
	]);
	expect(lap!.x + lap!.width).toBeLessThan(side!.x);
	expect(Math.abs(lap!.y - side!.y)).toBeLessThan(80);

	await page.setViewportSize({ width: 390, height: 844 });
	const [mlap, mside] = await Promise.all([
		safety.locator('.side.laptop').boundingBox(),
		safety.locator('.side.right').boundingBox()
	]);
	expect(mlap!.y + mlap!.height).toBeLessThanOrEqual(mside!.y);
});

test('the plans stand on the thirds at 1440', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/');
	const m = await page.evaluate(() => {
		const x = (s: string, i = 0) => document.querySelectorAll(s)[i].getBoundingClientRect().x;
		return {
			plans: [0, 1, 2].map((i) => x('.plans--public .plan', i)),
			names: [0, 1, 2].map((i) => x('.plans--public .p-name', i)),
			content: x('#pricing-h')
		};
	});
	const third = (m.names[2] - m.names[0]) / 2;
	expect(Math.round(m.names[1] - m.names[0])).toBe(Math.round(third));
	expect(Math.round(m.names[0])).toBe(Math.round(m.content));
	for (const i of [1, 2]) expect(Math.round(m.names[i] - m.plans[i])).toBe(16);
});

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
