// A project's Triggers tab (DECISIONS I-1248..I-1262, I-1266, I-1360):
// the list with each trigger's door, who answers it, its last run and
// the next fire; Run, Edit, On/Off and Remove in the row; the runs and
// the dry run under it; and the form that makes a trigger, including the
// prompt, which I-1266 let a browser write into the machine. A webhook
// route that forwards to the app is a row here too (webhooks.spec.ts).
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, addTriggerRun, triggerPrompt } from './helpers';

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

test('a project with no triggers offers to make one', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'trig-empty',
		remote_url: 'github.com/heracraft/trig-empty'
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: 'Triggers', exact: true })).toBeVisible();
	await expect(section.getByText('No triggers yet')).toBeVisible();
	// I-1266: the tab makes a trigger, so the empty state no longer
	// sends the reader to a terminal.
	await expect(section.getByRole('button', { name: 'Add trigger' })).toBeVisible();
	await expect(section.getByText('repose triggers add')).toHaveCount(0);
	await shots(page, 'triggers-empty');
});

test('the form makes a route trigger and the URL is a copy away', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'trig-new-route',
		remote_url: 'github.com/heracraft/trig-new-route'
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Add trigger' }).click();
	const form = section.getByTestId('trigger-form');
	await form.getByLabel('Name').fill('apply');
	await form.getByRole('button', { name: 'A webhook' }).click();
	// One door, two receivers (I-1360): a webhook starts an agent unless
	// Then says the app. There is no Route field; the URL is the
	// trigger's own.
	await expect(form.getByRole('button', { name: 'Start an agent' })).toHaveAttribute(
		'aria-pressed',
		'true'
	);
	await expect(form.getByLabel('Route')).toHaveCount(0);
	await form
		.getByPlaceholder('Triage the new issues')
		.fill('Read $REPOSE_PAYLOAD and apply if it fits');
	await shots(page, 'triggers-form');
	await form.getByRole('button', { name: 'More settings' }).click();
	await expect(form.getByTestId('trigger-more')).toBeVisible();
	await shots(page, 'triggers-form-more');
	await form.getByRole('button', { name: 'More settings' }).click();
	await form.getByRole('button', { name: 'Add trigger' }).click();

	await expect(section.getByRole('heading', { name: '1 trigger' })).toBeVisible();
	await expect(section.getByRole('cell', { name: 'webhook', exact: true })).toBeVisible();
	// The route is named after the trigger, so the CLI's `repose
	// webhooks` and this tab say the same name.
	const routes = await fetch(`${apiURLFromEnv()}/projects/${p.id}/webhooks`, {
		headers: { Authorization: 'Bearer playwright' }
	}).then((r) => r.json());
	expect(routes.map((r: { name: string; trigger?: string }) => [r.name, r.trigger])).toEqual([
		['apply', 'apply']
	]);
	// The prompt reached the machine and the api kept none of it: the
	// row holds a path, and no page ever draws the text (I-1266).
	expect(await triggerPrompt(p.id, 'apply')).toBe('Read $REPOSE_PAYLOAD and apply if it fits');
	const row = await fetch(`${apiURLFromEnv()}/projects/${p.id}/triggers`, {
		headers: { Authorization: 'Bearer playwright' }
	}).then((r) => r.json());
	expect(row[0].prompt_path).toBe('.repose/triggers/apply.md');
	expect(JSON.stringify(row)).not.toContain('REPOSE_PAYLOAD');
	// The URL is on the clipboard and never on the page, as a route's is.
	await expect(section.getByRole('button', { name: 'Copy the URL of apply' })).toBeVisible();
	await expect(page.locator('body')).not.toContainText('hooks.repose');
	await shots(page, 'triggers-new-route');
});

test('the form builds a schedule from the six shapes and says when it fires', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'trig-new-sched',
		remote_url: 'github.com/heracraft/trig-new-sched'
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Add trigger' }).click();
	const form = section.getByTestId('trigger-form');
	await form.getByLabel('Name').fill('triage');
	await form.getByRole('button', { name: 'A schedule' }).click();
	await form.getByLabel('When').selectOption('weekdays');
	await form.getByLabel('At').fill('07:00');
	await form.getByLabel('Time zone').fill('Europe/Berlin');
	// The spec and the zone are shown before it is made, because 07:00
	// means nothing without the zone it is read in (I-1252).
	await expect(form.getByTestId('trigger-spec')).toHaveText(
		'It fires weekdays 07:00 Europe/Berlin.'
	);
	await form.getByPlaceholder('Triage the new issues').fill('Triage the new issues');
	await form.getByRole('button', { name: 'Add trigger' }).click();

	await expect(section.getByRole('cell', { name: /^weekdays 07:00/ })).toBeVisible();
	expect(await triggerPrompt(p.id, 'triage')).toBe('Triage the new issues');
	await shots(page, 'triggers-new-schedule');
});

