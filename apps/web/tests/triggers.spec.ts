// A project's Triggers tab (DECISIONS I-1248..I-1262): the list with
// each trigger's door, its last run and the next fire; Run, On/Off, Copy
// and Remove in the row; the runs under it; and the empty state that
// names the command, because making a trigger means writing a prompt
// into the machine and a browser cannot do that (I-1258).
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addTriggerRun } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

/**
 * The viewport at 1440 and 390 in both schemes, at 1x, for the review
 * CLAUDE.md asks for ("Judge visuals at real size"). Written only with
 * REPOSE_SHOTS_DIR set, as browser-view.spec.ts does.
 */
async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await page.emulateMedia({ colorScheme: null });
}

/** A project with three triggers and a run each, through the api. */
async function seeded(name: string) {
	const api = apiURLFromEnv();
	const p = await createProject(api, { name, remote_url: `github.com/heracraft/${name}` });
	const put = (body: Record<string, unknown>) =>
		fetch(`${api}/projects/${p.id}/triggers`, {
			method: 'POST',
			headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
			body: JSON.stringify(body)
		});
	await put({ name: 'apply', kind: 'route', route: 'recruiting' });
	await put({ name: 'triage', kind: 'schedule', spec: 'weekdays 07:00', zone: 'Europe/Berlin' });
	await put({ name: 'sweep', kind: 'schedule', spec: 'daily 03:00', zone: 'Europe/Berlin' });
	await put({ name: 'sweep', enabled: false });
	await addTriggerRun({
		project_id: p.id,
		trigger: 'apply',
		state: 'done',
		summary: 'applied to Senior Go at Intel',
		source: 'route',
		window: 't-apply',
		payload_bytes: 412,
		ago_minutes: 12,
		took_seconds: 112,
		link_url: 'https://jobs.example.com/a/1829',
		link_label: 'The application'
	});
	await addTriggerRun({
		project_id: p.id,
		trigger: 'triage',
		state: 'done',
		summary: '12 issues triaged, 2 need you',
		source: 'clock',
		ago_minutes: 420,
		took_seconds: 251
	});
	await addTriggerRun({
		project_id: p.id,
		trigger: 'sweep',
		state: 'failed',
		reason: 'not_logged_in',
		source: 'clock',
		ago_minutes: 180,
		took_seconds: 14
	});
	return p;
}

test('a project with no triggers names the command that makes one', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'trig-empty',
		remote_url: 'github.com/heracraft/trig-empty'
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: 'Triggers', exact: true })).toBeVisible();
	await expect(section.getByText('No triggers yet')).toBeVisible();
	// The tab cannot write a prompt into the machine, so it does not
	// offer to make one (I-1258).
	await expect(section.getByText('repose triggers add')).toBeVisible();
	await expect(section.getByRole('button', { name: /add/i })).toHaveCount(0);
	await shots(page, 'triggers-empty');
});

test('the list shows each door, its last run and the next fire', async ({ page }) => {
	const p = await seeded('trig-app');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: '3 triggers' })).toBeVisible();

	// The door: a route trigger reads as its route, a schedule as its
	// spec. It is in its own column at 1440 and under the name at phone
	// width, so both cells carry it and the exact one is the column's.
	await expect(
		section.getByRole('cell', { name: 'recruiting (route)', exact: true })
	).toBeVisible();
	// The schedule's cell carries its zone beside the spec, so this is
	// anchored rather than exact; the name cell's copy reads
	// "triage weekdays 07:00…" and does not match.
	await expect(section.getByRole('cell', { name: /^weekdays 07:00/ })).toBeVisible();
	// The state word is the one the CLI prints.
	await expect(section.getByRole('cell', { name: /done/ }).first()).toBeVisible();
	await expect(section.getByRole('cell', { name: /failed/ }).first()).toBeVisible();
	// A trigger that is off says so and offers On.
	await expect(section.getByRole('button', { name: 'Turn sweep on' })).toBeVisible();
	await expect(section.getByRole('button', { name: 'Turn apply off' })).toBeVisible();
	// Every row offers the same three: Run, On/Off and Remove.
	await expect(section.getByRole('button', { name: 'Remove apply' })).toBeVisible();
	await expect(section.getByRole('button', { name: 'Remove sweep' })).toBeVisible();
	// At phone width the On column is not drawn, so the door moves under
	// the name: a list of names alone does not say which trigger fires
	// on a webhook and which on a clock.
	await page.setViewportSize({ width: 390, height: 844 });
	await expect(section.getByRole('cell', { name: 'apply recruiting (route)' })).toBeVisible();
	await page.setViewportSize({ width: 1280, height: 720 });
	// A route trigger's URL is in its expansion, on the clipboard, and
	// never on the page.
	await expect(page.locator('body')).not.toContainText('hooks.repose');
	await shots(page, 'triggers-list');
});

test('a row opens into its runs, with the agent’s own words', async ({ page }) => {
	const p = await seeded('trig-runs');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'apply', exact: true }).click();
	await expect(section.getByText('applied to Senior Go at Intel')).toBeVisible();
	// The agent's own link, with the host beside the label so the words
	// the agent chose are not the only thing saying where it leads
	// (I-931), and the door the run came in by.
	await expect(
		section.getByRole('link', { name: 'The application · jobs.example.com' })
	).toHaveAttribute('href', 'https://jobs.example.com/a/1829');
	await expect(section.getByText('route', { exact: true })).toBeVisible();
	// A route trigger's URL lives here, as a button and not as text.
	await expect(section.getByRole('button', { name: 'Copy the URL of apply' })).toBeVisible();
	await expect(page.locator('body')).not.toContainText('hooks.repose');
	await shots(page, 'triggers-runs');
});

test('Run fires the trigger and the row says it is running', async ({ page }) => {
	const p = await seeded('trig-fire');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Run apply now' }).click();
	// The fire is a row the machine has not been asked for yet, so the
	// run reads `queued` (I-1162's hand-off).
	await expect(section.getByRole('cell', { name: /queued/ }).first()).toBeVisible();
	// A fire is not an op: Stop stays available while it runs (I-1258).
	await expect(page.getByRole('button', { name: /^Stop/ })).toBeEnabled();
});

test('Remove asks in the row and names what goes with it', async ({ page }) => {
	const p = await seeded('trig-rm');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	// sweep is the one that is off, so its button offers On.
	await section.getByRole('button', { name: 'Turn sweep on' }).waitFor();
	await section.getByRole('button', { name: 'Remove sweep' }).click();
	const confirm = section.getByTestId('confirm-remove-trigger');
	await expect(confirm).toContainText('Its prompt file stays on the machine');
	await confirm.getByRole('button', { name: 'Remove' }).click();
	await expect(section.getByRole('heading', { name: '2 triggers' })).toBeVisible();
});
