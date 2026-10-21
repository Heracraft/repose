// Machine on a project's Overview and its Usage tab (DECISIONS I-492,
// I-841): the size spelled out, and the minute samples drawn from GET
// /projects/:id/samples.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('the status line says what the size gives and the machine card reads memory and disk', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'machine-card-app',
		remote_url: 'github.com/heracraft/machine-card-app',
		class: 'small'
	});
	await page.goto(`/projects/${p.id}`);
	await expect(page.getByTestId('project-class')).toHaveText('small');
	await expect(page.getByText('1 core', { exact: true })).toBeVisible();
	const card = page.getByTestId('machine-card');
	// The newest sample's memory, of the size's 4 GB.
	await expect(card.getByRole('meter', { name: 'Memory in use' })).toHaveAttribute(
		'aria-valuetext',
		/ of 4 GB$/
	);
	await expect(card.getByRole('meter', { name: 'Disk' })).toBeVisible();
});

test('usage draws the samples and switches window', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'usage-app',
		remote_url: 'github.com/heracraft/usage-app',
		class: 'large'
	});
	const asked: string[] = [];
	page.on('request', (r) => {
		if (r.url().includes('/samples')) asked.push(new URL(r.url()).searchParams.get('window') ?? '');
	});
	await page.goto(`/projects/${p.id}/usage`);
	const card = page.getByTestId('usage-card');
	await expect(card.getByTestId('chart-cpu')).toBeVisible();
	// The fake's hour ends idle at 15% and held every core for a while.
	await expect(card.getByTestId('chart-cpu')).toContainText('now 15%, peak 100%');
	await expect(card.getByTestId('chart-memory')).toContainText('now 3.6 GB');
	await expect(card.getByTestId('chart-waiting-for-a-core')).toContainText('peak 62%');
	await expect(card.getByTestId('chart-waiting-for-the-server')).toContainText('now 2%');
	// The processes are folded until opened.
	await expect(card.getByRole('cell', { name: 'cc1plus' })).toBeHidden();
	await card.getByText('Busiest processes').click();
	await expect(card.getByRole('cell', { name: 'cc1plus' })).toBeVisible();
	await expect(card.getByText('100% of 2 cores')).toBeVisible();

	// The arrow keys read one point.
	await card.getByTestId('chart-cpu').locator('svg').focus();
	await page.keyboard.press('ArrowLeft');
	await expect(card.getByTestId('chart-cpu').getByText('15%', { exact: true })).toBeVisible();

	await card.getByRole('button', { name: 'Day' }).click();
	await expect(card.getByRole('button', { name: 'Day' })).toHaveAttribute('aria-pressed', 'true');
	await expect.poll(() => asked).toContain('24h');
	await expect(card.getByText('24 hours ago').first()).toBeVisible();
});

test('a stopped machine has no samples to draw', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'usage-stopped-app',
		remote_url: 'github.com/heracraft/usage-stopped-app'
	});
	await page.goto(`/projects/${p.id}`);
	await page.getByRole('button', { name: 'Stop', exact: true }).click();
	await expect(page.getByTestId('project-state')).toHaveText('stopped', { timeout: 10_000 });
	// The header and its poll stay as the tab changes, so Usage reads the
	// state the Stop left.
	await page
		.getByRole('navigation', { name: 'Project' })
		.getByRole('link', { name: 'Usage' })
		.click();
	await page.getByTestId('usage-card').getByRole('button', { name: 'Week' }).click();
	await expect(
		page.getByText('No samples in this window: the machine was not running.')
	).toBeVisible();
});

// The empty state asserted a cause nobody had checked: a machine started
// a moment ago hit it while the status line above read "running · up 0m".
test('a running machine with nothing reported yet does not claim it was off', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'usage-fresh',
		remote_url: 'github.com/h/usage-fresh'
	});
	await page.goto(`/projects/${p.id}/usage`);
	await expect(page.getByTestId('project-state')).toContainText('running');
	const usage = page.getByRole('region', { name: 'Usage' });
	if ((await usage.getByText(/No usage reported|No samples/).count()) > 0) {
		await expect(usage.getByText('No samples in this window')).toHaveCount(0);
	}
});

// Two by two from a 640px column: at 1440 the four charts make two rows.
test('the four charts sit two by two at 1440 and in one column at 390', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'usage-grid',
		remote_url: 'github.com/h/usage-grid'
	});
	for (const [width, cols] of [
		[1440, 2],
		[390, 1]
	] as const) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto(`/projects/${p.id}/usage`);
		await expect(page.getByTestId('chart-cpu')).toBeVisible();
		const xs = new Set<number>();
		for (const id of [
			'chart-cpu',
			'chart-memory',
			'chart-waiting-for-a-core',
			'chart-waiting-for-the-server'
		])
			xs.add(Math.round((await page.getByTestId(id).boundingBox())!.x));
		expect(xs.size, `at ${width}`).toBe(cols);
	}
});