test("a prompt is refused while the machine is stopped, in the api's words", async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'trig-stopped',
		remote_url: 'github.com/heracraft/trig-stopped'
	});
	await fetch(`${api}/projects/${p.id}/stop`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright' }
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Add trigger' }).click();
	const form = section.getByTestId('trigger-form');
	await form.getByLabel('Name').fill('apply');
	await form.getByPlaceholder('Triage the new issues').fill('do the thing');
	await form.getByRole('button', { name: 'Add trigger' }).click();
	await expect(form.getByTestId('trigger-form-error')).toContainText(
		'a prompt is written into the machine'
	);
	// The other half of the field works on a stopped machine, as
	// `--prompt-file` does: the file is not read until the first fire.
	await form.getByRole('button', { name: 'A file in the machine' }).click();
	await form.getByPlaceholder('.repose/triggers/apply.md').fill('apply.md');
	await form.getByRole('button', { name: 'Add trigger' }).click();
	await expect(section.getByRole('heading', { name: '1 trigger' })).toBeVisible();
});

test('Edit opens on what the trigger is and changes the fields it names', async ({ page }) => {
	const p = await seeded('trig-edit');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Change triage' }).click();
	const form = section.getByTestId('trigger-form');
	// The schedule opens in the fields that made it, not as raw text.
	await expect(form.getByLabel('When')).toHaveValue('weekdays');
	await expect(form.getByLabel('At')).toHaveValue('07:00');
	await shots(page, 'triggers-edit');
	await expect(form.getByLabel('Name')).toBeDisabled();
	await form.getByLabel('At').fill('08:30');
	await form.getByRole('button', { name: 'More settings' }).click();
	await form.getByLabel('Timeout, minutes').fill('30');
	await form.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(section.getByRole('cell', { name: /^weekdays 08:30/ })).toBeVisible();
	const rows = await fetch(`${apiURLFromEnv()}/projects/${p.id}/triggers`, {
		headers: { Authorization: 'Bearer playwright' }
	}).then((r) => r.json());
	expect(rows.find((t: { name: string }) => t.name === 'triage').timeout_s).toBe(1800);
});

test('the list shows each door, its last run and the next fire', async ({ page }) => {
	const p = await seeded('trig-app');
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: '3 triggers' })).toBeVisible();

	// The door: a webhook trigger reads `webhook`, with the route's name
	// where the CLI gave it one of its own (`--route recruiting`); a
	// schedule reads as its spec. It is in its own column at 1440 and
	// under the name at phone width, so both cells carry it and the
	// exact one is the column's.
	await expect(
		section.getByRole('cell', { name: 'webhook recruiting', exact: true })
	).toBeVisible();
	// Who answers it, beside the door.
	await expect(section.getByRole('cell', { name: /^an agent/ }).first()).toBeVisible();
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
	// Every row offers the same four: Run, Edit, On/Off and Remove.
	await expect(section.getByRole('button', { name: 'Change apply' })).toBeVisible();
	await expect(section.getByRole('button', { name: 'Remove apply' })).toBeVisible();
	await expect(section.getByRole('button', { name: 'Remove sweep' })).toBeVisible();
	// At phone width the On column is not drawn, so the door moves under
	// the name: a list of names alone does not say which trigger fires
	// on a webhook and which on a clock.
	await page.setViewportSize({ width: 390, height: 844 });
	await expect(section.getByRole('cell', { name: 'apply webhook recruiting' })).toBeVisible();
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
	// A route trigger's URL lives here, as a button and not as text, and
	// so does the dry run that writes a gate (I-1254).
	await expect(section.getByRole('button', { name: 'Copy the URL of apply' })).toBeVisible();
	await expect(section.getByRole('button', { name: 'Dry run apply' })).toBeVisible();
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
